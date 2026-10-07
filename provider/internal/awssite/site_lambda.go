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

// CreateSiteFunctionURL creates the Lambda Function URL used as the CloudFront
// Lambda origin. IAM-protected modes use AWS_IAM auth so only the site's
// CloudFront distribution (via OAC) can invoke it; ProtectionNone uses NONE.
func CreateSiteFunctionURL(ctx *pulumi.Context, parent pulumi.Resource, name string, fn *lambda.Function, protection string) (*lambda.FunctionUrl, error) {
	authType := "NONE"
	if IsIAMProtected(protection) {
		authType = "AWS_IAM"
	}

	fnURL := &lambda.FunctionUrl{}
	err := ctx.RegisterResource("aws:lambda/functionUrl:FunctionUrl", name+"-site-fn-url", pulumi.Map{
		"functionName":      fn.Name,
		"authorizationType": pulumi.String(authType),
	}, fnURL, pulumi.Parent(parent))
	if err != nil {
		return nil, fmt.Errorf("failed to create site function URL: %w", err)
	}
	return fnURL, nil
}

// GrantSiteFunctionURLAccess adds the resource-based policy that lets the
// Function URL be invoked. Lambda requires both lambda:InvokeFunctionUrl and
// lambda:InvokeFunction (restricted to URL invocations) for every Function URL,
// including NONE ones.
//
// ProtectionNone grants the public principal "*". IAM-protected modes grant only
// the CloudFront service principal, scoped to this site's distribution ARN so no
// other distribution can use it.
//
// Resource names differ per mode so switching modes replaces the grants cleanly.
func GrantSiteFunctionURLAccess(ctx *pulumi.Context, parent pulumi.Resource, name string, fn *lambda.Function, protection string, distributionArn pulumi.StringOutput) error {
	urlPerm := pulumi.Map{
		"action":   pulumi.String("lambda:InvokeFunctionUrl"),
		"function": fn.Name,
	}
	invokePerm := pulumi.Map{
		"action":                pulumi.String("lambda:InvokeFunction"),
		"function":              fn.Name,
		"invokedViaFunctionUrl": pulumi.Bool(true),
	}

	suffix := "public"
	if IsIAMProtected(protection) {
		suffix = "cloudfront"
		for _, perm := range []pulumi.Map{urlPerm, invokePerm} {
			perm["principal"] = pulumi.String("cloudfront.amazonaws.com")
			perm["sourceArn"] = distributionArn
		}
	} else {
		urlPerm["principal"] = pulumi.String("*")
		urlPerm["functionUrlAuthType"] = pulumi.String("NONE")
		invokePerm["principal"] = pulumi.String("*")
	}

	err := ctx.RegisterResource("aws:lambda/permission:Permission", name+"-site-fn-url-"+suffix, urlPerm, &lambda.Permission{}, pulumi.Parent(parent))
	if err != nil {
		return fmt.Errorf("failed to grant function URL access: %w", err)
	}
	err = ctx.RegisterResource("aws:lambda/permission:Permission", name+"-site-fn-invoke-"+suffix, invokePerm, &lambda.Permission{}, pulumi.Parent(parent))
	if err != nil {
		return fmt.Errorf("failed to grant function invoke access: %w", err)
	}
	return nil
}
