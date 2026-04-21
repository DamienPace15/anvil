package awssite

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/secretsmanager"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/wafv2"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// OriginProtectionArgs configures CloudFront origin protection.
// Currently only 'cloudflare' is supported as the provider.
type OriginProtectionArgs struct {
	// Provider is the CDN/proxy in front of CloudFront.
	// Only "cloudflare" is supported.
	Provider string
}

// OriginProtectionResult holds the outputs of SetupOriginProtection.
type OriginProtectionResult struct {
	// WebACLArn is the ARN of the WAF WebACL to attach to the CloudFront distribution.
	WebACLArn pulumi.StringOutput

	// OriginSecret is the plaintext secret value.
	// Output this so the user can configure it in Cloudflare Transform Rules
	// as the X-Origin-Secret header value.
	OriginSecret pulumi.StringOutput
}

// SetupOriginProtection provisions a WAF WebACL and Secrets Manager secret
// that together enforce the secret header pattern:
//
//  1. Anvil generates a 32-byte random secret and stores it in Secrets Manager.
//  2. A WAF WebACL (CLOUDFRONT scope, us-east-1) blocks any request where the
//     x-origin-secret header does not exactly match the secret.
//  3. The secret value is output so the user can add it to Cloudflare Transform
//     Rules, which inject the header on every proxied request.
//
// The WAF must be created in us-east-1 — this is an AWS requirement for WAFs
// attached to CloudFront distributions.
func SetupOriginProtection(ctx *pulumi.Context, parent pulumi.Resource, name, stage string, _ OriginProtectionArgs) (*OriginProtectionResult, error) {
	// Generate a 32-byte random secret.
	secretBytes := make([]byte, 32)
	if _, err := rand.Read(secretBytes); err != nil {
		return nil, fmt.Errorf("failed to generate origin secret: %w", err)
	}
	secretValue := hex.EncodeToString(secretBytes)

	// Secrets Manager secret name: anvil-{stage}-{name}-origin-secret
	secretName := fmt.Sprintf("anvil-%s-%s-origin-secret", stage, name)

	// Store the secret in Secrets Manager.
	secret := &secretsmanager.Secret{}
	err := ctx.RegisterResource("aws:secretsmanager/secret:Secret", name+"-origin-secret", pulumi.Map{
		"name":        pulumi.String(secretName),
		"description": pulumi.Sprintf("Origin protection secret for Anvil site %s (stage: %s)", name, stage),
		"tags":        pulumi.StringMap{"ManagedBy": pulumi.String("anvil")},
	}, secret, pulumi.Parent(parent))
	if err != nil {
		return nil, fmt.Errorf("failed to create origin secret: %w", err)
	}

	secretVersion := &secretsmanager.SecretVersion{}
	err = ctx.RegisterResource("aws:secretsmanager/secretVersion:SecretVersion", name+"-origin-secret-version", pulumi.Map{
		"secretId":     secret.ID(),
		"secretString": pulumi.String(secretValue),
	}, secretVersion, pulumi.Parent(parent))
	if err != nil {
		return nil, fmt.Errorf("failed to store origin secret value: %w", err)
	}

	// WAF WebACL must be in us-east-1 for CloudFront.
	usEast1, err := CreateUSEast1Provider(ctx, name+"-waf", parent)
	if err != nil {
		return nil, err
	}

	// WAF WebACL: block requests missing the correct x-origin-secret header.
	// Default action is ALLOW — the rule below BLOCKS non-matching requests.
	// Rule logic: if x-origin-secret header does NOT exactly match the secret → BLOCK.
	webACL := &wafv2.WebAcl{}
	err = ctx.RegisterResource("aws:wafv2/webAcl:WebAcl", name+"-origin-waf", pulumi.Map{
		"name":        pulumi.Sprintf("%s-origin-protection", name),
		"scope":       pulumi.String("CLOUDFRONT"),
		"description": pulumi.Sprintf("Origin protection WAF for Anvil site %s", name),
		"defaultAction": pulumi.Map{
			"block": pulumi.Map{},
		},
		"rules": pulumi.Array{
			pulumi.Map{
				"name":     pulumi.String("allow-valid-origin-secret"),
				"priority": pulumi.Int(0),
				"action": pulumi.Map{
					"allow": pulumi.Map{},
				},
				"statement": pulumi.Map{
					"byteMatchStatement": pulumi.Map{
						"searchString": pulumi.String(secretValue),
						"fieldToMatch": pulumi.Map{
							"singleHeader": pulumi.Map{
								"name": pulumi.String("x-origin-secret"),
							},
						},
						"textTransformations": pulumi.Array{
							pulumi.Map{
								"priority": pulumi.Int(0),
								"type":     pulumi.String("NONE"),
							},
						},
						"positionalConstraint": pulumi.String("EXACTLY"),
					},
				},
				"visibilityConfig": pulumi.Map{
					"cloudwatchMetricsEnabled": pulumi.Bool(true),
					"metricName":               pulumi.Sprintf("%s-allow-valid-secret", name),
					"sampledRequestsEnabled":   pulumi.Bool(true),
				},
			},
		},
		"visibilityConfig": pulumi.Map{
			"cloudwatchMetricsEnabled": pulumi.Bool(true),
			"metricName":               pulumi.Sprintf("%s-origin-protection", name),
			"sampledRequestsEnabled":   pulumi.Bool(true),
		},
		"tags": pulumi.StringMap{"ManagedBy": pulumi.String("anvil")},
	}, webACL, pulumi.Parent(parent), pulumi.Provider(usEast1))
	if err != nil {
		return nil, fmt.Errorf("failed to create origin protection WAF: %w", err)
	}

	return &OriginProtectionResult{
		WebACLArn:    webACL.Arn,
		OriginSecret: pulumi.String(secretValue).ToStringOutput(),
	}, nil
}
