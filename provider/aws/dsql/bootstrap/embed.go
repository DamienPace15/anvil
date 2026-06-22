package bootstrap

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
)

//go:embed dsql-lambda.zip
var DSQLLambdaZip []byte

// WriteDSQLLambdaZip writes the embedded Lambda zip to a temp file and
// returns the path. The caller is responsible for cleanup.
func WriteDSQLLambdaZip() (string, error) {
	if len(DSQLLambdaZip) == 0 {
		return "", fmt.Errorf("dsql-lambda.zip not embedded — run `go run build.go build-dsql-lambda` before building the provider")
	}

	tmpDir, err := os.MkdirTemp("", "anvil-dsql-lambda-*")
	if err != nil {
		return "", fmt.Errorf("creating temp dir: %w", err)
	}

	zipPath := filepath.Join(tmpDir, "dsql-lambda.zip")
	if err := os.WriteFile(zipPath, DSQLLambdaZip, 0644); err != nil {
		os.RemoveAll(tmpDir)
		return "", fmt.Errorf("writing dsql-lambda.zip: %w", err)
	}

	return zipPath, nil
}
