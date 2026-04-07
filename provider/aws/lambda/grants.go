package lambda

import (
	"fmt"

	aws "github.com/DamienPace15/anvil/provider/aws"
	provider "github.com/DamienPace15/anvil/provider/internal/shared"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// GrantInvoke grants lambda:InvokeFunction permission on this Lambda
// to the target compute resource's execution role.
//
// Usage:
//
//	fn.GrantInvoke(ctx, otherLambda, nil)
//	fn.GrantInvoke(ctx, otherLambda, nil, provider.GrantOptions{
//	    Justification: "Order processor triggers notification sender",
//	})
func (l *Lambda) GrantInvoke(ctx *pulumi.Context, target provider.GrantTarget, opts ...provider.GrantOptions) error {
	o := provider.MergeGrantOptions(opts)
	name := fmt.Sprintf("%s-%s-invoke", l.name, target.Name())
	arns := aws.BuildResourceARNs(l.Arn, nil)

	return aws.CreateGrant(ctx, l, name, target, []string{
		"lambda:InvokeFunction",
	}, arns, o)
}
