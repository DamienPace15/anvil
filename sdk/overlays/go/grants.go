// sdk/go/anvil/grants.go
// Hand-written. Backed up/restored during gen-sdk like app.go and block.go.
//
// Provides the runtime grant execution for all resource grant methods.

package anvil

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/iam"
	awsvpc "github.com/pulumi/pulumi-aws/sdk/v7/go/aws/vpc"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// GrantTarget is implemented by any Anvil compute resource that can receive
// IAM permissions from infra resources via grant methods.
type GrantTarget interface {
	GrantName() string
	GrantRoleArn() pulumi.StringOutput
}

// GrantOptions provides optional metadata for grant methods.
type GrantOptions struct {
	Justification string
}

// CIDREgressRule defines a single CIDR-scoped egress rule.
// One SecurityGroupEgressRule is created per port.
type CIDREgressRule struct {
	// Range is the IPv4 CIDR block, e.g. "10.0.0.0/8"
	Range string

	// Ports are the TCP ports allowed to this CIDR. Required.
	Ports []int
}

// EgressArgs defines the internet egress configuration for a VPC-attached
// compute resource.
//
// Must specify either Internet or CIDRs — not both, not neither.
type EgressArgs struct {
	// Internet allows outbound to 0.0.0.0/0.
	// Defaults to port 443 if Ports is omitted.
	Internet bool

	// Ports is the list of TCP ports when Internet is true.
	// Defaults to [443] if omitted.
	Ports []int

	// AllPorts allows all outbound TCP ports (0-65535) when Internet is true.
	// Deliberate unrestricted egress — visible in code review.
	AllPorts bool

	// CIDRs defines structured CIDR-scoped egress rules.
	// One rule per { Range, port } pair. Ports required per entry.
	CIDRs []CIDREgressRule
}

// CreateGrant creates a scoped IAM RolePolicy granting the specified actions
// on the specified resource ARNs to the target's execution role.
func CreateGrant(
	ctx *pulumi.Context,
	parent pulumi.Resource,
	name string,
	target GrantTarget,
	actions []string,
	resourceArns []pulumi.StringInput,
	opts *GrantOptions,
) error {
	policyJSON := pulumi.All(toOutputs(resourceArns)...).ApplyT(func(args []interface{}) (string, error) {
		resources := make([]string, len(args))
		for i, a := range args {
			resources[i] = a.(string)
		}
		doc := map[string]interface{}{
			"Version": "2012-10-17",
			"Statement": []map[string]interface{}{{
				"Effect":   "Allow",
				"Action":   actions,
				"Resource": resources,
			}},
		}
		b, err := json.Marshal(doc)
		if err != nil {
			return "", fmt.Errorf("failed to marshal IAM policy: %w", err)
		}
		return string(b), nil
	}).(pulumi.StringOutput)

	roleName := target.GrantRoleArn().ApplyT(func(arn string) string {
		idx := strings.LastIndex(arn, "/")
		if idx >= 0 {
			return arn[idx+1:]
		}
		return arn
	}).(pulumi.StringOutput)

	policyName := name
	if opts != nil && opts.Justification != "" {
		policyName = fmt.Sprintf("%s-%s", name, sanitize(opts.Justification))
	}

	_, err := iam.NewRolePolicy(ctx, policyName, &iam.RolePolicyArgs{
		Role:   roleName,
		Policy: policyJSON,
	}, pulumi.Parent(parent))

	return err
}

