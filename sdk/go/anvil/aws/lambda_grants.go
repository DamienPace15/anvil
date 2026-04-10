// sdk/go/anvil/aws/lambda_grants.go
// Hand-written. Not generated — do not delete.
//
// Wraps NewLambda to capture the logical name, then adds grant methods.
// The generated lambda.go is regenerated on every gen-sdk run — this file
// survives regeneration because it is hand-written.
//
// Adding a new grant method to Lambda:
//   1. Add the method here using lambdaName(l) for rule naming
//   2. Done — no other files change for Go SDK

package aws

import (
	"fmt"
	"strings"

	awsvpc "github.com/pulumi/pulumi-aws/sdk/v7/go/aws/vpc"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// lambdaNames stores the logical name for each Lambda created via
// NewLambdaWithGrants. Used by grant methods for SG rule naming.
var lambdaNames = map[*Lambda]string{}

// NewLambdaWithGrants wraps the generated NewLambda and stores the logical
// name for use in grant methods.
//
// Use this instead of NewLambda when you plan to call grant methods.
// NewLambda still works if you don't need grants.
//
// Usage:
//
//	fn, err := aws.NewLambdaWithGrants(ctx, "my-lambda", &aws.LambdaArgs{...})
//	fn.GrantEgress(ctx, aws.EgressArgs{Internet: true})
//	fn.GrantVpcEndpointAccess(ctx, []aws.EndpointTarget{
//	    aws.NewVpcEndpointTarget("ssm", ep),
//	})
func NewLambdaWithGrants(ctx *pulumi.Context, name string, args *LambdaArgs, opts ...pulumi.ResourceOption) (*Lambda, error) {
	l, err := NewLambda(ctx, name, args, opts...)
	if err != nil {
		return nil, err
	}
	lambdaNames[l] = name
	return l, nil
}

// lambdaName returns the stored logical name for this Lambda, falling back
// to "lambda" if NewLambdaWithGrants was not used.
func lambdaName(l *Lambda) string {
	if name, ok := lambdaNames[l]; ok {
		return name
	}
	return "lambda"
}

// CIDREgressRule defines a single CIDR-scoped egress rule.
// One SecurityGroupEgressRule is created per port.
type CIDREgressRule struct {
	// Range is the IPv4 CIDR block, e.g. "10.0.0.0/8"
	Range string
	// Ports are the TCP ports to allow to this CIDR. Required.
	Ports []int
}

// EgressArgs defines the egress configuration for GrantEgress.
// Must specify either Internet or CIDRs — not both, not neither.
type EgressArgs struct {
	// Internet allows outbound to 0.0.0.0/0.
	// Defaults to port 443 if Ports is omitted.
	Internet bool

	// Ports are the TCP ports to allow when Internet is true.
	// Defaults to [443] if omitted.
	Ports []int

	// AllPorts allows all outbound TCP ports (0-65535) when Internet is true.
	// Deliberate unrestricted egress — visible in code review.
	AllPorts bool

	// CIDRs defines structured CIDR-scoped egress rules.
	// One rule per { Range, Port } pair. Ports required per entry.
	CIDRs []CIDREgressRule
}

// GrantEgress adds internet or CIDR-scoped egress rules to this Lambda's
// security group. Opt-in only — a VPC-attached Lambda with no GrantEgress
// call has zero outbound internet access.
//
// Requires the Lambda to be created via NewLambdaWithGrants and VPC-placed.
//
// Usage:
//
//	fn.GrantEgress(ctx, aws.EgressArgs{Internet: true})
//	fn.GrantEgress(ctx, aws.EgressArgs{Internet: true, Ports: []int{80, 443}})
//	fn.GrantEgress(ctx, aws.EgressArgs{CIDRs: []aws.CIDREgressRule{
//	    {Range: "10.0.0.0/8", Ports: []int{5432}},
//	}})
func (l *Lambda) GrantEgress(ctx *pulumi.Context, args EgressArgs) error {
	name := lambdaName(l)

	if args.Internet && len(args.CIDRs) > 0 {
		return fmt.Errorf("Lambda.GrantEgress: cannot specify both internet and cidrs — choose one")
	}
	if !args.Internet && len(args.CIDRs) == 0 {
		return fmt.Errorf("Lambda.GrantEgress: requires either internet: true or cidrs")
	}
	if args.AllPorts && !args.Internet {
		return fmt.Errorf("Lambda.GrantEgress: allPorts is only valid with internet: true")
	}

	sgId := l.SecurityGroupId.ApplyT(func(id *string) (string, error) {
		if id == nil || *id == "" {
			return "", fmt.Errorf("Lambda %q has no VPC — vpc must be set to use GrantEgress", name)
		}
		return *id, nil
	}).(pulumi.StringOutput)

	// ── Internet egress ───────────────────────────────────────────────────────
	if args.Internet {
		if args.AllPorts {
			_, err := awsvpc.NewSecurityGroupEgressRule(ctx,
				name+"-egress-all",
				&awsvpc.SecurityGroupEgressRuleArgs{
					SecurityGroupId: sgId,
					CidrIpv4:        pulumi.String("0.0.0.0/0"),
					FromPort:        pulumi.Int(0),
					ToPort:          pulumi.Int(65535),
					IpProtocol:      pulumi.String("tcp"),
					Tags:            pulumi.StringMap{"ManagedBy": pulumi.String("anvil")},
				}, pulumi.Parent(l))
			return err
		}

		ports := args.Ports
		if len(ports) == 0 {
			ports = []int{443}
		}

		for _, port := range ports {
			if _, err := awsvpc.NewSecurityGroupEgressRule(ctx,
				fmt.Sprintf("%s-egress-%d", name, port),
				&awsvpc.SecurityGroupEgressRuleArgs{
					SecurityGroupId: sgId,
					CidrIpv4:        pulumi.String("0.0.0.0/0"),
					FromPort:        pulumi.Int(port),
					ToPort:          pulumi.Int(port),
					IpProtocol:      pulumi.String("tcp"),
					Tags:            pulumi.StringMap{"ManagedBy": pulumi.String("anvil")},
				}, pulumi.Parent(l)); err != nil {
				return err
			}
		}
		return nil
	}

	// ── CIDR egress ───────────────────────────────────────────────────────────
	for _, cidrRule := range args.CIDRs {
		if len(cidrRule.Ports) == 0 {
			return fmt.Errorf(
				"Lambda.GrantEgress: cidr %q requires ports — be explicit about which ports this range needs",
				cidrRule.Range,
			)
		}
		for _, port := range cidrRule.Ports {
			if _, err := awsvpc.NewSecurityGroupEgressRule(ctx,
				fmt.Sprintf("%s-egress-%s-%d", name, sanitizeCIDR(cidrRule.Range), port),
				&awsvpc.SecurityGroupEgressRuleArgs{
					SecurityGroupId: sgId,
					CidrIpv4:        pulumi.String(cidrRule.Range),
					FromPort:        pulumi.Int(port),
					ToPort:          pulumi.Int(port),
					IpProtocol:      pulumi.String("tcp"),
					Tags:            pulumi.StringMap{"ManagedBy": pulumi.String("anvil")},
				}, pulumi.Parent(l)); err != nil {
				return err
			}
		}
	}

	return nil
}

// GrantVpcEndpointAccess opens the network path between this Lambda and one
// or more Interface VPC Endpoints.
//
// For each endpoint:
//   - Egress rule on the Lambda SG  — port 443 → endpoint SG
//   - Ingress rule on the endpoint SG — port 443 ← Lambda SG
//
// Requires the Lambda to be created via NewLambdaWithGrants and VPC-placed.
//
// Usage:
//
//	fn.GrantVpcEndpointAccess(ctx, []aws.EndpointTarget{
//	    aws.NewVpcEndpointTarget("ssm", ssmEndpoint),
//	    aws.NewVpcEndpointTarget("secrets", secretsEndpoint),
//	})
func (l *Lambda) GrantVpcEndpointAccess(ctx *pulumi.Context, endpoints []EndpointTarget) error {
	name := lambdaName(l)

	sgId := l.SecurityGroupId.ApplyT(func(id *string) (string, error) {
		if id == nil || *id == "" {
			return "", fmt.Errorf("Lambda %q has no VPC — vpc must be set to use GrantVpcEndpointAccess", name)
		}
		return *id, nil
	}).(pulumi.StringOutput)

	for _, endpoint := range endpoints {
		epName := endpoint.EndpointName()

		// 1. Egress rule on the Lambda SG — port 443 out to the endpoint SG.
		if _, err := awsvpc.NewSecurityGroupEgressRule(ctx,
			fmt.Sprintf("%s-%s-endpoint-egress", name, epName),
			&awsvpc.SecurityGroupEgressRuleArgs{
				SecurityGroupId:           sgId,
				ReferencedSecurityGroupId: endpoint.EndpointSecurityGroupId(),
				FromPort:                  pulumi.Int(443),
				ToPort:                    pulumi.Int(443),
				IpProtocol:                pulumi.String("tcp"),
				Tags:                      pulumi.StringMap{"ManagedBy": pulumi.String("anvil")},
			}, pulumi.Parent(l)); err != nil {
			return fmt.Errorf("GrantVpcEndpointAccess: egress rule to %s: %w", epName, err)
		}

		// 2. Ingress rule on the endpoint SG — port 443 in from the Lambda SG.
		if _, err := awsvpc.NewSecurityGroupIngressRule(ctx,
			fmt.Sprintf("%s-%s-endpoint-ingress", epName, name),
			&awsvpc.SecurityGroupIngressRuleArgs{
				SecurityGroupId:           endpoint.EndpointSecurityGroupId(),
				ReferencedSecurityGroupId: sgId,
				FromPort:                  pulumi.Int(443),
				ToPort:                    pulumi.Int(443),
				IpProtocol:                pulumi.String("tcp"),
				Tags:                      pulumi.StringMap{"ManagedBy": pulumi.String("anvil")},
			}, pulumi.Parent(l)); err != nil {
			return fmt.Errorf("GrantVpcEndpointAccess: ingress rule on %s: %w", epName, err)
		}
	}

	return nil
}

// sanitizeCIDR converts a CIDR like "10.0.0.0/8" to "10-0-0-0-8"
// for use in resource names.
func sanitizeCIDR(cidr string) string {
	return strings.Map(func(r rune) rune {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') {
			return r
		}
		return '-'
	}, cidr)
}
