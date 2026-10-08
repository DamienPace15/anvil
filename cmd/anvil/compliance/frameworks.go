package compliance

import (
	"fmt"
	"sort"
	"strings"
)

// Frameworks can be given by friendly name or by raw Prowler compliance ID.
// Friendly names always point at the newest version Anvil pins; raw IDs stay
// fixed (e.g. cis_6.0_aws for an older CIS).
//
// Keep in sync with provider/aws/complianceScanner/frameworks.go, and update
// both when ProwlerImage (embed.go) is bumped.
var friendlyFrameworks = map[string]string{
	"soc2":             "soc2_aws",
	"iso27001":         "iso27001_2022_aws",
	"cis":              "cis_7.0_aws",
	"nist-800-53":      "nist_800_53_revision_5_aws",
	"nist-800-171":     "nist_800_171_revision_2_aws",
	"nist-csf":         "nist_csf_2.0_aws",
	"pci":              "pci_4.0_aws",
	"hipaa":            "hipaa_aws",
	"fsbp":             "aws_foundational_security_best_practices_aws",
	"gdpr":             "gdpr_aws",
	"nis2":             "nis2_aws",
	"dora":             "dora_2022_2554",
	"fedramp-low":      "fedramp_low_revision_4_aws",
	"fedramp-moderate": "fedramp_moderate_revision_4_aws",
	"cmmc":             "cmmc_2.0",
	"essential-eight":  "asd_essential_eight_aws",
	"well-architected": "aws_well_architected_framework_security_pillar_aws",
	"c5":               "c5_aws",
	"csa-ccm":          "csa_ccm_4.0",
}

// prowlerFrameworks is every AWS compliance ID in Prowler 5.44.0
// (`prowler aws --list-compliance`).
var prowlerFrameworks = map[string]bool{
	"asd_essential_eight_aws":                               true,
	"aws_account_security_onboarding_aws":                   true,
	"aws_ai_security_framework_aws":                         true,
	"aws_audit_manager_control_tower_guardrails_aws":        true,
	"aws_foundational_security_best_practices_aws":          true,
	"aws_foundational_technical_review_aws":                 true,
	"aws_well_architected_framework_reliability_pillar_aws": true,
	"aws_well_architected_framework_security_pillar_aws":    true,
	"c5_aws":                           true,
	"ccc_aws":                          true,
	"cis_1.4_aws":                      true,
	"cis_1.5_aws":                      true,
	"cis_2.0_aws":                      true,
	"cis_3.0_aws":                      true,
	"cis_4.0_aws":                      true,
	"cis_5.0_aws":                      true,
	"cis_6.0_aws":                      true,
	"cis_7.0_aws":                      true,
	"cis_controls_8.1":                 true,
	"cisa_aws":                         true,
	"cmmc_2.0":                         true,
	"csa_ccm_4.0":                      true,
	"dora_2022_2554":                   true,
	"ens_rd2022_aws":                   true,
	"fedramp_20x_frr_class_c_2026":     true,
	"fedramp_20x_ksi_2026":             true,
	"fedramp_low_revision_4_aws":       true,
	"fedramp_moderate_revision_4_aws":  true,
	"ffiec_aws":                        true,
	"gdpr_aws":                         true,
	"gxp_21_cfr_part_11_aws":           true,
	"gxp_eu_annex_11_aws":              true,
	"hipaa_aws":                        true,
	"iso27001_2013_aws":                true,
	"iso27001_2022_aws":                true,
	"kisa_isms_p_2023_aws":             true,
	"kisa_isms_p_2023_korean_aws":      true,
	"mitre_attack_aws":                 true,
	"nis2_aws":                         true,
	"nist_800_171_revision_2_aws":      true,
	"nist_800_53_revision_4_aws":       true,
	"nist_800_53_revision_5_aws":       true,
	"nist_csf_1.1_aws":                 true,
	"nist_csf_2.0_aws":                 true,
	"pci_3.2.1_aws":                    true,
	"pci_4.0_aws":                      true,
	"prowler_threatscore_aws":          true,
	"rbi_cyber_security_framework_aws": true,
	"secnumcloud_3.2_aws":              true,
	"soc2_aws":                         true,
}

// resolveFramework turns a friendly name or raw Prowler ID into a Prowler ID.
func resolveFramework(name string) (string, error) {
	if id, ok := friendlyFrameworks[strings.ToLower(name)]; ok {
		return id, nil
	}
	if prowlerFrameworks[name] {
		return name, nil
	}
	msg := fmt.Sprintf("unknown compliance framework %q", name)
	if s := suggestFramework(name); s != "" {
		msg += fmt.Sprintf(" — did you mean %q?", s)
	}
	return "", fmt.Errorf("%s (friendly names: %s; or any Prowler AWS compliance ID)", msg, strings.Join(friendlyNames(), ", "))
}

func friendlyNames() []string {
	names := make([]string, 0, len(friendlyFrameworks))
	for k := range friendlyFrameworks {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// suggestFramework returns the closest friendly name or Prowler ID, if any is
// close enough to be a likely typo.
func suggestFramework(name string) string {
	name = strings.ToLower(name)
	best, bestDist := "", 4
	consider := func(candidate string) {
		if d := editDistance(name, candidate); d < bestDist {
			best, bestDist = candidate, d
		}
	}
	for k := range friendlyFrameworks {
		consider(k)
	}
	for k := range prowlerFrameworks {
		consider(k)
	}
	return best
}

func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}

// RetentionTiers are the values the results bucket has lifecycle rules for.
var RetentionTiers = []string{"30d", "90d", "180d", "1y", "2y", "7y"}

var retentionDays = map[string]int32{"30d": 30, "90d": 90, "180d": 180, "1y": 365, "2y": 730, "7y": 2555}

// ValidRetention reports whether r is a supported retention tier.
func ValidRetention(r string) bool {
	_, ok := retentionDays[r]
	return ok
}

// ResolveFrameworks turns friendly names or raw Prowler IDs into Prowler IDs,
// deduplicated in order.
func ResolveFrameworks(names []string) ([]string, error) {
	if len(names) == 0 {
		return nil, fmt.Errorf("at least one framework is required (%s, or any Prowler AWS compliance ID)", strings.Join(friendlyNames(), ", "))
	}
	seen := map[string]bool{}
	var ids []string
	for _, name := range names {
		id, err := resolveFramework(name)
		if err != nil {
			return nil, err
		}
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids, nil
}
