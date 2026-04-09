package lambda

import (
	"fmt"

	aws "github.com/DamienPace15/anvil/provider/aws"
	provider "github.com/DamienPace15/anvil/provider/internal/shared"
	"github.com/DamienPace15/anvil/provider/internal/vpcsg"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// GrantInvoke grants lambda:InvokeFunction permission on this Lambda
// to the target compute resource's execution role.
func (l *Lambda) GrantInvoke(ctx *pulumi.Context, target provider.GrantTarget, opts ...provider.GrantOptions) error {
	o := provider.MergeGrantOptions(opts)
	name := fmt.Sprintf("%s-%s-invoke", l.name, target.Name())
	arns := aws.BuildResourceARNs(l.Arn, nil)

	return aws.CreateGrant(ctx, l, name, target, []string{
		"lambda:InvokeFunction",
	}, arns, o)
}

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

// EgressArgs defines the internet egress configuration for a VPC-attached
// compute resource. Every Lambda with internet access must call GrantEgress —
// this makes outbound access explicit and findable by searching the codebase.
//
// Must specify either Internet or CIDRs — not both, not neither.
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

// GrantEgress adds internet or CIDR-scoped egress rules to this Lambda's
// security group. Opt-in only — a Lambda with no GrantEgress call has zero
// outbound internet access.
//
// Must specify either Internet or CIDRs — not both, not neither.
//
// Usage:
//
//	fn.GrantEgress(ctx, EgressArgs{Internet: true})                         // 443 only (default)
//	fn.GrantEgress(ctx, EgressArgs{Internet: true, Ports: []int{80, 443}})  // HTTP + HTTPS
//	fn.GrantEgress(ctx, EgressArgs{Internet: true, AllPorts: true})         // unrestricted
//	fn.GrantEgress(ctx, EgressArgs{CIDRs: []CIDREgressRule{
//	    {Range: "10.0.0.0/8", Ports: []int{5432}},
//	    {Range: "172.16.0.0/12", Ports: []int{6379}},
//	}})
func (l *Lambda) GrantEgress(ctx *pulumi.Context, args EgressArgs) error {
	// Validate: Lambda must be VPC-attached.
	if l.SecurityGroupID == (pulumi.StringOutput{}) {
		return fmt.Errorf(
			"Lambda %q has no VPC — GrantEgress requires vpc to be set on the Lambda",
			l.name,
		)
	}

	// Validate: must specify internet or cidrs, not both, not neither.
	if args.Internet && len(args.CIDRs) > 0 {
		return fmt.Errorf(
			"Lambda %q: GrantEgress cannot specify both internet and cidrs — choose one",
			l.name,
		)
	}
	if !args.Internet && len(args.CIDRs) == 0 {
		return fmt.Errorf(
			"Lambda %q: GrantEgress requires either internet: true or cidrs",
			l.name,
		)
	}

	// Validate: allPorts only valid with internet.
	if args.AllPorts && !args.Internet {
		return fmt.Errorf(
			"Lambda %q: GrantEgress allPorts is only valid with internet: true",
			l.name,
		)
	}

	// ── Internet egress ───────────────────────────────────────────────────────
	if args.Internet {
		if args.AllPorts {
			return vpcsg.AddInternetEgressRule(
				ctx,
				fmt.Sprintf("%s-egress-all", l.name),
				l.SecurityGroupID,
				0, 65535,
				l,
			)
		}

		ports := args.Ports
		if len(ports) == 0 {
			ports = []int{443}
		}

		for _, port := range ports {
			if err := vpcsg.AddInternetEgressRule(
				ctx,
				fmt.Sprintf("%s-egress-%d", l.name, port),
				l.SecurityGroupID,
				port, port,
				l,
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
				"Lambda %q: GrantEgress cidr %q requires ports — be explicit about which ports this range needs",
				l.name,
				cidrRule.Range,
			)
		}
		for _, port := range cidrRule.Ports {
			ruleName := fmt.Sprintf("%s-egress-%s-%d", l.name, sanitizeCIDR(cidrRule.Range), port)
			if err := vpcsg.AddCIDREgressRule(
				ctx,
				ruleName,
				l.SecurityGroupID,
				cidrRule.Range,
				port, port,
				l,
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
	result := make([]byte, len(cidr))
	for i := 0; i < len(cidr); i++ {
		c := cidr[i]
		if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') {
			result[i] = c
		} else {
			result[i] = '-'
		}
	}
	return string(result)
}
