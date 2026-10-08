package waf

import (
	"reflect"
	"regexp"
	"strings"
	"testing"
)

func ptr[T any](v T) *T { return &v }

func groupKeys(cfg config) []string {
	var keys []string
	for _, g := range cfg.groups {
		keys = append(keys, g.key)
	}
	return keys
}

func TestDefaults(t *testing.T) {
	cfg, err := resolveConfig(WafArgs{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.scope != ScopeCloudFront || cfg.mode != ModeCount {
		t.Errorf("scope/mode = %q/%q, want cloudfront/count", cfg.scope, cfg.mode)
	}
	if cfg.rateLimit != 2000 || cfg.windowSeconds != 300 {
		t.Errorf("rate limit = %d/%ds, want 2000/300s", cfg.rateLimit, cfg.windowSeconds)
	}
	if want := []string{"ipReputation", "knownBadInputs", "core", "sqli"}; !reflect.DeepEqual(groupKeys(cfg), want) {
		t.Errorf("groups = %v, want %v", groupKeys(cfg), want)
	}
	if !cfg.logging || cfg.retentionDays != 30 || cfg.includeAllowed {
		t.Errorf("logging = %v/%d/%v, want on/30/blocked-and-counted only", cfg.logging, cfg.retentionDays, cfg.includeAllowed)
	}
	for _, g := range cfg.groups {
		if g.key == "core" && g.overrides["SizeRestrictions_BODY"] != ModeCount {
			t.Errorf("core SizeRestrictions_BODY override = %q, want count", g.overrides["SizeRestrictions_BODY"])
		}
	}
	if len(cfg.notices) != 0 {
		t.Errorf("unexpected notices %v", cfg.notices)
	}
}

func TestClientIPHeaderDisablesIPBasedGroups(t *testing.T) {
	cfg, err := resolveConfig(WafArgs{ClientIpHeader: "CF-Connecting-IP"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"knownBadInputs", "core", "sqli"}; !reflect.DeepEqual(groupKeys(cfg), want) {
		t.Errorf("groups = %v, want %v", groupKeys(cfg), want)
	}
	if len(cfg.notices) != 1 || !strings.Contains(cfg.notices[0], "managedRules.ipReputation is off") {
		t.Errorf("notices = %v", cfg.notices)
	}

	// Explicit wins, with a notice.
	cfg, err = resolveConfig(WafArgs{ClientIpHeader: "CF-Connecting-IP", ManagedRules: &WafManagedRulesArgs{IpReputation: ptr(true)}})
	if err != nil {
		t.Fatal(err)
	}
	if groupKeys(cfg)[0] != "ipReputation" || !strings.Contains(cfg.notices[0], "is on while clientIpHeader is set") {
		t.Errorf("groups = %v, notices = %v", groupKeys(cfg), cfg.notices)
	}
}

func TestRuleOverrides(t *testing.T) {
	cfg, err := resolveConfig(WafArgs{
		Mode:          ModeBlock,
		RuleOverrides: map[string]string{"SizeRestrictions_BODY": "block", "SQLi_BODY": "count"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range cfg.groups {
		switch g.key {
		case "core":
			if g.overrides["SizeRestrictions_BODY"] != ModeBlock {
				t.Errorf("user override should replace the default, got %v", g.overrides)
			}
		case "sqli":
			if g.overrides["SQLi_BODY"] != ModeCount {
				t.Errorf("override not routed to sqli: %v", g.overrides)
			}
		}
	}
}

func TestIPListsAndCountries(t *testing.T) {
	cfg, err := resolveConfig(WafArgs{
		IpAllowList:    []string{"203.0.113.7", "2001:db8::/32", "198.51.100.0/24"},
		BlockCountries: []string{"kp", " RU "},
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"198.51.100.0/24", "203.0.113.7/32"}; !reflect.DeepEqual(cfg.allowV4, want) {
		t.Errorf("allowV4 = %v, want %v", cfg.allowV4, want)
	}
	if want := []string{"2001:db8::/32"}; !reflect.DeepEqual(cfg.allowV6, want) {
		t.Errorf("allowV6 = %v, want %v", cfg.allowV6, want)
	}
	if want := []string{"KP", "RU"}; !reflect.DeepEqual(cfg.blockCountries, want) {
		t.Errorf("blockCountries = %v, want %v", cfg.blockCountries, want)
	}
}

func TestDisableThings(t *testing.T) {
	cfg, err := resolveConfig(WafArgs{
		RateLimit:    &WafRateLimitArgs{Enabled: ptr(false)},
		Logging:      &WafLoggingArgs{Enabled: ptr(false)},
		ManagedRules: &WafManagedRulesArgs{Core: ptr(false), AnonymousIp: ptr(true)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.rateLimit != 0 || cfg.logging {
		t.Errorf("rate limit/logging should be off: %d/%v", cfg.rateLimit, cfg.logging)
	}
	if want := []string{"ipReputation", "anonymousIp", "knownBadInputs", "sqli"}; !reflect.DeepEqual(groupKeys(cfg), want) {
		t.Errorf("groups = %v, want %v", groupKeys(cfg), want)
	}
}

func TestValidation(t *testing.T) {
	tests := []struct {
		name    string
		args    WafArgs
		wantErr string
	}{
		{"bad scope", WafArgs{Scope: "global"}, `invalid scope "global"`},
		{"bad mode", WafArgs{Mode: "deny"}, `invalid mode "deny"`},
		{"rate too low", WafArgs{RateLimit: &WafRateLimitArgs{Limit: 5}}, "invalid rateLimit.limit 5"},
		{"bad window", WafArgs{RateLimit: &WafRateLimitArgs{WindowSeconds: 30}}, "invalid rateLimit.windowSeconds 30"},
		{"unknown rule", WafArgs{RuleOverrides: map[string]string{"NotARule": "count"}}, `"NotARule" isn't a rule`},
		{"bad action", WafArgs{RuleOverrides: map[string]string{"SQLi_BODY": "deny"}}, `must be "count", "block" or "allow"`},
		{"rule in disabled group", WafArgs{
			ManagedRules:  &WafManagedRulesArgs{Sqli: ptr(false)},
			RuleOverrides: map[string]string{"SQLi_BODY": "count"},
		}, "belongs to managedRules.sqli, which is off"},
		{"bad cidr", WafArgs{IpBlockList: []string{"300.1.1.1"}}, `invalid ipBlockList entry "300.1.1.1"`},
		{"bad country", WafArgs{BlockCountries: []string{"USA"}}, `invalid blockCountries entry "USA"`},
		{"bad retention", WafArgs{Logging: &WafLoggingArgs{RetentionDays: 45}}, "invalid logging.retentionDays 45"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := resolveConfig(tt.args)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("err = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestDescription(t *testing.T) {
	// Must match AWS's pattern: ^[\w+=:#@/\-,\.][\w+=:#@/\-,\.\s]+[\w+=:#@/\-,\.]$
	valid := regexp.MustCompile(`^[\w+=:#@/\-,.][\w+=:#@/\-,.\s]+[\w+=:#@/\-,.]$`)
	for _, in := range []string{"Anvil WAF testwaf, stage damienpace", "Anvil WAF my(waf)!, stage dev*"} {
		if out := description(in); !valid.MatchString(out) {
			t.Errorf("description(%q) = %q, rejected by AWS pattern", in, out)
		}
	}
}
