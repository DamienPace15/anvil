package compliance

// Code generated from the Prowler 5.44.0 image (prowler/compliance/**/*.json). DO NOT EDIT.
// Regenerate when ProwlerImage is bumped.

// FrameworkInfo describes a Prowler compliance framework.
type FrameworkInfo struct {
	// Key is how findings reference the framework in unmapped.compliance
	// ("<Framework>-<Version>", or "<Framework>" when unversioned). Empty for
	// cross-provider frameworks, which findings don't reference.
	Key string
	// Label is a short display name, e.g. "CIS 7.0".
	Label string
	// Name is Prowler's full name.
	Name string
}

// frameworkInfo is keyed by Prowler compliance ID.
var frameworkInfo = map[string]FrameworkInfo{
	"asd_essential_eight_aws":                               {Key: "ASD-Essential-Eight-Nov 2023", Label: "ASD Essential Eight Nov 2023", Name: "ASD Essential Eight Maturity Model - Maturity Level One (AWS)"},
	"aws_account_security_onboarding_aws":                   {Key: "AWS-Account-Security-Onboarding", Label: "AWS Account Security Onboarding", Name: "AWS Account Security Onboarding"},
	"aws_ai_security_framework_aws":                         {Key: "AWS-AI-Security-Framework-1.0", Label: "AWS AI Security Framework 1.0", Name: "AWS AI Security Framework"},
	"aws_audit_manager_control_tower_guardrails_aws":        {Key: "AWS-Audit-Manager-Control-Tower-Guardrails", Label: "AWS Audit Manager Control Tower Guardrails", Name: "AWS Audit Manager Control Tower Guardrails"},
	"aws_foundational_security_best_practices_aws":          {Key: "AWS-Foundational-Security-Best-Practices", Label: "AWS Foundational Security Best Practices", Name: "AWS Foundational Security Best Practices"},
	"aws_foundational_technical_review_aws":                 {Key: "AWS-Foundational-Technical-Review", Label: "AWS Foundational Technical Review", Name: "AWS Foundational Technical Review"},
	"aws_well_architected_framework_reliability_pillar_aws": {Key: "AWS-Well-Architected-Framework-Reliability-Pillar", Label: "AWS Well Architected Framework Reliability Pillar", Name: "AWS Well-Architected Framework Reliability Pillar"},
	"aws_well_architected_framework_security_pillar_aws":    {Key: "AWS-Well-Architected-Framework-Security-Pillar", Label: "AWS Well Architected Framework Security Pillar", Name: "AWS Well-Architected Framework Security Pillar"},
	"c5_aws":                           {Key: "C5-2025", Label: "C5 2025", Name: "Cloud Computing Compliance Criteria Catalogue C5"},
	"ccc_aws":                          {Key: "CCC-v2025.10", Label: "CCC v2025.10", Name: "Common Cloud Controls Catalog (CCC)"},
	"cis_1.4_aws":                      {Key: "CIS-1.4", Label: "CIS 1.4", Name: "CIS Amazon Web Services Foundations Benchmark v1.4.0"},
	"cis_1.5_aws":                      {Key: "CIS-1.5", Label: "CIS 1.5", Name: "CIS Amazon Web Services Foundations Benchmark v1.5.0"},
	"cis_2.0_aws":                      {Key: "CIS-2.0", Label: "CIS 2.0", Name: "CIS Amazon Web Services Foundations Benchmark v2.0.0"},
	"cis_3.0_aws":                      {Key: "CIS-3.0", Label: "CIS 3.0", Name: "CIS Amazon Web Services Foundations Benchmark v3.0.0"},
	"cis_4.0_aws":                      {Key: "CIS-4.0.1", Label: "CIS 4.0.1", Name: "CIS Amazon Web Services Foundations Benchmark v4.0.1"},
	"cis_5.0_aws":                      {Key: "CIS-5.0", Label: "CIS 5.0", Name: "CIS Amazon Web Services Foundations Benchmark v5.0.0"},
	"cis_6.0_aws":                      {Key: "CIS-6.0", Label: "CIS 6.0", Name: "CIS Amazon Web Services Foundations Benchmark v6.0.0"},
	"cis_7.0_aws":                      {Key: "CIS-7.0", Label: "CIS 7.0", Name: "CIS Amazon Web Services Foundations Benchmark v7.0.0"},
	"cis_controls_8.1":                 {Key: "", Label: "CIS Controls 8.1", Name: "CIS Controls v8.1"},
	"cisa_aws":                         {Key: "CISA", Label: "CISA", Name: "CISA Cyber Essentials framework"},
	"cmmc_2.0":                         {Key: "", Label: "CMMC 2.0", Name: "Cybersecurity Maturity Model Certification (CMMC) 2.0"},
	"csa_ccm_4.0":                      {Key: "", Label: "CSA CCM 4.0", Name: "CSA Cloud Controls Matrix (CCM) v4.0.13"},
	"dora_2022_2554":                   {Key: "", Label: "DORA 2022/2554", Name: "Digital Operational Resilience Act (Regulation (EU) 2022/2554)"},
	"ens_rd2022_aws":                   {Key: "ENS-RD2022", Label: "ENS RD2022", Name: "ENS RD 311/2022 - Categor\u00eda Alta"},
	"fedramp_20x_frr_class_c_2026":     {Key: "", Label: "FedRAMP 20x FRR Class C 2026.09.13.02", Name: "FedRAMP 20x Class C Rules (FRR) 2026"},
	"fedramp_20x_ksi_2026":             {Key: "", Label: "FedRAMP 20x KSI 2026.07.14.01", Name: "FedRAMP 20x Key Security Indicators (KSI) 2026"},
	"fedramp_low_revision_4_aws":       {Key: "FedRAMP-Low-Revision-4", Label: "FedRAMP Low Revision 4", Name: "FedRAMP Low Revision 4"},
	"fedramp_moderate_revision_4_aws":  {Key: "FedRamp-Moderate-Revision-4", Label: "FedRamp Moderate Revision 4", Name: "FedRAMP Moderate Revision 4"},
	"ffiec_aws":                        {Key: "FFIEC", Label: "FFIEC", Name: "FFIEC Cybersecurity Assessment Tool framework"},
	"gdpr_aws":                         {Key: "GDPR", Label: "GDPR", Name: "GDPR compliance framework"},
	"gxp_21_cfr_part_11_aws":           {Key: "GxP-21-CFR-Part-11", Label: "GxP 21 CFR Part 11", Name: "GxP (Good Practices) 21 CFR Part 11"},
	"gxp_eu_annex_11_aws":              {Key: "GxP-EU-Annex-11", Label: "GxP EU Annex 11", Name: "GxP (Good Practices) EU Annex 11"},
	"hipaa_aws":                        {Key: "HIPAA", Label: "HIPAA", Name: "HIPAA compliance framework"},
	"iso27001_2013_aws":                {Key: "ISO27001-2013", Label: "ISO27001 2013", Name: "ISO/IEC 27001 Information Security Management Standard 2013"},
	"iso27001_2022_aws":                {Key: "ISO27001-2022", Label: "ISO27001 2022", Name: "ISO/IEC 27001 Information Security Management Standard 2022"},
	"kisa_isms_p_2023_aws":             {Key: "KISA-ISMS-P-2023", Label: "KISA ISMS P 2023", Name: "KISA ISMS compliance framework 2023"},
	"kisa_isms_p_2023_korean_aws":      {Key: "KISA-ISMS-P-2023-korean", Label: "KISA ISMS P 2023-korean", Name: "KISA ISMS compliance framework 2023 (Korean)"},
	"mitre_attack_aws":                 {Key: "MITRE-ATTACK", Label: "MITRE ATTACK", Name: "MITRE ATT&CK compliance framework"},
	"nis2_aws":                         {Key: "NIS2", Label: "NIS2", Name: "NIS2 - Network and Information Security Directive (Directive (EU) 2022/2555)"},
	"nist_800_171_revision_2_aws":      {Key: "NIST-800-171-Revision-2", Label: "NIST 800 171 Revision 2", Name: "National Institute of Standards and Technology (NIST) 800-171 Revision 2"},
	"nist_800_53_revision_4_aws":       {Key: "NIST-800-53-Revision-4", Label: "NIST 800 53 Revision 4", Name: "National Institute of Standards and Technology (NIST) 800-53 Revision 4"},
	"nist_800_53_revision_5_aws":       {Key: "NIST-800-53-Revision-5", Label: "NIST 800 53 Revision 5", Name: "National Institute of Standards and Technology (NIST) 800-53 Revision 5"},
	"nist_csf_1.1_aws":                 {Key: "NIST-CSF-1.1", Label: "NIST CSF 1.1", Name: "National Institute of Standards and Technology (NIST) Cybersecurity Framework (CSF) v1.1"},
	"nist_csf_2.0_aws":                 {Key: "NIST-CSF-2.0", Label: "NIST CSF 2.0", Name: "National Institute of Standards and Technology (NIST) Cybersecurity Framework (CSF) v2.0"},
	"pci_3.2.1_aws":                    {Key: "PCI-3.2.1", Label: "PCI 3.2.1", Name: "Payment Card Industry Data Security Standard (PCI DSS) v3.2.1"},
	"pci_4.0_aws":                      {Key: "PCI-4.0", Label: "PCI 4.0", Name: "Payment Card Industry Data Security Standard (PCI DSS) v4.0"},
	"prowler_threatscore_aws":          {Key: "ProwlerThreatScore-1.0", Label: "ProwlerThreatScore 1.0", Name: "Prowler ThreatScore Compliance Framework for AWS"},
	"rbi_cyber_security_framework_aws": {Key: "RBI-Cyber-Security-Framework", Label: "RBI Cyber Security Framework", Name: "Reserve Bank of India (RBI) Cyber Security Framework"},
	"secnumcloud_3.2_aws":              {Key: "SecNumCloud-3.2", Label: "SecNumCloud 3.2", Name: "SecNumCloud Referentiel d'Exigences v3.2"},
	"soc2_aws":                         {Key: "SOC2", Label: "SOC2", Name: "System and Organization Controls 2 (SOC2)"},
}

// Framework returns display metadata for a Prowler compliance ID.
func Framework(id string) FrameworkInfo {
	if info, ok := frameworkInfo[id]; ok {
		return info
	}
	return FrameworkInfo{Label: id, Name: id}
}
