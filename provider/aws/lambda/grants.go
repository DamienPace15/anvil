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

// GrantEgress adds internet or CIDR-scoped egress rules to this Lambda's
// security group. Opt-in only — a Lambda with no GrantEgress call has zero
// outbound internet access.
//
// Requires the Lambda to be VPC-placed (vpc field set in LambdaArgs).
//
// Usage:
//
//	fn.GrantEgress(ctx, vpcsg.EgressArgs{Internet: true})
//	fn.GrantEgress(ctx, vpcsg.EgressArgs{Internet: true, Ports: []int{80, 443}})
//	fn.GrantEgress(ctx, vpcsg.EgressArgs{CIDRs: []vpcsg.CIDREgressRule{
//	    {Range: "10.0.0.0/8", Ports: []int{5432}},
//	}})
func (l *Lambda) GrantEgress(ctx *pulumi.Context, args vpcsg.EgressArgs) error {
	if l.SecurityGroupID == (pulumi.StringOutput{}) {
		return fmt.Errorf(
			"Lambda %q has no VPC — GrantEgress requires vpc to be set on the Lambda",
			l.name,
		)
	}
	return vpcsg.GrantEgress(ctx, l.name, l.SecurityGroupID, args, l)
}

// GrantEndpointAccess opens the network path between this Lambda and one or
// more Interface VPC Endpoints.
//
// For each endpoint:
//   - Egress rule on the Lambda SG  — port 443 → endpoint SG
//   - Ingress rule on the endpoint SG — port 443 ← Lambda SG
//
// Requires the Lambda to be VPC-placed (vpc field set in LambdaArgs).
//
// Usage:
//
//	fn.GrantEndpointAccess(ctx, []vpcsg.EndpointTarget{
//	    ssmEndpoint,
//	    ssmMessagesEndpoint,
//	    ec2MessagesEndpoint,
//	})
func (l *Lambda) GrantEndpointAccess(ctx *pulumi.Context, endpoints []vpcsg.EndpointTarget) error {
	if l.SecurityGroupID == (pulumi.StringOutput{}) {
		return fmt.Errorf(
			"Lambda %q has no VPC — GrantEndpointAccess requires vpc to be set on the Lambda",
			l.name,
		)
	}
	return vpcsg.GrantEndpointAccess(ctx, l.name, l.SecurityGroupID, endpoints, l)
}
