package awssite

import (
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/cloudfront"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// Origin protection locks a site's CloudFront distribution to the CDN/proxy in
// front of it (Cloudflare, Fastly, Akamai, nginx, ...). The proxy adds the
// x-origin-secret header to every request; a CloudFront Function rejects
// requests without it and strips the header before the origin sees it.
// See docs/design/site-protection.md.

//go:embed edge/origin-guard.js
var originGuardCode string

// OriginSecretHeader is the header the proxy must send on every request.
const OriginSecretHeader = "x-origin-secret"

// originSecretKey is the KeyValueStore key holding the secret. Must match
// SECRET_KEYS in edge/origin-guard.js.
const originSecretKey = "origin-secret"

// OriginProtectionResult holds the outputs of SetupOriginProtection.
type OriginProtectionResult struct {
	// FunctionArn is the CloudFront Function to associate with every cache
	// behavior as a viewer-request function.
	FunctionArn pulumi.StringOutput

	// OriginSecret is the header value to configure in the proxy. Secret.
	OriginSecret pulumi.StringOutput
}

// SetupOriginProtection creates the secret and the CloudFront Function that
// checks it.
//
// The secret is stored in a CloudFront KeyValueStore rather than in the
// function code, so reading the function doesn't reveal it. It is generated
// on first deploy and kept stable afterwards: the key ignores changes to its
// value, so later deploys (which generate a fresh candidate) leave it alone
// and the proxy configuration keeps working.
func SetupOriginProtection(ctx *pulumi.Context, parent pulumi.Resource, name string) (*OriginProtectionResult, error) {
	secretBytes := make([]byte, 32)
	if _, err := rand.Read(secretBytes); err != nil {
		return nil, fmt.Errorf("failed to generate origin secret: %w", err)
	}

	store := &cloudfront.KeyValueStore{}
	err := ctx.RegisterResource("aws:cloudfront/keyValueStore:KeyValueStore", name+"-origin-kvs", pulumi.Map{
		"comment": pulumi.Sprintf("Anvil origin protection secret for site %s", name),
	}, store, pulumi.Parent(parent))
	if err != nil {
		return nil, fmt.Errorf("failed to create origin secret store: %w", err)
	}

	secretKey := &cloudfront.KeyvaluestoreKey{}
	err = ctx.RegisterResource("aws:cloudfront/keyvaluestoreKey:KeyvaluestoreKey", name+"-origin-secret", pulumi.Map{
		"keyValueStoreArn": store.Arn,
		"key":              pulumi.String(originSecretKey),
		"value":            pulumi.ToSecret(pulumi.String(hex.EncodeToString(secretBytes))),
	}, secretKey,
		pulumi.Parent(parent),
		pulumi.IgnoreChanges([]string{"value"}),
		pulumi.AdditionalSecretOutputs([]string{"value"}),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to store origin secret: %w", err)
	}

	// cf.kvs() takes the store ID, the last segment of its ARN.
	code := store.Arn.ApplyT(func(arn string) string {
		return strings.Replace(originGuardCode, "__KVS_ID__", arn[strings.LastIndex(arn, "/")+1:], 1)
	}).(pulumi.StringOutput)

	fn := &cloudfront.Function{}
	err = ctx.RegisterResource("aws:cloudfront/function:Function", name+"-origin-guard", pulumi.Map{
		"runtime":                   pulumi.String("cloudfront-js-2.0"),
		"comment":                   pulumi.Sprintf("Anvil origin protection for site %s", name),
		"code":                      code,
		"keyValueStoreAssociations": pulumi.StringArray{store.Arn},
		"publish":                   pulumi.Bool(true),
	}, fn, pulumi.Parent(parent), pulumi.DependsOn([]pulumi.Resource{secretKey}))
	if err != nil {
		return nil, fmt.Errorf("failed to create origin guard function: %w", err)
	}

	return &OriginProtectionResult{
		FunctionArn:  fn.Arn,
		OriginSecret: pulumi.ToSecret(secretKey.Value).(pulumi.StringOutput),
	}, nil
}
