// Package compliance manages Anvil's Prowler-based compliance scanning: the
// shared per-account layer (results bucket, roles, CodeBuild project, schedule
// group) and starting scans. See docs/design/compliance-scanning.md.
package compliance

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"encoding/base64"
	"fmt"
)

// scanScript runs inside the Prowler image. It is shipped in the CodeBuild
// buildspec, so the upstream image is used unmodified.
//
//go:embed scan.py
var scanScript []byte

const (
	// ProwlerVersion is the Prowler release this Anvil version is tested with.
	ProwlerVersion = "5.44.0"

	// ProwlerImage is pinned by digest and pulled from AWS's public registry,
	// which avoids Docker Hub's anonymous pull limits from shared CodeBuild IPs.
	ProwlerImage = "public.ecr.aws/prowler-cloud/prowler@sha256:9dbcc3c9abd71ec6d37610e6a5a2c45279bc6488caf8aaa948a9e8e368622d30"

	prowlerPython = "/home/prowler/.venv/bin/python"
)

// Buildspec returns the CodeBuild buildspec. The scanner travels gzipped and
// base64-encoded and is unpacked with the image's own Python, so the build
// depends on nothing but the pinned image.
func Buildspec() (string, error) {
	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return "", err
	}
	if _, err := zw.Write(scanScript); err != nil {
		return "", err
	}
	if err := zw.Close(); err != nil {
		return "", err
	}
	payload := base64.StdEncoding.EncodeToString(buf.Bytes())

	return fmt.Sprintf(`version: 0.2
phases:
  build:
    commands:
      - |
        %[1]s -c "import base64, gzip, sys; open('/tmp/anvil-scan.py', 'wb').write(gzip.decompress(base64.b64decode(sys.argv[1])))" %[2]s
      - %[1]s -B /tmp/anvil-scan.py
`, prowlerPython, payload), nil
}
