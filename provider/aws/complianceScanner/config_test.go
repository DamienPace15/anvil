package complianceScanner

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

func base() ComplianceScannerArgs {
	return ComplianceScannerArgs{Frameworks: []string{"soc2", "iso27001", "cis"}}
}

func TestResolveDefaults(t *testing.T) {
	cfg, err := resolve(base(), "testwaf", "damienpace", "ap-southeast-2")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(cfg.Frameworks, " "); got != "soc2_aws iso27001_2022_aws cis_7.0_aws" {
		t.Errorf("frameworks = %s", got)
	}
	if cfg.Retention != "1y" || cfg.Schedule != "daily" || cfg.Timezone != "UTC" {
		t.Errorf("defaults wrong: %+v", cfg)
	}
	if !regexp.MustCompile(`^cron\(\d{1,2} \d{1,2} \* \* \? \*\)$`).MatchString(cfg.ScheduleExpression) {
		t.Errorf("daily expression = %s", cfg.ScheduleExpression)
	}
	if strings.Join(cfg.Regions, ",") != "ap-southeast-2" {
		t.Errorf("regions = %v", cfg.Regions)
	}
}

func TestFrameworksRequired(t *testing.T) {
	if _, err := resolve(ComplianceScannerArgs{}, "p", "s", "ap-southeast-2"); err == nil {
		t.Error("empty frameworks accepted")
	}
	args := base()
	args.Frameworks = []string{"soc3"}
	if _, err := resolve(args, "p", "s", "ap-southeast-2"); err == nil {
		t.Error("unknown framework accepted")
	}
	for _, raw := range []string{"gdpr_aws", "cis_6.0_aws", "dora_2022_2554", "cmmc_2.0", "csa_ccm_4.0"} {
		args.Frameworks = []string{raw}
		if _, err := resolve(args, "p", "s", "ap-southeast-2"); err != nil {
			t.Errorf("raw Prowler ID %s rejected: %v", raw, err)
		}
	}
	args.Frameworks = []string{"soc2_aw"}
	if _, err := resolve(args, "p", "s", "ap-southeast-2"); err == nil || !strings.Contains(err.Error(), `did you mean "soc2_aws"`) {
		t.Errorf("typo not suggested: %v", err)
	}
}

func TestStartTimeIsStableAndSpread(t *testing.T) {
	a1, _ := resolve(base(), "testwaf", "damienpace", "ap-southeast-2")
	a2, _ := resolve(base(), "testwaf", "damienpace", "ap-southeast-2")
	b, _ := resolve(base(), "billing", "prod", "ap-southeast-2")
	if a1.ScheduleExpression != a2.ScheduleExpression {
		t.Error("start time not stable across runs")
	}
	if a1.ScheduleExpression == b.ScheduleExpression {
		t.Log("two apps collided on the same start time (possible, but unlikely)")
	}
}

func TestWeeklyAndNone(t *testing.T) {
	args := base()
	args.Schedule = "weekly"
	cfg, err := resolve(args, "testwaf", "damienpace", "ap-southeast-2")
	if err != nil || !regexp.MustCompile(`^cron\(\d+ \d+ \? \* (SUN|MON|TUE|WED|THU|FRI|SAT) \*\)$`).MatchString(cfg.ScheduleExpression) {
		t.Errorf("weekly = %q, %v", cfg.ScheduleExpression, err)
	}
	args.Schedule = "none"
	cfg, err = resolve(args, "testwaf", "damienpace", "ap-southeast-2")
	if err != nil || cfg.ScheduleExpression != "" {
		t.Errorf("none = %q, %v", cfg.ScheduleExpression, err)
	}
}

