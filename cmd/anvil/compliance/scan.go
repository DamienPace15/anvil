package compliance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/codebuild"
	cbtypes "github.com/aws/aws-sdk-go-v2/service/codebuild/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// ScanRequest is one app's scan scope. Mirrors scan.py's inputs, which
// re-validate everything inside the build.
type ScanRequest struct {
	Project    string
	Stage      string
	Regions    []string
	Frameworks []string // Prowler IDs
	Retention  string
	Trigger    string // scheduled | deploy | manual
}

var (
	nameRe   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	regionRe = regexp.MustCompile(`^[a-z]{2}(-[a-z]+)+-[0-9]$`)
)

func (r ScanRequest) validate() error {
	if !nameRe.MatchString(r.Project) {
		return fmt.Errorf("invalid project name %q", r.Project)
	}
	if !nameRe.MatchString(r.Stage) {
		return fmt.Errorf("invalid stage name %q", r.Stage)
	}
	if len(r.Regions) == 0 {
		return errors.New("at least one region is required")
	}
	for _, reg := range r.Regions {
		if !regionRe.MatchString(reg) {
			return fmt.Errorf("invalid region %q", reg)
		}
	}
	if len(r.Frameworks) == 0 {
		return errors.New("at least one framework is required")
	}
	if !ValidRetention(r.Retention) {
		return fmt.Errorf("retention must be one of %s", strings.Join(RetentionTiers, ", "))
	}
	return nil
}

// EnvOverrides are the CodeBuild environment overrides for this scan. Schedules
// send the same variables.
func (r ScanRequest) EnvOverrides() []cbtypes.EnvironmentVariable {
	vars := [][2]string{
		{"ANVIL_PROJECT", r.Project},
		{"ANVIL_STAGE", r.Stage},
		{"ANVIL_REGIONS", strings.Join(r.Regions, " ")},
		{"ANVIL_FRAMEWORKS", strings.Join(r.Frameworks, " ")},
		{"ANVIL_RETENTION", r.Retention},
		{"ANVIL_TRIGGER", r.Trigger},
	}
	out := make([]cbtypes.EnvironmentVariable, 0, len(vars))
	for _, v := range vars {
		out = append(out, cbtypes.EnvironmentVariable{
			Name: aws.String(v[0]), Value: aws.String(v[1]), Type: cbtypes.EnvironmentVariableTypePlaintext,
		})
	}
	return out
}

// StartScan starts a CodeBuild scan and returns the build ID.
func (s *Shared) StartScan(ctx context.Context, req ScanRequest) (string, error) {
	if err := req.validate(); err != nil {
		return "", err
	}
	out, err := s.cb.StartBuild(ctx, &codebuild.StartBuildInput{
		ProjectName:                  aws.String(s.Names.Project()),
		EnvironmentVariablesOverride: req.EnvOverrides(),
	})
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "concurrent") {
			return "", fmt.Errorf("a scan is already running and the build limit is reached; try again shortly")
		}
		return "", err
	}
	return aws.ToString(out.Build.Id), nil
}

// BuildResult is the final state of a scan build.
type BuildResult struct {
	Status  string // SUCCEEDED, FAILED, FAULT, TIMED_OUT, STOPPED
	LogTail []string
	LogLink string
}

// WaitForScan polls the build until it finishes, reporting phase changes.
func (s *Shared) WaitForScan(ctx context.Context, buildID string, onPhase func(string)) (BuildResult, error) {
	last := ""
	for {
		out, err := s.cb.BatchGetBuilds(ctx, &codebuild.BatchGetBuildsInput{Ids: []string{buildID}})
		if err != nil {
			return BuildResult{}, err
		}
		if len(out.Builds) == 0 {
			return BuildResult{}, fmt.Errorf("build %s not found", buildID)
		}
		b := out.Builds[0]
		if phase := aws.ToString(b.CurrentPhase); phase != last && phase != "" && onPhase != nil {
			onPhase(phase)
			last = phase
		}
		if b.BuildComplete {
			res := BuildResult{Status: string(b.BuildStatus)}
			if b.Logs != nil {
				res.LogLink = aws.ToString(b.Logs.DeepLink)
				res.LogTail = s.logTail(ctx, aws.ToString(b.Logs.GroupName), aws.ToString(b.Logs.StreamName), 25)
			}
			return res, nil
		}
		select {
		case <-ctx.Done():
			return BuildResult{}, ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
}

func (s *Shared) logTail(ctx context.Context, group, stream string, n int32) []string {
	if group == "" || stream == "" {
		return nil
	}
	out, err := s.logs.GetLogEvents(ctx, &cloudwatchlogs.GetLogEventsInput{
		LogGroupName: aws.String(group), LogStreamName: aws.String(stream), Limit: aws.Int32(n),
	})
	if err != nil {
		return nil
	}
	var lines []string
	for _, e := range out.Events {
		lines = append(lines, strings.TrimRight(aws.ToString(e.Message), "\n"))
	}
	return lines
}

// Manifest is the subset of scan.py's manifest.json the CLI reads.
type Manifest struct {
	ScanID          string   `json:"scanId"`
	Status          string   `json:"status"`
	Trigger         string   `json:"trigger"`
	Frameworks      []string `json:"frameworks"`
	Regions         []string `json:"regions"`
	ProwlerVersion  string   `json:"prowlerVersion"`
	StartedAt       string   `json:"startedAt"`
	FinishedAt      string   `json:"finishedAt"`
	DurationSeconds float64  `json:"durationSeconds"`
	Error           string   `json:"error"`
	Resources       struct {
		Count int      `json:"count"`
		ARNs  []string `json:"arns"`
	} `json:"resources"`
	DroppedOutOfScope int `json:"droppedOutOfScope"`
	Findings          struct {
		Total          int            `json:"total"`
		Pass           int            `json:"pass"`
		Fail           int            `json:"fail"`
		Manual         int            `json:"manual"`
		Muted          int            `json:"muted"`
		FailBySeverity map[string]int `json:"failBySeverity"`
	} `json:"findings"`
}

// LatestManifest reads the newest complete scan for project/stage. Returns nil
// when the app has never been scanned.
func (s *Shared) LatestManifest(ctx context.Context, project, stage string) (*Manifest, error) {
	var pointer struct {
		Manifest string `json:"manifest"`
	}
	found, err := s.getJSON(ctx, fmt.Sprintf("results/%s/%s/latest.json", project, stage), &pointer)
	if err != nil || !found {
		return nil, err
	}
	var m Manifest
	if _, err := s.getJSON(ctx, pointer.Manifest, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

func (s *Shared) getJSON(ctx context.Context, key string, v any) (bool, error) {
	out, err := s.s3.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.Names.Bucket()), Key: aws.String(key), ExpectedBucketOwner: aws.String(s.Names.Account),
	})
	if httpStatus(err) == 404 {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer out.Body.Close()
	body, err := io.ReadAll(out.Body)
	if err != nil {
		return false, err
	}
	return true, json.Unmarshal(body, v)
}
