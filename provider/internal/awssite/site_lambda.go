package awssite

import (
	"fmt"

	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/iam"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/lambda"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

const (
	lambdaAssumeRolePolicy = `{"Version":"2012-10-17","Statement":[{"Action":"sts:AssumeRole","Principal":{"Service":"lambda.amazonaws.com"},"Effect":"Allow"}]}`

	// LWALayerArch is the Lambda Web Adapter layer for arm64.
	// This layer injects the LWA bootstrap that proxies HTTP between
	// CloudFront and the Node.js server process.
	lwaAccountID    = "753240598075"
	lwaLayerName    = "LambdaAdapterLayerArm64"
	lwaLayerVersion = "24"
)

// SiteLambdaRoleResult holds the IAM role created for a site Lambda function.
type SiteLambdaRoleResult struct {
	Role *iam.Role
}

// CreateSiteLambdaRole creates an IAM execution role for a site Lambda function
// with basic CloudWatch Logs permissions attached.
func CreateSiteLambdaRole(ctx *pulumi.Context, parent pulumi.Resource, name string) (*SiteLambdaRoleResult, error) {
	role := &iam.Role{}
	err := ctx.RegisterResource("aws:iam/role:Role", name+"-site-lambda-role", pulumi.Map{
		"assumeRolePolicy": pulumi.String(lambdaAssumeRolePolicy),
		"tags":             pulumi.StringMap{"ManagedBy": pulumi.String("anvil")},
	}, role, pulumi.Parent(parent))
	if err != nil {
		return nil, err
	}

	err = ctx.RegisterResource("aws:iam/rolePolicyAttachment:RolePolicyAttachment", name+"-site-lambda-logs", pulumi.Map{
		"role":      role.Name,
		"policyArn": pulumi.String("arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole"),
	}, &iam.RolePolicyAttachment{}, pulumi.Parent(parent))
	if err != nil {
		return nil, err
	}

	return &SiteLambdaRoleResult{Role: role}, nil
}

// SiteLWALayerARN returns the Lambda Web Adapter layer ARN for arm64 in the given region.
// The LWA layer handles HTTP proxying between CloudFront and the Node.js server process.
func SiteLWALayerARN(region string) pulumi.StringOutput {
	return pulumi.Sprintf("arn:aws:lambda:%s:%s:layer:%s:%s", region, lwaAccountID, lwaLayerName, lwaLayerVersion)
}

// CreateSiteFunctionURL creates a public Lambda Function URL for the given function.
// The URL is used as the CloudFront Lambda origin — CloudFront handles auth,
// so the Function URL itself uses NONE auth.
func CreateSiteFunctionURL(ctx *pulumi.Context, parent pulumi.Resource, name string, fn *lambda.Function) (*lambda.FunctionUrl, error) {
	fnURL := &lambda.FunctionUrl{}
	err := ctx.RegisterResource("aws:lambda/functionUrl:FunctionUrl", name+"-site-fn-url", pulumi.Map{
		"functionName":      fn.Name,
		"authorizationType": pulumi.String("NONE"),
	}, fnURL, pulumi.Parent(parent))
	if err != nil {
		return nil, fmt.Errorf("failed to create site function URL: %w", err)
	}
	return fnURL, nil
}
