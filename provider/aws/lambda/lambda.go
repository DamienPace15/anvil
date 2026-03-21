package lambda

import (
	"github.com/DamienPace15/anvil/provider/internal/transform"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/lambda"
	p "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi-go-provider/infer"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

type LambdaArgs struct {
	Name      string                            `pulumi:"name"`
	Vpc       string                            `pulumi:"vpc,optional"`
	Transform map[string]map[string]interface{} `pulumi:"transform,optional"`
}

type Lambda struct {
	pulumi.ResourceState

	Arn          pulumi.StringOutput `pulumi:"arn"`
	FunctionName pulumi.StringOutput `pulumi:"functionName"`
}

func (l *Lambda) Annotate(a infer.Annotator) {
	a.Describe(&l, "An Anvil-managed Lambda function.")
}

func NewLambda(ctx *pulumi.Context, name string, args LambdaArgs, opts ...pulumi.ResourceOption) (*Lambda, error) {
	l := &Lambda{}
	err := ctx.RegisterComponentResource(p.GetTypeToken(ctx), name, l, opts...)
	if err != nil {
		return nil, err
	}

	lambdaProps := transform.MergeTransform(args.Transform["lambda"], pulumi.Map{
		"name":    pulumi.String(args.Name),
		"runtime": pulumi.String("nodejs18.x"),
		"handler": pulumi.String("index.handler"),
	})

	res := &lambda.Function{}
	err = ctx.RegisterResource("aws:lambda/function:Function", name, lambdaProps, res, pulumi.Parent(l))
	if err != nil {
		return nil, err
	}

	l.Arn = res.Arn
	l.FunctionName = res.Name

	ctx.RegisterResourceOutputs(l, pulumi.Map{
		"arn":          res.Arn,
		"functionName": res.Name,
	})

	return l, nil
}