func TestCron(t *testing.T) {
	args := base()
	args.Schedule = "cron"
	args.Timezone = "Australia/Sydney"
	for in, want := range map[string]string{
		"0 3 * * ? *":          "cron(0 3 * * ? *)",
		"cron(30 2 ? * MON *)": "cron(30 2 ? * MON *)",
	} {
		args.Cron = in
		cfg, err := resolve(args, "p", "s", "ap-southeast-2")
		if err != nil || cfg.ScheduleExpression != want || cfg.Timezone != "Australia/Sydney" {
			t.Errorf("cron %q → %q (%v)", in, cfg.ScheduleExpression, err)
		}
	}
	for _, bad := range []string{"* * * * ? *", "*/5 * * * ? *", "0 3 * *", "0 3 * * ? *; rm -rf /", ""} {
		args.Cron = bad
		if _, err := resolve(args, "p", "s", "ap-southeast-2"); err == nil {
			t.Errorf("cron %q accepted", bad)
		}
	}
	args.Cron = "0 3 * * ? *"
	args.Timezone = "Mars/Olympus Mons"
	if _, err := resolve(args, "p", "s", "ap-southeast-2"); err == nil {
		t.Error("invalid timezone accepted")
	}
}

func TestRetentionAndRegions(t *testing.T) {
	args := base()
	args.Retention = "9m"
	if _, err := resolve(args, "p", "s", "ap-southeast-2"); err == nil {
		t.Error("unknown retention accepted")
	}
	args.Retention = "7y"
	args.Regions = []string{"us-west-2", "ap-southeast-2", "us-west-2"}
	cfg, err := resolve(args, "p", "s", "ap-southeast-2")
	if err != nil || strings.Join(cfg.Regions, ",") != "ap-southeast-2,us-west-2" {
		t.Errorf("regions = %v, %v", cfg.Regions, err)
	}
	args.Regions = []string{"not a region"}
	if _, err := resolve(args, "p", "s", "ap-southeast-2"); err == nil {
		t.Error("invalid region accepted")
	}
}

func TestScheduleName(t *testing.T) {
	if got := scheduleName("testwaf", "damienpace"); got != "anvil-testwaf-damienpace" {
		t.Errorf("got %q", got)
	}
	long := scheduleName(strings.Repeat("project", 10), "stage")
	if len(long) > 64 || long == scheduleName(strings.Repeat("project", 10), "stage2") {
		t.Errorf("long name %q (%d chars) not truncated uniquely", long, len(long))
	}
	if got := scheduleName("my app", "dev/1"); !regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`).MatchString(got) {
		t.Errorf("invalid characters kept: %q", got)
	}
}

func TestStartBuildInput(t *testing.T) {
	cfg, _ := resolve(base(), "testwaf", "damienpace", "ap-southeast-2")
	in, err := startBuildInput("904233095613", "ap-southeast-2", "testwaf", "damienpace", cfg)
	if err != nil {
		t.Fatal(err)
	}
	// Scheduler requires PascalCase keys; decode strictly so camelCase fails.
	for _, key := range []string{`"ProjectName"`, `"EnvironmentVariablesOverride"`, `"Name"`, `"Value"`, `"Type"`} {
		if !strings.Contains(in, key) {
			t.Fatalf("input missing %s: %s", key, in)
		}
	}
	var req struct {
		ProjectName string
		Env         []struct {
			Name, Value, Type string
		} `json:"EnvironmentVariablesOverride"`
	}
	if err := json.Unmarshal([]byte(in), &req); err != nil {
		t.Fatal(err)
	}
	if req.ProjectName != "anvil-compliance-904233095613-ap-southeast-2" {
		t.Errorf("project = %s", req.ProjectName)
	}
	got := map[string]string{}
	for _, e := range req.Env {
		got[e.Name] = e.Value
		if e.Type != "PLAINTEXT" {
			t.Errorf("%s type = %s", e.Name, e.Type)
		}
	}
	want := map[string]string{
		"ANVIL_PROJECT": "testwaf", "ANVIL_STAGE": "damienpace", "ANVIL_REGIONS": "ap-southeast-2",
		"ANVIL_FRAMEWORKS": "soc2_aws iso27001_2022_aws cis_7.0_aws", "ANVIL_RETENTION": "1y", "ANVIL_TRIGGER": "scheduled",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}

func TestFriendlyNamesPointAtKnownIDs(t *testing.T) {
	for name, id := range friendlyFrameworks {
		if !prowlerFrameworks[id] {
			t.Errorf("friendly name %s → %s, which isn't in the pinned Prowler list", name, id)
		}
	}
}
