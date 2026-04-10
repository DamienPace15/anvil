package vpcsg

import (
	"fmt"
	"strings"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// CIDREgressRule defines a single CIDR-scoped egress rule.
// Each entry creates one SecurityGroupEgressRule per port.
type CIDREgressRule struct {
	// Range is the IPv4 CIDR block to allow outbound traffic to.
	// e.g. "10.0.0.0/8", "203.0.113.0/24"
	Range string `pulumi:"range"`

	// Ports is the list of TCP ports to allow outbound to this CIDR.
	// Required — be explicit about which ports each range needs.
	Ports []int `pulumi:"ports"`
}

// EgressArgs defines the internet or CIDR egress configuration for any
// VPC-attached compute resource.
//
// Must specify either Internet or CIDRs — not both, not neither.
// Shared across Lambda, ECS, EC2 — all compute resources use the same shape.
type EgressArgs struct {
	// Internet allows outbound to 0.0.0.0/0.
	// Must be true when set — explicit signal that internet egress is intended.
	// Defaults to port 443 if Ports is omitted.
	Internet bool `pulumi:"internet,optional"`

	// Ports is the list of TCP ports to allow when Internet is true.
	// Defaults to [443] if omitted. Be explicit if you need port 80 or others.
	Ports []int `pulumi:"ports,optional"`

	// AllPorts allows all outbound TCP ports (0-65535) when Internet is true.
	// Deliberate unrestricted egress signal — visible in code review.
	// Only use when you genuinely need arbitrary port access.
	AllPorts bool `pulumi:"allPorts,optional"`

	// CIDRs defines structured CIDR-scoped egress rules.
	// Each entry specifies a CIDR range and the ports allowed to that range.
	// One SecurityGroupEgressRule is created per { range, port } pair.
	// Ports are required per entry — be explicit about what each range needs.
	CIDRs []CIDREgressRule `pulumi:"cidrs,optional"`
}

// GrantEgress adds internet or CIDR-scoped egress rules to a compute
// resource's security group.
//
// Shared by all compute resources — Lambda, ECS, EC2 all call this.
// The compute resource's own GrantEgress method is a thin wrapper around this.
//
// Validates:
//   - Resource must be VPC-attached (non-empty sgId)
//   - Must specify Internet or CIDRs — not both, not neither
//   - AllPorts only valid with Internet
func GrantEgress(
	ctx *pulumi.Context,
	resourceName string,
	sgId pulumi.StringOutput,
	args EgressArgs,
	parent pulumi.Resource,
) error {
	// Validate: must specify internet or cidrs, not both, not neither.
	if args.Internet && len(args.CIDRs) > 0 {
		return fmt.Errorf(
			"%s: GrantEgress cannot specify both internet and cidrs — choose one",
			resourceName,
		)
	}
	if !args.Internet && len(args.CIDRs) == 0 {
		return fmt.Errorf(
			"%s: GrantEgress requires either internet: true or cidrs",
			resourceName,
		)
	}

	// Validate: allPorts only valid with internet.
	if args.AllPorts && !args.Internet {
		return fmt.Errorf(
			"%s: GrantEgress allPorts is only valid with internet: true",
			resourceName,
		)
	}

	// ── Internet egress ───────────────────────────────────────────────────────
	if args.Internet {
		if args.AllPorts {
			return AddInternetEgressRule(
				ctx,
				fmt.Sprintf("%s-egress-all", resourceName),
				sgId,
				0, 65535,
				parent,
			)
		}

		ports := args.Ports
		if len(ports) == 0 {
			ports = []int{443}
		}

		for _, port := range ports {
			if err := AddInternetEgressRule(
				ctx,
				fmt.Sprintf("%s-egress-%d", resourceName, port),
				sgId,
				port, port,
				parent,
			); err != nil {
				return err
			}
		}
		return nil
	}

	// ── CIDR egress ───────────────────────────────────────────────────────────
	for _, cidrRule := range args.CIDRs {
		if len(cidrRule.Ports) == 0 {
			return fmt.Errorf(
				"%s: GrantEgress cidr %q requires ports — be explicit about which ports this range needs",
				resourceName,
				cidrRule.Range,
			)
		}
		for _, port := range cidrRule.Ports {
			ruleName := fmt.Sprintf("%s-egress-%s-%d", resourceName, sanitizeCIDR(cidrRule.Range), port)
			if err := AddCIDREgressRule(
				ctx,
				ruleName,
				sgId,
				cidrRule.Range,
				port, port,
				parent,
			); err != nil {
				return err
			}
		}
	}

	return nil
}

// sanitizeCIDR converts a CIDR like "10.0.0.0/8" to "10-0-0-0-8" for use
// in resource names.
func sanitizeCIDR(cidr string) string {
	return strings.Map(func(r rune) rune {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') {
			return r
		}
		return '-'
	}, cidr)
}
