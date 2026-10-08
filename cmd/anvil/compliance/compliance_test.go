package compliance

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"io"
	"regexp"
	"strings"
	"testing"
)

var testNames = Names{Account: "123456789012", Region: "ap-southeast-2"}

func TestNames(t *testing.T) {
	if got := testNames.Bucket(); got != "anvil-compliance-123456789012-ap-southeast-2" {
		t.Errorf("bucket = %q", got)
	}
	if got := testNames.ProjectArn(); got != "arn:aws:codebuild:ap-southeast-2:123456789012:project/anvil-compliance-123456789012-ap-southeast-2" {
		t.Errorf("project arn = %q", got)
	}
	for _, role := range []string{testNames.ScanRole(), testNames.InvokeRole()} {
		if len(role) > 64 {
			t.Errorf("role name %q exceeds IAM's 64 chars", role)
		}
	}
}

func TestBuildspecRoundTripsScanner(t *testing.T) {
	spec, err := Buildspec()
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`sys\.argv\[1\]\)\)\)" ([A-Za-z0-9+/=]+)`).FindStringSubmatch(spec)
	if m == nil {
		t.Fatalf("payload not found in buildspec:\n%s", spec)
	}
	raw, err := base64.StdEncoding.DecodeString(m[1])
	if err != nil {
		t.Fatal(err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(zr)
	if !bytes.Equal(got, scanScript) {
		t.Error("decoded payload differs from scan.py")
	}
	// CodeBuild caps inline buildspecs; keep well clear.
	if len(spec) > 20000 {
		t.Errorf("buildspec is %d bytes", len(spec))
	}
}

func decode(t *testing.T, doc string) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal([]byte(doc), &v); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, doc)
	}
	return v
}

func TestScanTrustIsScopedToProject(t *testing.T) {
	doc := scanTrustPolicy(testNames)
	decode(t, doc)
	for _, want := range []string{`"aws:SourceAccount":"123456789012"`, `"aws:SourceArn":"` + testNames.ProjectArn() + `"`, "codebuild.amazonaws.com"} {
		if !strings.Contains(doc, want) {
			t.Errorf("trust policy missing %s:\n%s", want, doc)
		}
	}
}

func TestScanPolicyDeniesDataReadsAndGrantsNoWrites(t *testing.T) {
	doc := decode(t, scanInlinePolicy(testNames))
	var allowed []string
	denied := map[string]bool{}
	for _, raw := range doc["Statement"].([]any) {
		st := raw.(map[string]any)
		var actions []string
		switch a := st["Action"].(type) {
		case string:
			actions = []string{a}
		case []any:
			for _, x := range a {
				actions = append(actions, x.(string))
			}
		}
		if st["Effect"] == "Deny" {
			for _, a := range actions {
				denied[a] = true
			}
			continue
		}
		if st["Sid"] == "WriteResults" {
			if st["Resource"] != testNames.BucketArn()+"/results/*" {
				t.Errorf("results write not scoped: %v", st["Resource"])
			}
			continue
		}
		if st["Sid"] == "WriteBuildLogs" {
			continue
		}
		allowed = append(allowed, actions...)
	}

	for _, a := range []string{"s3:GetObject", "logs:FilterLogEvents", "lambda:GetFunction", "ecr:BatchGetImage", "secretsmanager:GetSecretValue"} {
		if !denied[a] {
			t.Errorf("%s should be explicitly denied", a)
		}
	}
	for _, a := range allowed {
		if strings.HasPrefix(a, "lambda:GetFunction") && a != "lambda:GetFunctionConfiguration" &&
			a != "lambda:GetFunctionUrlConfig" && a != "lambda:GetFunctionCodeSigningConfig" && a != "lambda:GetFunctionConcurrency" {
			t.Errorf("allowed %s, which can download code", a)
		}
		if a == "securityhub:BatchImportFindings" || strings.HasSuffix(a, ":*") {
			t.Errorf("allowed %s", a)
		}
	}
}

func TestInvokePolicyOnlyStartsThisProject(t *testing.T) {
	doc := invokeInlinePolicy(testNames)
	decode(t, doc)
	if !strings.Contains(doc, `"Action":"codebuild:StartBuild"`) || !strings.Contains(doc, testNames.ProjectArn()) {
		t.Errorf("unexpected invoke policy: %s", doc)
	}
}

func TestLifecycleRulesCoverEveryTier(t *testing.T) {
	rules := lifecycleRules()
	if len(rules) != len(RetentionTiers)+1 {
		t.Fatalf("got %d rules", len(rules))
	}
	for i, tier := range RetentionTiers {
		r := rules[i]
		if *r.Filter.Tag.Value != tier || *r.Expiration.Days != retentionDays[tier] || r.NoncurrentVersionExpiration == nil {
			t.Errorf("rule for %s is wrong: %+v", tier, r)
		}
	}
}

func TestResolveFrameworks(t *testing.T) {
	got, err := ResolveFrameworks([]string{"soc2", "ISO27001", "cis", "soc2"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, " ") != "soc2_aws iso27001_2022_aws cis_7.0_aws" {
		t.Errorf("got %v", got)
	}
	for _, raw := range []string{"gdpr_aws", "dora_2022_2554", "cmmc_2.0", "cis_6.0_aws"} {
		if _, err := ResolveFrameworks([]string{raw}); err != nil {
			t.Errorf("raw Prowler ID %s rejected: %v", raw, err)
		}
	}
	for name, id := range friendlyFrameworks {
		if !prowlerFrameworks[id] {
			t.Errorf("friendly name %s → %s is not in the pinned Prowler list", name, id)
		}
	}
	for _, bad := range []string{"soc3", "x; rm -rf /", ""} {
		if _, err := ResolveFrameworks([]string{bad}); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestNeedsConverge(t *testing.T) {
	cases := []struct {
		st   State
		cli  string
		want bool
	}{
		{State{}, "1.0.0", true},
		{State{Exists: true, Version: "1.0.0"}, "1.0.0", false},
		{State{Exists: true, Version: "1.0.0"}, "1.1.0", true},
		{State{Exists: true, Version: "1.2.0"}, "1.1.0", false}, // never downgrade
		{State{Exists: true, Version: "1.0.0"}, "dev", true},
		{State{Exists: true, Version: "dev"}, "1.0.0", false},
	}
	for _, c := range cases {
		if got := NeedsConverge(c.st, c.cli); got != c.want {
			t.Errorf("NeedsConverge(%+v, %q) = %v, want %v", c.st, c.cli, got, c.want)
		}
	}
}

func TestScanRequestValidation(t *testing.T) {
	ok := ScanRequest{Project: "testwaf", Stage: "damienpace", Regions: []string{"ap-southeast-2"},
		Frameworks: []string{"soc2_aws"}, Retention: "1y", Trigger: "manual"}
	if err := ok.validate(); err != nil {
		t.Fatal(err)
	}
	bad := ok
	bad.Stage = "$(id)"
	if bad.validate() == nil {
		t.Error("shell characters accepted in stage")
	}
	bad = ok
	bad.Retention = "9m"
	if bad.validate() == nil {
		t.Error("unknown retention accepted")
	}
}