// CreateEgressGrant creates internet or CIDR-scoped egress SecurityGroupEgressRule(s)
// on a compute resource's dedicated security group.
//
// Must specify either Internet or CIDRs — not both, not neither.
func CreateEgressGrant(
	ctx *pulumi.Context,
	parent pulumi.Resource,
	name string,
	securityGroupId pulumi.StringOutput,
	args EgressArgs,
) error {
	// Validate: internet or cidrs, not both, not neither.
	if args.Internet && len(args.CIDRs) > 0 {
		return fmt.Errorf(
			"%s: CreateEgressGrant cannot specify both Internet and CIDRs — choose one",
			name,
		)
	}
	if !args.Internet && len(args.CIDRs) == 0 {
		return fmt.Errorf(
			"%s: CreateEgressGrant requires either Internet: true or CIDRs",
			name,
		)
	}

	// Validate: AllPorts only with Internet.
	if args.AllPorts && !args.Internet {
		return fmt.Errorf(
			"%s: CreateEgressGrant AllPorts is only valid with Internet: true",
			name,
		)
	}

	// ── Internet egress ───────────────────────────────────────────────────────
	if args.Internet {
		if args.AllPorts {
			_, err := awsvpc.NewSecurityGroupEgressRule(ctx, name+"-egress-all", &awsvpc.SecurityGroupEgressRuleArgs{
				SecurityGroupId: securityGroupId,
				CidrIpv4:        pulumi.String("0.0.0.0/0"),
				FromPort:        pulumi.Int(0),
				ToPort:          pulumi.Int(65535),
				IpProtocol:      pulumi.String("tcp"),
				Tags:            pulumi.StringMap{"ManagedBy": pulumi.String("anvil")},
			}, pulumi.Parent(parent))
			return err
		}

		ports := args.Ports
		if len(ports) == 0 {
			ports = []int{443}
		}

		for _, port := range ports {
			_, err := awsvpc.NewSecurityGroupEgressRule(ctx,
				fmt.Sprintf("%s-egress-%d", name, port),
				&awsvpc.SecurityGroupEgressRuleArgs{
					SecurityGroupId: securityGroupId,
					CidrIpv4:        pulumi.String("0.0.0.0/0"),
					FromPort:        pulumi.Int(port),
					ToPort:          pulumi.Int(port),
					IpProtocol:      pulumi.String("tcp"),
					Tags:            pulumi.StringMap{"ManagedBy": pulumi.String("anvil")},
				}, pulumi.Parent(parent))
			if err != nil {
				return fmt.Errorf("failed to create internet egress rule for port %d: %w", port, err)
			}
		}
		return nil
	}

	// ── CIDR egress ───────────────────────────────────────────────────────────
	for _, cidrRule := range args.CIDRs {
		if len(cidrRule.Ports) == 0 {
			return fmt.Errorf(
				"%s: CreateEgressGrant cidr %q requires Ports — be explicit about which ports this range needs",
				name, cidrRule.Range,
			)
		}
		for _, port := range cidrRule.Ports {
			ruleName := fmt.Sprintf("%s-egress-%s-%d", name, sanitizeCIDR(cidrRule.Range), port)
			_, err := awsvpc.NewSecurityGroupEgressRule(ctx, ruleName, &awsvpc.SecurityGroupEgressRuleArgs{
				SecurityGroupId: securityGroupId,
				CidrIpv4:        pulumi.String(cidrRule.Range),
				FromPort:        pulumi.Int(port),
				ToPort:          pulumi.Int(port),
				IpProtocol:      pulumi.String("tcp"),
				Tags:            pulumi.StringMap{"ManagedBy": pulumi.String("anvil")},
			}, pulumi.Parent(parent))
			if err != nil {
				return fmt.Errorf("failed to create CIDR egress rule %s port %d: %w", cidrRule.Range, port, err)
			}
		}
	}

	return nil
}

// sanitize cleans a string for use in resource names.
func sanitize(s string) string {
	s = strings.ToLower(s)
	result := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			result = append(result, c)
		} else {
			result = append(result, '-')
		}
	}
	if len(result) > 40 {
		result = result[:40]
	}
	return string(result)
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

// BuildResourceArns constructs the list of ARNs for a grant.
func BuildResourceArns(baseArn pulumi.StringOutput, paths []string) []pulumi.StringInput {
	arns := []pulumi.StringInput{baseArn}

	if len(paths) == 0 {
		arns = append(arns, pulumi.Sprintf("%s/*", baseArn))
	} else {
		for _, p := range paths {
			arns = append(arns, pulumi.Sprintf("%s/%s", baseArn, p))
		}
	}

	return arns
}

func toOutputs(inputs []pulumi.StringInput) []interface{} {
	outputs := make([]interface{}, len(inputs))
	for i, input := range inputs {
		outputs[i] = input.ToStringOutput()
	}
	return outputs
}
