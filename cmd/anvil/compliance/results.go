package compliance

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// ScanRef identifies a stored scan. Scan IDs are "<UTC timestamp>-<trigger>",
// so they sort chronologically.
type ScanRef struct {
	ID      string `json:"id"`
	Trigger string `json:"trigger"`
	Time    string `json:"time"` // RFC 3339, derived from the ID
}

// ListScans returns project/stage's scans, newest first, at most limit.
func (s *Shared) ListScans(ctx context.Context, project, stage string, limit int) ([]ScanRef, error) {
	prefix := fmt.Sprintf("results/%s/%s/", project, stage)
	var refs []ScanRef
	p := s3.NewListObjectsV2Paginator(s.s3, &s3.ListObjectsV2Input{
		Bucket: aws.String(s.Names.Bucket()), Prefix: aws.String(prefix), Delimiter: aws.String("/"),
		ExpectedBucketOwner: aws.String(s.Names.Account),
	})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, cp := range page.CommonPrefixes {
			id := strings.TrimSuffix(strings.TrimPrefix(aws.ToString(cp.Prefix), prefix), "/")
			if ref, ok := parseScanID(id); ok {
				refs = append(refs, ref)
			}
		}
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].ID > refs[j].ID })
	if limit > 0 && len(refs) > limit {
		refs = refs[:limit]
	}
	return refs, nil
}

// parseScanID splits "20261008T120004Z-manual".
func parseScanID(id string) (ScanRef, bool) {
	ts, trigger, ok := strings.Cut(id, "-")
	if !ok || len(ts) != 16 || ts[8] != 'T' || ts[15] != 'Z' {
		return ScanRef{}, false
	}
	t := fmt.Sprintf("%s-%s-%sT%s:%s:%sZ", ts[0:4], ts[4:6], ts[6:8], ts[9:11], ts[11:13], ts[13:15])
	return ScanRef{ID: id, Trigger: trigger, Time: t}, true
}

// ScanResult is one stored scan: its manifest and, when complete, the
// app's findings in Prowler's OCSF format.
type ScanResult struct {
	Manifest Manifest
	Raw      json.RawMessage // manifest.json, verbatim
	Findings []json.RawMessage
}

// LoadScan reads a scan's manifest and findings.
func (s *Shared) LoadScan(ctx context.Context, project, stage, scanID string) (*ScanResult, error) {
	if _, ok := parseScanID(scanID); !ok {
		return nil, fmt.Errorf("invalid scan ID %q", scanID)
	}
	prefix := fmt.Sprintf("results/%s/%s/%s/", project, stage, scanID)

	var raw json.RawMessage
	found, err := s.getJSON(ctx, prefix+"manifest.json", &raw)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("scan %s has no manifest (still running, or interrupted)", scanID)
	}
	res := &ScanResult{Raw: raw}
	if err := json.Unmarshal(raw, &res.Manifest); err != nil {
		return nil, fmt.Errorf("scan %s: invalid manifest: %w", scanID, err)
	}
	if res.Manifest.Status != "complete" {
		return res, nil
	}
	if _, err := s.getJSON(ctx, prefix+"findings.ocsf.json", &res.Findings); err != nil {
		return nil, fmt.Errorf("scan %s: reading findings: %w", scanID, err)
	}
	return res, nil
}
