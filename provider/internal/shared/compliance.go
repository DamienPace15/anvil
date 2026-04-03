package provider

import (
	"strings"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	c "github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

// ── ComplianceFramework ────────────────────────────────────
//
// Represents a supported compliance standard. Adding a value here
// will automatically surface it as an enum in the generated SDKs
// via the Pulumi schema.

type ComplianceFramework string

const (
	ComplianceSOC2     ComplianceFramework = "soc2"
	ComplianceISO27001 ComplianceFramework = "iso27001"
	ComplianceCIS      ComplianceFramework = "cis"
	CompliancePCIDSS   ComplianceFramework = "pci-dss"
	ComplianceHIPAA    ComplianceFramework = "hipaa"
	ComplianceFedRAMP  ComplianceFramework = "fedramp"
	ComplianceHITRUST  ComplianceFramework = "hitrust"
	ComplianceGDPR     ComplianceFramework = "gdpr"
	ComplianceSOC1     ComplianceFramework = "soc1"
	ComplianceIRAP     ComplianceFramework = "irap"
	ComplianceNISTCSF  ComplianceFramework = "nist-csf"
	ComplianceCSASTAR  ComplianceFramework = "csa-star"
)

// ── Per-resource allowed sets ──────────────────────────────
//
// Not every framework is enforceable at the resource level.
// CSA STAR and HITRUST are organisational certifications — they
// don't map to discrete resource controls.

// StorageFrameworks are the compliance frameworks applicable to
// storage resources (S3 buckets, GCP buckets, etc).
var StorageFrameworks = map[ComplianceFramework]bool{
	ComplianceSOC2:     true,
	ComplianceISO27001: true,
	ComplianceCIS:      true,
	CompliancePCIDSS:   true,
	ComplianceHIPAA:    true,
	ComplianceFedRAMP:  true,
	ComplianceGDPR:     true,
	ComplianceSOC1:     true,
	ComplianceIRAP:     true,
	ComplianceNISTCSF:  true,
}

// ComputeFrameworks are the compliance frameworks applicable to
// compute resources (Lambda, GCP Functions, etc).
var ComputeFrameworks = map[ComplianceFramework]bool{
	ComplianceSOC2:     true,
	ComplianceISO27001: true,
	ComplianceCIS:      true,
	CompliancePCIDSS:   true,
	ComplianceHIPAA:    true,
	ComplianceFedRAMP:  true,
	ComplianceGDPR:     true,
	ComplianceNISTCSF:  true,
}

// SiteFrameworks are the compliance frameworks applicable to
// frontend site deployments (SvelteKit, etc).
var SiteFrameworks = map[ComplianceFramework]bool{
	ComplianceSOC2:     true,
	ComplianceISO27001: true,
	ComplianceCIS:      true,
	CompliancePCIDSS:   true,
	ComplianceGDPR:     true,
	ComplianceNISTCSF:  true,
}

// ── GetAppCompliance ───────────────────────────────────────
//
// Reads the app-level compliance frameworks from Pulumi config.
// Set by the SDK's App class during initialisation as "anvil:compliance".
// Returns nil if not set.

func GetAppCompliance(ctx *pulumi.Context) []ComplianceFramework {
	cfg := c.New(ctx, "anvil")
	raw := cfg.Get("compliance")
	if raw == "" {
		return nil
	}

	parts := strings.Split(raw, ",")
	frameworks := make([]ComplianceFramework, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			frameworks = append(frameworks, ComplianceFramework(p))
		}
	}
	return frameworks
}

// ── ResolveCompliance ──────────────────────────────────────
//
// Merges app-level defaults with component-level frameworks.
// Component frameworks extend app defaults — they never replace them.
// Duplicates are removed. Order is: app defaults first, then component additions.
//
// Example:
//
//	app defaults: ["soc2", "iso27001"]
//	component:    ["hipaa"]
//	resolved:     ["soc2", "iso27001", "hipaa"]

func ResolveCompliance(appDefaults []ComplianceFramework, component []ComplianceFramework) []ComplianceFramework {
	seen := make(map[ComplianceFramework]bool)
	result := make([]ComplianceFramework, 0, len(appDefaults)+len(component))

	for _, f := range append(appDefaults, component...) {
		if !seen[f] {
			seen[f] = true
			result = append(result, f)
		}
	}
	return result
}

// ── FilterFrameworks ──────────────────────────────────────
//
// Filters a resolved compliance list down to only the frameworks
// that are applicable to a given resource type. Silently drops
// frameworks that don't apply — no error, since they're inherited
// from app defaults and the user didn't explicitly set them per-resource.

func FilterFrameworks(resolved []ComplianceFramework, allowed map[ComplianceFramework]bool) []ComplianceFramework {
	result := make([]ComplianceFramework, 0, len(resolved))
	for _, f := range resolved {
		if allowed[f] {
			result = append(result, f)
		}
	}
	return result
}

// ── HasFramework ──────────────────────────────────────────
//
// Convenience helper for checking a single framework in a resolved list.
//
//	if provider.HasFramework(compliance, provider.ComplianceHIPAA) { ... }

func HasFramework(resolved []ComplianceFramework, target ComplianceFramework) bool {
	for _, f := range resolved {
		if f == target {
			return true
		}
	}
	return false
}
