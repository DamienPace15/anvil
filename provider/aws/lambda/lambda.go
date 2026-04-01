package lambda

import (
	provider "github.com/DamienPace15/anvil/provider/internal/shared"
	"github.com/DamienPace15/anvil/provider/internal/transform"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/iam"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/lambda"
	p "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi-go-provider/infer"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	c "github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

// Compile-time check: Lambda must implement provider.GrantTarget
var _ provider.GrantTarget = (*Lambda)(nil)

type LambdaArgs struct {
	Name      string                            `pulumi:"name"`
	Runtime   string                            `pulumi:"runtime"`
	Handler   string                            `pulumi:"handler"`
	Vpc       string                            `pulumi:"vpc,optional"`
	Transform map[string]map[string]interface{} `pulumi:"transform,optional"`
}

type Lambda struct {
	pulumi.ResourceState

	Arn          pulumi.StringOutput `pulumi:"arn"`
	FunctionName pulumi.StringOutput `pulumi:"functionName"`
	name         string              // logical resource name — exposed via GrantTarget interface
	roleArn      pulumi.StringOutput // unexported — exposed via GrantTarget interface
}

// Name implements provider.GrantTarget.
// Returns the logical resource name for deterministic IAM policy naming.
func (l *Lambda) Name() string {
	return l.name
}

// RoleARN implements provider.GrantTarget.
// Returns the ARN of the Lambda's IAM execution role for grant methods to attach policies to.
func (l *Lambda) RoleARN() pulumi.StringOutput {
	return l.roleArn
}

func (l *Lambda) Annotate(a infer.Annotator) {
	a.Describe(&l, "An Anvil-managed Lambda function.")
}

func NewLambda(ctx *pulumi.Context, name string, args LambdaArgs, opts ...pulumi.ResourceOption) (*Lambda, error) {
	l := &Lambda{name: name}

	cfg := c.New(ctx, "anvil")
	stage := cfg.Require("stage")
	stageId := cfg.Require("stageId")

	physicalName := provider.PhysicalName(stage, name, "lambda", stageId)

	err := ctx.RegisterComponentResource(p.GetTypeToken(ctx), name, l, opts...)
	if err != nil {
		return nil, err
	}

	// 1. IAM Execution Role
	assumeRolePolicy := `{
		"Version": "2012-10-17",
		"Statement": [{
			"Effect": "Allow",
			"Principal": { "Service": "lambda.amazonaws.com" },
			"Action": "sts:AssumeRole"
		}]
	}`

	role := &iam.Role{}
	roleProps := transform.MergeTransform(args.Transform["role"], pulumi.Map{
		"assumeRolePolicy": pulumi.String(assumeRolePolicy),
		"tags": pulumi.StringMap{
			"ManagedBy": pulumi.String("anvil"),
		},
	})
	err = ctx.RegisterResource("aws:iam/role:Role", name+"-role", roleProps, role, pulumi.Parent(l))
	if err != nil {
		return nil, err
	}

	// 2. Attach basic execution policy (CloudWatch Logs)
	err = ctx.RegisterResource("aws:iam/rolePolicyAttachment:RolePolicyAttachment", name+"-basic-exec", pulumi.Map{
		"role":      role.Name,
		"policyArn": pulumi.String("arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole"),
	}, &iam.RolePolicyAttachment{}, pulumi.Parent(l))
	if err != nil {
		return nil, err
	}

	// 3. Lambda Function
	lambdaProps := transform.MergeTransform(args.Transform["lambda"], pulumi.Map{
		"name":    pulumi.String(physicalName),
		"runtime": pulumi.String(args.Runtime),
		"handler": pulumi.String(args.Handler),
		"role":    role.Arn,
	})

	res := &lambda.Function{}
	err = ctx.RegisterResource("aws:lambda/function:Function", name, lambdaProps, res, pulumi.Parent(l))
	if err != nil {
		return nil, err
	}

	l.Arn = res.Arn
	l.FunctionName = res.Name
	l.roleArn = role.Arn

	ctx.RegisterResourceOutputs(l, pulumi.Map{
		"arn":          res.Arn,
		"functionName": res.Name,
	})

	return l, nil
}
