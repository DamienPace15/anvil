package compliance

import (
	"encoding/json"
	"testing"
)

func finding(check, status, uid, rtype string, labels []string, compliance map[string][]string) json.RawMessage {
	b, _ := json.Marshal(map[string]any{
		"status_code": status, "severity": "Medium", "status_detail": check + " detail",
		"metadata":     map[string]any{"event_code": check},
		"finding_info": map[string]any{"title": check + " title"},
		"remediation":  map[string]any{"desc": "fix it", "references": []string{"https://hub.prowler.com/check/" + check}},
		"resources":    []any{map[string]any{"uid": uid, "type": rtype, "region": "ap-southeast-2", "labels": labels}},
		"unmapped":     map[string]any{"compliance": compliance},
	})
	return b
}

func TestBuildView(t *testing.T) {
	site := "arn:aws:s3:::web-assets-b88b893"
	state := "arn:aws:s3:::damienpace-anvil-state-testwaf-e4d1f6"
	logs := "arn:aws:logs:us-east-1:1:log-group:aws-waf-logs-x:*"
	soc2 := map[string][]string{"SOC2": {"cc_7_2"}, "CIS-7.0": {"2.1.1"}, "GDPR": {"x"}}

	current := &ScanResult{
		Manifest: Manifest{ScanID: "20261008T120004Z-manual", Status: "complete", Frameworks: []string{"soc2_aws", "cis_7.0_aws", "dora_2022_2554"}},
		Findings: []json.RawMessage{
			finding("s3_bucket_object_versioning", "FAIL", site, "AwsS3Bucket", []string{"Component:SvelteKitSite"}, soc2),
			finding("s3_bucket_kms_encryption", "FAIL", state, "AwsS3Bucket", []string{"Component:StateBucket"}, soc2),
			finding("s3_bucket_default_encryption", "PASS", site, "AwsS3Bucket", nil, soc2),
		},
	}
	current.Manifest.Resources.ARNs = []string{site, state, logs}

	previous := &ScanResult{
		Manifest: Manifest{ScanID: "20261007T120004Z-scheduled", Status: "complete"},
		Findings: []json.RawMessage{
			finding("s3_bucket_kms_encryption", "FAIL", state, "AwsS3Bucket", nil, soc2),
			finding("s3_bucket_secure_transport_policy", "FAIL", site, "AwsS3Bucket", nil, soc2),
		},
	}

	v := BuildView(ViewInput{
		Project: "testwaf", Stage: "damienpace", Current: current, Previous: previous,
		Components: map[string]Component{
			site:               {Name: "web", Type: "SvelteKitSite"},
			NormaliseARN(logs): {Name: "testwaf", Type: "Waf"},
		},
	})

	byCheck := map[string]ViewFinding{}
	for _, f := range v.Findings {
		byCheck[f.Check] = f
	}
	if f := byCheck["s3_bucket_object_versioning"]; f.Component != "web" || f.Resource.Name != "web-assets-b88b893" || f.Resource.Kind != "S3 bucket" {
		t.Errorf("state mapping wrong: %+v", f)
	}
	if f := byCheck["s3_bucket_kms_encryption"]; f.Component != "State bucket" {
		t.Errorf("bootstrap fallback wrong: %+v", f.Component)
	}
	if got := byCheck["s3_bucket_object_versioning"].Frameworks; len(got) != 2 || got["SOC2"][0] != "cc_7_2" || got["CIS 7.0"][0] != "2.1.1" {
		t.Errorf("frameworks = %v (GDPR wasn't scanned, so it must be dropped)", got)
	}

	supported := map[string]bool{}
	for _, fw := range v.Frameworks {
		supported[fw.ID] = fw.Supported
	}
	if !supported["soc2_aws"] || supported["dora_2022_2554"] {
		t.Errorf("supported flags wrong: %v", supported)
	}

	var logRes ViewResource
	for _, r := range v.Resources {
		if r.UID == logs {
			logRes = r
		}
	}
	if logRes.Component != "testwaf" || logRes.Name != "aws-waf-logs-x" {
		t.Errorf("log group resource wrong: %+v", logRes)
	}

	if !v.Changes.Compared || len(v.Changes.New) != 1 || v.Changes.New[0] != findingID("s3_bucket_object_versioning", site) {
		t.Errorf("new = %v", v.Changes.New)
	}
	if len(v.Changes.Fixed) != 1 || v.Changes.Fixed[0].Check != "s3_bucket_secure_transport_policy" {
		t.Errorf("fixed = %+v", v.Changes.Fixed)
	}
}

func TestParseScanID(t *testing.T) {
	ref, ok := parseScanID("20261008T120004Z-manual")
	if !ok || ref.Trigger != "manual" || ref.Time != "2026-10-08T12:00:04Z" {
		t.Errorf("got %+v %v", ref, ok)
	}
	for _, bad := range []string{"latest.json", "2026-manual", "../x", ""} {
		if _, ok := parseScanID(bad); ok {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestArnName(t *testing.T) {
	for arn, want := range map[string]string{
		"arn:aws:lambda:ap-southeast-2:1:function:web-server-fb19113":          "web-server-fb19113",
		"arn:aws:iam::1:role/web-site-lambda-role-1253635":                     "web-site-lambda-role-1253635",
		"arn:aws:cloudfront::1:distribution/E15QPA8WB4SQLN":                    "E15QPA8WB4SQLN",
		"arn:aws:wafv2:us-east-1:1:global/webacl/damienpace-testwaf-waf/ee09":  "damienpace-testwaf-waf",
		"arn:aws:logs:us-east-1:1:log-group:aws-waf-logs-damienpace-testwaf:*": "aws-waf-logs-damienpace-testwaf",
		"arn:aws:s3:::web-assets-b88b893":                                      "web-assets-b88b893",
	} {
		if got := arnName(arn); got != want {
			t.Errorf("arnName(%s) = %q, want %q", arn, got, want)
		}
	}
}
