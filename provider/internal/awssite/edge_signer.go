package awssite

import (
	_ "embed"
	"fmt"

	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/iam"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/lambda"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// The edge signer used by ProtectionEdgeOAC: a Lambda@Edge origin-request
// function that adds x-amz-content-sha256 to requests with a body, following
// AWS's guidance for POST/PUT to an IAM-protected Function URL behind
// CloudFront. See docs/design/site-protection.md.

//go:embed edge/edge-signer.js
var edgeSignerCode string

const edgeAssumeRolePolicy = `{"Version":"2012-10-17","Statement":[{"Action":"sts:AssumeRole","Effect":"Allow","Principal":{"Service":["lambda.amazonaws.com","edgelambda.amazonaws.com"]}}]}`

// SetupEdgeSigner creates the Lambda@Edge signer in us-east-1 (a Lambda@Edge
// requirement) and returns its qualified (versioned) ARN for the distribution's
// origin-request association.
//
// Lambda@Edge is replicated to every edge location, so deleting it takes a few
// minutes after the distribution stops using it.
func SetupEdgeSigner(ctx *pulumi.Context, parent pulumi.Resource, name string) (pulumi.StringOutput, error) {
	role := &iam.Role{}
	err := ctx.RegisterResource("aws:iam/role:Role", name+"-edge-signer-role", pulumi.Map{
		"assumeRolePolicy": pulumi.String(edgeAssumeRolePolicy),
		"managedPolicyArns": pulumi.StringArray{
			pulumi.String("arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole"),
		},
		"tags": pulumi.StringMap{"ManagedBy": pulumi.String("anvil")},
	}, role, pulumi.Parent(parent))
	if err != nil {
		return pulumi.StringOutput{}, fmt.Errorf("failed to create edge signer role: %w", err)
	}

	fn := &lambda.Function{}
	err = ctx.RegisterResource("aws:lambda/function:Function", name+"-edge-signer", pulumi.Map{
		"description": pulumi.Sprintf("Anvil edge signer for site %s: adds x-amz-content-sha256 for CloudFront OAC", name),
		"runtime":     pulumi.String("nodejs22.x"),
		"handler":     pulumi.String("index.handler"),
		"role":        role.Arn,
		"code": pulumi.NewAssetArchive(map[string]interface{}{
			"index.js": pulumi.NewStringAsset(edgeSignerCode),
		}),
		// Lambda@Edge needs a published version, runs on x86_64 only, and has no
		// environment variables.
		"publish":       pulumi.Bool(true),
		"architectures": pulumi.StringArray{pulumi.String("x86_64")},
		"memorySize":    pulumi.Int(128),
		"timeout":       pulumi.Int(5),
		"region":        pulumi.String(usEast1),
		"tags":          pulumi.StringMap{"ManagedBy": pulumi.String("anvil")},
	}, fn, pulumi.Parent(parent))
	if err != nil {
		return pulumi.StringOutput{}, fmt.Errorf("failed to create edge signer: %w", err)
	}

	return fn.QualifiedArn, nil
}
