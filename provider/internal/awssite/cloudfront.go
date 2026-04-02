package awssite

import (
	"strings"

	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/cloudfront"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/s3"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// CloudFrontArgs holds the inputs for BuildCloudFrontArgs.
type CloudFrontArgs struct {
	// Name is the component name, used to derive origin IDs.
	Name string

	// Bucket is the S3 bucket for static assets (S3 origin).
	Bucket *s3.Bucket

	// OAC is the Origin Access Control for the S3 origin.
	OAC *cloudfront.OriginAccessControl

	// LambdaOriginDomain is the hostname of the Lambda Function URL (no scheme, no trailing slash).
	LambdaOriginDomain pulumi.StringOutput

	// Domain is the optional custom domain. Empty string means use the CloudFront default cert.
	Domain string

	// CertARN is the ACM certificate ARN. Only required when Domain is set.
	CertARN pulumi.StringOutput

	// OrderedCacheBehaviors defines framework-specific path patterns routed to S3.
	// SvelteKit passes /_app/immutable/* and /_app/*.
	// Astro will pass /_astro/*.
	// The default cache behavior (all other paths) always routes to Lambda.
	OrderedCacheBehaviors pulumi.Array
}

// BuildCloudFrontArgs constructs the full CloudFront distribution argument map.
// The default cache behavior routes to Lambda (SSR). Framework-specific static
// asset paths are routed to S3 via OrderedCacheBehaviors.
func BuildCloudFrontArgs(args CloudFrontArgs) pulumi.Map {
	s3OriginID := args.Name + "-s3"
	lambdaOriginID := args.Name + "-lambda"

	var viewerCertificate pulumi.Map
	if args.Domain != "" {
		viewerCertificate = pulumi.Map{
			"acmCertificateArn":      args.CertARN,
			"sslSupportMethod":       pulumi.String("sni-only"),
			"minimumProtocolVersion": pulumi.String("TLSv1.2_2021"),
		}
	} else {
		viewerCertificate = pulumi.Map{
			"cloudfrontDefaultCertificate": pulumi.Bool(true),
		}
	}

	cfArgs := pulumi.Map{
		"enabled":           pulumi.Bool(true),
		"isIpv6Enabled":     pulumi.Bool(true),
		"httpVersion":       pulumi.String("http2and3"),
		"priceClass":        pulumi.String("PriceClass_100"),
		"defaultRootObject": pulumi.String(""),
		"origins": pulumi.Array{
			pulumi.Map{
				"domainName":            args.Bucket.BucketRegionalDomainName,
				"originId":              pulumi.String(s3OriginID),
				"originAccessControlId": args.OAC.ID(),
			},
			pulumi.Map{
				"domainName": args.LambdaOriginDomain,
				"originId":   pulumi.String(lambdaOriginID),
				"customOriginConfig": pulumi.Map{
					"httpPort":             pulumi.Int(80),
					"httpsPort":            pulumi.Int(443),
					"originProtocolPolicy": pulumi.String("https-only"),
					"originSslProtocols":   pulumi.StringArray{pulumi.String("TLSv1.2")},
				},
			},
		},
		// Default: all requests → Lambda (SSR).
		"defaultCacheBehavior": pulumi.Map{
			"targetOriginId":        pulumi.String(lambdaOriginID),
			"viewerProtocolPolicy":  pulumi.String("redirect-to-https"),
			"allowedMethods":        pulumi.StringArray{pulumi.String("GET"), pulumi.String("HEAD"), pulumi.String("OPTIONS"), pulumi.String("PUT"), pulumi.String("POST"), pulumi.String("PATCH"), pulumi.String("DELETE")},
			"cachedMethods":         pulumi.StringArray{pulumi.String("GET"), pulumi.String("HEAD")},
			"compress":              pulumi.Bool(true),
			"cachePolicyId":         pulumi.String("4135ea2d-6df8-44a3-9df3-4b5a84be39ad"), // CachingDisabled
			"originRequestPolicyId": pulumi.String("b689b0a8-53d0-40ab-baf2-68738e2966ac"), // AllViewerExceptHostHeader
		},
		// Framework-specific static asset paths → S3.
		"orderedCacheBehaviors": args.OrderedCacheBehaviors,
		"restrictions": pulumi.Map{
			"geoRestriction": pulumi.Map{"restrictionType": pulumi.String("none")},
		},
		"viewerCertificate": viewerCertificate,
		"tags":              pulumi.StringMap{"ManagedBy": pulumi.String("anvil")},
	}

	if args.Domain != "" {
		cfArgs["aliases"] = pulumi.StringArray{pulumi.String(args.Domain)}
	}

	return cfArgs
}

// S3CacheBehavior returns a CloudFront ordered cache behavior that routes a path
// pattern to S3 with long-term caching (CachingOptimized policy).
// Use for content-hashed static asset paths like /_app/immutable/* or /_astro/*.
func S3CacheBehavior(pathPattern string, s3OriginID string) pulumi.Map {
	return pulumi.Map{
		"pathPattern":          pulumi.String(pathPattern),
		"targetOriginId":       pulumi.String(s3OriginID),
		"viewerProtocolPolicy": pulumi.String("redirect-to-https"),
		"allowedMethods":       pulumi.StringArray{pulumi.String("GET"), pulumi.String("HEAD")},
		"cachedMethods":        pulumi.StringArray{pulumi.String("GET"), pulumi.String("HEAD")},
		"compress":             pulumi.Bool(true),
		"cachePolicyId":        pulumi.String("658327ea-f89d-4fab-a63d-7e88639e58f6"), // CachingOptimized
	}
}

// LambdaOriginDomainFromURL strips the scheme and trailing slash from a Lambda
// Function URL to produce a bare hostname suitable for a CloudFront custom origin.
func LambdaOriginDomainFromURL(fnURL pulumi.StringOutput) pulumi.StringOutput {
	return fnURL.ApplyT(func(url string) string {
		return strings.TrimSuffix(strings.TrimPrefix(url, "https://"), "/")
	}).(pulumi.StringOutput)
}
