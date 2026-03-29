package aws

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strings"

	"github.com/DamienPace15/anvil/provider/internal/sites"
	"github.com/DamienPace15/anvil/provider/internal/transform"
	p "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi-go-provider/infer"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/acm"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/cloudfront"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/iam"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/lambda"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/route53"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/s3"
)

// SvelteKitSiteArgs defines the inputs for an AWS SvelteKit site deployment.
// Shared inputs are embedded from the framework layer.
//
//anvil:inputs-from sites.SvelteKitSiteInputs
type SvelteKitSiteArgs struct {
	sites.SvelteKitSiteInputs

	// Transform overrides for underlying AWS resources.
	Transform map[string]map[string]interface{} `pulumi:"transform,optional"`
}

// SvelteKitSite is an Anvil-managed SvelteKit deployment on AWS.
// Static assets go to S3 behind CloudFront. Server code runs on Lambda.
//
//anvil:outputs-from sites.SvelteKitSiteOutputs
type SvelteKitSite struct {
	pulumi.ResourceState

	sites.SvelteKitSiteOutputs

	// The CloudFront distribution ID serving the site.
	CloudFrontDistributionID pulumi.StringOutput `pulumi:"cloudFrontDistributionId"`

	// The S3 bucket name storing static assets.
	BucketName pulumi.StringOutput `pulumi:"bucketName"`

	// The Lambda function name running SSR.
	FunctionName pulumi.StringOutput `pulumi:"functionName"`

	// DNS records the user needs to create (if domain is set and not on Route53).
	// Empty if no domain or if Route53 records were created automatically.
	DNSRecords pulumi.StringOutput `pulumi:"dnsRecords"`
}

func (s *SvelteKitSite) Annotate(a infer.Annotator) {
	a.Describe(&s, "An Anvil-managed SvelteKit site deployed on AWS. "+
		"Static assets are served from S3 via CloudFront. "+
		"Server-side rendering runs on Lambda.")
}

func NewSvelteKitSite(ctx *pulumi.Context, name string, args SvelteKitSiteArgs, opts ...pulumi.ResourceOption) (*SvelteKitSite, error) {
	site := &SvelteKitSite{}
	err := ctx.RegisterComponentResource(p.GetTypeToken(ctx), name, site, opts...)
	if err != nil {
		return nil, err
	}

	// ── 1. Build the SvelteKit project ──────────────────────────
	// TODO: ProjectRoot needs to come from the Anvil engine context.
	// For now, use the current working directory.
	projectRoot, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("cannot determine project root: %w", err)
	}

	buildResult, err := sites.BuildSvelteKit(sites.BuildOptions{
		Path:        args.Path,
		ProjectRoot: projectRoot,
		Environment: args.Environment,
	})
	if err != nil {
		return nil, fmt.Errorf("SvelteKit build failed: %w", err)
	}

	// ── 2. S3 Bucket for static assets ──────────────────────────
	bucketProps := transform.MergeTransform(args.Transform["bucket"], pulumi.Map{
		"forceDestroy": pulumi.Bool(true),
		"tags": pulumi.StringMap{
			"ManagedBy": pulumi.String("anvil"),
			"Component": pulumi.String("SvelteKitSite"),
		},
	})

	bucket := &s3.BucketV2{}
	err = ctx.RegisterResource("aws:s3/bucketV2:BucketV2", name+"-assets", bucketProps, bucket, pulumi.Parent(site))
	if err != nil {
		return nil, err
	}

	// ── 3. CloudFront Origin Access Control ─────────────────────
	oac := &cloudfront.OriginAccessControl{}
	err = ctx.RegisterResource("aws:cloudfront/originAccessControl:OriginAccessControl", name+"-oac", pulumi.Map{
		"name":                          pulumi.Sprintf("%s-oac", name),
		"originAccessControlOriginType": pulumi.String("s3"),
		"signingBehavior":               pulumi.String("always"),
		"signingProtocol":               pulumi.String("sigv4"),
	}, oac, pulumi.Parent(site))
	if err != nil {
		return nil, err
	}

	// ── 4. Lambda function for SSR ──────────────────────────────
	// 4a. IAM role
	assumeRolePolicy := `{
		"Version": "2012-10-17",
		"Statement": [{
			"Action": "sts:AssumeRole",
			"Principal": {"Service": "lambda.amazonaws.com"},
			"Effect": "Allow"
		}]
	}`

	lambdaRole := &iam.Role{}
	err = ctx.RegisterResource("aws:iam/role:Role", name+"-lambda-role", pulumi.Map{
		"assumeRolePolicy": pulumi.String(assumeRolePolicy),
		"tags": pulumi.StringMap{
			"ManagedBy": pulumi.String("anvil"),
		},
	}, lambdaRole, pulumi.Parent(site))
	if err != nil {
		return nil, err
	}

	// Attach basic execution policy (CloudWatch logs)
	err = ctx.RegisterResource("aws:iam/rolePolicyAttachment:RolePolicyAttachment", name+"-lambda-logs", pulumi.Map{
		"role":      lambdaRole.Name,
		"policyArn": pulumi.String("arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole"),
	}, &iam.RolePolicyAttachment{}, pulumi.Parent(site))
	if err != nil {
		return nil, err
	}

	// 4b. Package the server code as a zip archive
	// The server directory from adapter-node contains the Node.js server.
	// Lambda expects a zip file with the handler at the root.
	serverArchive, err := createServerArchive(buildResult.ServerDir)
	if err != nil {
		return nil, fmt.Errorf("failed to package server code: %w", err)
	}

	// 4c. Lambda function
	// Uses AWS Lambda Web Adapter to run adapter-node's HTTP server on Lambda.
	// The adapter layer starts the Node server and proxies Lambda events to it.
	lambdaEnv := pulumi.StringMap{
		"NODE_ENV":                pulumi.String("production"),
		"AWS_LWA_PORT":            pulumi.String("3000"),
		"AWS_LAMBDA_EXEC_WRAPPER": pulumi.String("/opt/bootstrap"),
	}
	// Inject user-provided environment variables for runtime.
	for k, v := range args.Environment {
		lambdaEnv[k] = pulumi.String(v)
	}

	// Lambda Web Adapter layer ARN — region is interpolated at deploy time.
	// Account 753240598075 is the AWS-maintained public layer account.
	// Version 24 is the latest arm64 release as of March 2026.
	region, _ := ctx.GetConfig("aws:region")
	webAdapterLayerARN := pulumi.Sprintf(
		"arn:aws:lambda:%s:753240598075:layer:LambdaAdapterLayerArm64:24",
		region,
	)

	lambdaProps := transform.MergeTransform(args.Transform["function"], pulumi.Map{
		"runtime":       pulumi.String("nodejs20.x"),
		"handler":       pulumi.String("run.sh"),
		"role":          lambdaRole.Arn,
		"code":          pulumi.NewFileArchive(serverArchive),
		"timeout":       pulumi.Int(30),
		"memorySize":    pulumi.Int(1024),
		"architectures": pulumi.StringArray{pulumi.String("arm64")},
		"layers":        pulumi.StringArray{webAdapterLayerARN},
		"environment": pulumi.Map{
			"variables": lambdaEnv,
		},
		"tags": pulumi.StringMap{
			"ManagedBy": pulumi.String("anvil"),
		},
	})

	lambdaFn := &lambda.Function{}
	err = ctx.RegisterResource("aws:lambda/function:Function", name+"-server", lambdaProps, lambdaFn, pulumi.Parent(site))
	if err != nil {
		return nil, err
	}

	// 4d. Lambda function URL (SSR origin for CloudFront)
	fnURL := &lambda.FunctionUrl{}
	err = ctx.RegisterResource("aws:lambda/functionUrl:FunctionUrl", name+"-fn-url", pulumi.Map{
		"functionName":      lambdaFn.Name,
		"authorizationType": pulumi.String("NONE"),
	}, fnURL, pulumi.Parent(site))
	if err != nil {
		return nil, err
	}

	// ── 5. Upload static assets to S3 ───────────────────────────
	err = uploadStaticAssets(ctx, site, name, bucket, buildResult.StaticDir)
	if err != nil {
		return nil, fmt.Errorf("failed to upload static assets: %w", err)
	}

	// ── 6. CloudFront distribution ──────────────────────────────
	// Parse the Lambda function URL to extract the domain (strip https:// and trailing /)
	lambdaOriginDomain := fnURL.FunctionUrl.ApplyT(func(url string) string {
		url = strings.TrimPrefix(url, "https://")
		url = strings.TrimSuffix(url, "/")
		return url
	}).(pulumi.StringOutput)

	// Build CloudFront config
	cfArgs := buildCloudFrontArgs(name, bucket, oac, lambdaOriginDomain, args.Domain)

	distribution := &cloudfront.Distribution{}
	err = ctx.RegisterResource("aws:cloudfront/distribution:Distribution", name+"-cdn", cfArgs, distribution, pulumi.Parent(site))
	if err != nil {
		return nil, err
	}

	// ── 7. S3 bucket policy (allow CloudFront OAC access) ───────
	finalBucketPolicy := pulumi.All(bucket.Arn, distribution.Arn).ApplyT(func(args []interface{}) string {
		bucketArn := args[0].(string)
		distArn := args[1].(string)
		return fmt.Sprintf(`{
			"Version": "2012-10-17",
			"Statement": [{
				"Sid": "AllowCloudFrontOAC",
				"Effect": "Allow",
				"Principal": {"Service": "cloudfront.amazonaws.com"},
				"Action": "s3:GetObject",
				"Resource": "%s/*",
				"Condition": {
					"StringEquals": {
						"AWS:SourceArn": "%s"
					}
				}
			}]
		}`, bucketArn, distArn)
	}).(pulumi.StringOutput)

	err = ctx.RegisterResource("aws:s3/bucketPolicy:BucketPolicy", name+"-bucket-policy", pulumi.Map{
		"bucket": bucket.Bucket,
		"policy": finalBucketPolicy,
	}, &s3.BucketPolicy{}, pulumi.Parent(site))
	if err != nil {
		return nil, err
	}

	// ── 8. Custom domain (optional) ─────────────────────────────
	dnsRecordsOutput := pulumi.String("").ToStringOutput()

	if args.Domain != "" {
		dnsResult, err := setupCustomDomain(ctx, site, name, args.Domain, distribution)
		if err != nil {
			return nil, fmt.Errorf("custom domain setup failed: %w", err)
		}
		dnsRecordsOutput = dnsResult
	}

	// Note: CloudFront invalidation skipped for MVP.
	// Hashed assets (_app/immutable/*) never need invalidation.
	// SSR responses use CachingDisabled. Only non-hashed static files
	// (favicon, etc.) could serve stale — acceptable for now.

	// ── 10. Set outputs ─────────────────────────────────────────
	siteURL := distribution.DomainName.ApplyT(func(domain string) string {
		return "https://" + domain
	}).(pulumi.StringOutput)

	if args.Domain != "" {
		siteURL = pulumi.Sprintf("https://%s", args.Domain)
	}

	site.URL = siteURL
	site.CloudFrontDistributionID = distribution.ID().ToStringOutput()
	site.BucketName = bucket.Bucket
	site.FunctionName = lambdaFn.Name
	site.DNSRecords = dnsRecordsOutput

	ctx.RegisterResourceOutputs(site, pulumi.Map{
		"url":                      siteURL,
		"cloudFrontDistributionId": distribution.ID(),
		"bucketName":               bucket.Bucket,
		"functionName":             lambdaFn.Name,
		"dnsRecords":               dnsRecordsOutput,
	})

	return site, nil
}

// buildCloudFrontArgs constructs the CloudFront distribution arguments.
func buildCloudFrontArgs(name string, bucket *s3.BucketV2, oac *cloudfront.OriginAccessControl, lambdaOriginDomain pulumi.StringOutput, domain string) pulumi.Map {
	s3OriginID := name + "-s3"
	lambdaOriginID := name + "-lambda"

	origins := pulumi.Array{
		// S3 origin for static assets
		pulumi.Map{
			"domainName":            bucket.BucketRegionalDomainName,
			"originId":              pulumi.String(s3OriginID),
			"originAccessControlId": oac.ID(),
		},
		// Lambda function URL origin for SSR
		pulumi.Map{
			"domainName": lambdaOriginDomain,
			"originId":   pulumi.String(lambdaOriginID),
			"customOriginConfig": pulumi.Map{
				"httpPort":             pulumi.Int(80),
				"httpsPort":            pulumi.Int(443),
				"originProtocolPolicy": pulumi.String("https-only"),
				"originSslProtocols":   pulumi.StringArray{pulumi.String("TLSv1.2")},
			},
		},
	}

	// Default behaviour → Lambda (SSR)
	defaultCacheBehavior := pulumi.Map{
		"targetOriginId":        pulumi.String(lambdaOriginID),
		"viewerProtocolPolicy":  pulumi.String("redirect-to-https"),
		"allowedMethods":        pulumi.StringArray{pulumi.String("GET"), pulumi.String("HEAD"), pulumi.String("OPTIONS"), pulumi.String("PUT"), pulumi.String("POST"), pulumi.String("PATCH"), pulumi.String("DELETE")},
		"cachedMethods":         pulumi.StringArray{pulumi.String("GET"), pulumi.String("HEAD")},
		"compress":              pulumi.Bool(true),
		"cachePolicyId":         pulumi.String("4135ea2d-6df8-44a3-9df3-4b5a84be39ad"), // CachingDisabled
		"originRequestPolicyId": pulumi.String("b689b0a8-53d0-40ab-baf2-68738e2966ac"), // AllViewerExceptHostHeader
	}

	// Ordered cache behaviours — static assets to S3
	orderedCacheBehaviors := pulumi.Array{
		// Hashed immutable assets → long cache
		pulumi.Map{
			"pathPattern":          pulumi.String("/_app/immutable/*"),
			"targetOriginId":       pulumi.String(s3OriginID),
			"viewerProtocolPolicy": pulumi.String("redirect-to-https"),
			"allowedMethods":       pulumi.StringArray{pulumi.String("GET"), pulumi.String("HEAD")},
			"cachedMethods":        pulumi.StringArray{pulumi.String("GET"), pulumi.String("HEAD")},
			"compress":             pulumi.Bool(true),
			"cachePolicyId":        pulumi.String("658327ea-f89d-4fab-a63d-7e88639e58f6"), // CachingOptimized
		},
		// Other static assets (favicon, manifest, etc.) → shorter cache
		pulumi.Map{
			"pathPattern":          pulumi.String("/_app/*"),
			"targetOriginId":       pulumi.String(s3OriginID),
			"viewerProtocolPolicy": pulumi.String("redirect-to-https"),
			"allowedMethods":       pulumi.StringArray{pulumi.String("GET"), pulumi.String("HEAD")},
			"cachedMethods":        pulumi.StringArray{pulumi.String("GET"), pulumi.String("HEAD")},
			"compress":             pulumi.Bool(true),
			"cachePolicyId":        pulumi.String("658327ea-f89d-4fab-a63d-7e88639e58f6"), // CachingOptimized
		},
	}

	cfArgs := pulumi.Map{
		"enabled":               pulumi.Bool(true),
		"isIpv6Enabled":         pulumi.Bool(true),
		"httpVersion":           pulumi.String("http2and3"),
		"priceClass":            pulumi.String("PriceClass_100"),
		"defaultRootObject":     pulumi.String(""),
		"origins":               origins,
		"defaultCacheBehavior":  defaultCacheBehavior,
		"orderedCacheBehaviors": orderedCacheBehaviors,
		"restrictions": pulumi.Map{
			"geoRestriction": pulumi.Map{
				"restrictionType": pulumi.String("none"),
			},
		},
		"viewerCertificate": pulumi.Map{
			"cloudfrontDefaultCertificate": pulumi.Bool(true),
		},
		"tags": pulumi.StringMap{
			"ManagedBy": pulumi.String("anvil"),
		},
	}

	// If a custom domain is set, configure the alias and override viewer certificate later.
	// This is handled in setupCustomDomain which modifies the distribution after creation.
	if domain != "" {
		cfArgs["aliases"] = pulumi.StringArray{pulumi.String(domain)}
	}

	return cfArgs
}

// uploadStaticAssets walks the static asset directory and creates a BucketObjectv2
// for each file with appropriate content type and cache headers.
func uploadStaticAssets(ctx *pulumi.Context, parent pulumi.Resource, name string, bucket *s3.BucketV2, staticDir string) error {
	return filepath.Walk(staticDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}

		// Key is the relative path from the static dir.
		relPath, err := filepath.Rel(staticDir, path)
		if err != nil {
			return err
		}
		// Use forward slashes for S3 keys.
		key := filepath.ToSlash(relPath)

		// Determine content type.
		contentType := mime.TypeByExtension(filepath.Ext(path))
		if contentType == "" {
			contentType = "application/octet-stream"
		}

		// Determine cache control based on path.
		cacheControl := "public, max-age=0, s-maxage=86400, stale-while-revalidate=8640"
		if strings.Contains(key, "_app/immutable/") {
			// Hashed assets — cache forever.
			cacheControl = "public, max-age=31536000, immutable"
		}

		// Hash the file content for a stable resource name.
		content, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("cannot read %s: %w", path, err)
		}
		hash := sha256.Sum256(content)
		hashStr := hex.EncodeToString(hash[:8])

		// Sanitise the key for use as a Pulumi resource name.
		resourceName := fmt.Sprintf("%s-asset-%s-%s", name, sanitiseResourceName(key), hashStr)

		err = ctx.RegisterResource("aws:s3/bucketObjectv2:BucketObjectv2", resourceName, pulumi.Map{
			"bucket":       bucket.Bucket,
			"key":          pulumi.String(key),
			"source":       pulumi.NewFileAsset(path),
			"contentType":  pulumi.String(contentType),
			"cacheControl": pulumi.String(cacheControl),
		}, &s3.BucketObjectv2{}, pulumi.Parent(parent))
		if err != nil {
			return fmt.Errorf("failed to upload %s: %w", key, err)
		}

		return nil
	})
}

// setupCustomDomain provisions an ACM certificate, optionally creates Route53 records,
// and returns DNS instructions for the user.
func setupCustomDomain(ctx *pulumi.Context, parent pulumi.Resource, name string, domain string, distribution *cloudfront.Distribution) (pulumi.StringOutput, error) {

	// ACM certificate must be in us-east-1 for CloudFront.
	// We create an explicit provider for us-east-1.
	usEast1, err := createUSEast1Provider(ctx, name, parent)
	if err != nil {
		return pulumi.StringOutput{}, err
	}

	cert := &acm.Certificate{}
	err = ctx.RegisterResource("aws:acm/certificate:Certificate", name+"-cert", pulumi.Map{
		"domainName":       pulumi.String(domain),
		"validationMethod": pulumi.String("DNS"),
		"tags": pulumi.StringMap{
			"ManagedBy": pulumi.String("anvil"),
		},
	}, cert, pulumi.Parent(parent), pulumi.Provider(usEast1))
	if err != nil {
		return pulumi.StringOutput{}, err
	}

	// Try to find a Route53 hosted zone for automatic DNS validation.
	dnsInstructions := cert.DomainValidationOptions.ApplyT(func(opts []acm.CertificateDomainValidationOption) string {
		if len(opts) == 0 {
			return ""
		}

		var instructions []string
		for _, opt := range opts {
			if opt.ResourceRecordName != nil && opt.ResourceRecordValue != nil && opt.ResourceRecordType != nil {
				instructions = append(instructions, fmt.Sprintf(
					"  %s %s → %s",
					*opt.ResourceRecordType,
					*opt.ResourceRecordName,
					*opt.ResourceRecordValue,
				))
			}
		}

		if len(instructions) == 0 {
			return ""
		}

		return fmt.Sprintf(
			"Add these DNS records to validate your certificate for %s:\n%s\n\n"+
				"Then add a CNAME pointing %s to your CloudFront distribution domain.",
			domain,
			strings.Join(instructions, "\n"),
			domain,
		)
	}).(pulumi.StringOutput)

	// Attempt Route53 auto-validation.
	// Look up a hosted zone matching the domain.
	autoValidateRoute53(ctx, parent, name, domain, cert, distribution)

	return dnsInstructions, nil
}

// autoValidateRoute53 attempts to find a Route53 hosted zone and create
// DNS validation records automatically. If no hosted zone is found,
// this is a no-op — the user will see the DNS instructions in the output.
func autoValidateRoute53(ctx *pulumi.Context, parent pulumi.Resource, name string, domain string, cert *acm.Certificate, distribution *cloudfront.Distribution) {
	// Extract the zone name from the domain (e.g. "app.example.com" → "example.com")
	zoneName := extractZoneName(domain)

	// Look up the hosted zone. This is best-effort — if it fails, we skip.
	zone, err := route53.LookupZone(ctx, &route53.LookupZoneArgs{
		Name:        pulumi.StringRef(zoneName),
		PrivateZone: pulumi.BoolRef(false),
	})
	if err != nil || zone == nil {
		ctx.Log.Info(fmt.Sprintf("No Route53 hosted zone found for %s — add DNS records manually (see dnsRecords output)", zoneName), nil)
		return
	}

	// Create validation records.
	cert.DomainValidationOptions.ApplyT(func(opts []acm.CertificateDomainValidationOption) error {
		for i, opt := range opts {
			if opt.ResourceRecordName == nil || opt.ResourceRecordValue == nil || opt.ResourceRecordType == nil {
				continue
			}

			resourceName := fmt.Sprintf("%s-cert-validation-%d", name, i)
			err := ctx.RegisterResource("aws:route53/record:Record", resourceName, pulumi.Map{
				"zoneId": pulumi.String(zone.ZoneId),
				"name":   pulumi.String(*opt.ResourceRecordName),
				"type":   pulumi.String(*opt.ResourceRecordType),
				"ttl":    pulumi.Int(300),
				"records": pulumi.StringArray{
					pulumi.String(*opt.ResourceRecordValue),
				},
			}, &route53.Record{}, pulumi.Parent(parent))
			if err != nil {
				ctx.Log.Warn(fmt.Sprintf("Failed to create Route53 validation record: %v — add DNS records manually", err), nil)
			}
		}
		return nil
	})

	// Also create the CNAME/A record pointing the domain to CloudFront.
	// Use A record with alias — works for both apex and subdomains.
	// CloudFront's hosted zone ID is always Z2FDTNDATAQYW2 (global constant).
	err = ctx.RegisterResource("aws:route53/record:Record", name+"-domain-a", pulumi.Map{
		"zoneId": pulumi.String(zone.ZoneId),
		"name":   pulumi.String(domain),
		"type":   pulumi.String("A"),
		"aliases": pulumi.Array{
			pulumi.Map{
				"name":                 distribution.DomainName,
				"zoneId":               pulumi.String("Z2FDTNDATAQYW2"),
				"evaluateTargetHealth": pulumi.Bool(false),
			},
		},
	}, &route53.Record{}, pulumi.Parent(parent))
	if err != nil {
		ctx.Log.Warn(fmt.Sprintf("Failed to create Route53 A record for %s: %v", domain, err), nil)
	}

	// AAAA record for IPv6 (CloudFront supports IPv6).
	err = ctx.RegisterResource("aws:route53/record:Record", name+"-domain-aaaa", pulumi.Map{
		"zoneId": pulumi.String(zone.ZoneId),
		"name":   pulumi.String(domain),
		"type":   pulumi.String("AAAA"),
		"aliases": pulumi.Array{
			pulumi.Map{
				"name":                 distribution.DomainName,
				"zoneId":               pulumi.String("Z2FDTNDATAQYW2"),
				"evaluateTargetHealth": pulumi.Bool(false),
			},
		},
	}, &route53.Record{}, pulumi.Parent(parent))
	if err != nil {
		ctx.Log.Warn(fmt.Sprintf("Failed to create Route53 AAAA record for %s: %v", domain, err), nil)
	}

	ctx.Log.Info(fmt.Sprintf("Route53 hosted zone found for %s — DNS records created automatically", zoneName), nil)
}

// createUSEast1Provider creates an AWS provider configured for us-east-1.
// ACM certificates for CloudFront must be in this region.
func createUSEast1Provider(ctx *pulumi.Context, name string, parent pulumi.Resource) (*aws.Provider, error) {
	return aws.NewProvider(ctx, name+"-us-east-1", &aws.ProviderArgs{
		Region: pulumi.String("us-east-1"),
	}, pulumi.Parent(parent))
}

// extractZoneName extracts the registrable domain from a full domain name.
// e.g. "app.example.com" → "example.com."
//
//	"example.com" → "example.com."
func extractZoneName(domain string) string {
	parts := strings.Split(domain, ".")
	if len(parts) >= 2 {
		// Take the last two parts as the zone name.
		return strings.Join(parts[len(parts)-2:], ".") + "."
	}
	return domain + "."
}

// createServerArchive packages the adapter-node server output into a zip
// file suitable for Lambda deployment with AWS Lambda Web Adapter.
//
// The zip contains:
//   - run.sh — startup script that launches the Node.js server
//   - server/ — the adapter-node output (index.js + dependencies)
//
// Lambda Web Adapter starts run.sh, which starts the Node server on port 3000.
// The adapter then proxies Lambda events to the server via HTTP.
func createServerArchive(serverDir string) (string, error) {
	// Create a temp directory for the Lambda package.
	tmpDir, err := os.MkdirTemp("", "anvil-lambda-*")
	if err != nil {
		return "", fmt.Errorf("cannot create temp dir: %w", err)
	}

	// Write run.sh — the startup script Lambda Web Adapter will execute.
	runSh := `#!/bin/bash
exec node /var/task/server/index.js
`
	runShPath := filepath.Join(tmpDir, "run.sh")
	if err := os.WriteFile(runShPath, []byte(runSh), 0755); err != nil {
		return "", fmt.Errorf("cannot write run.sh: %w", err)
	}

	// Copy the server directory into the package.
	destServerDir := filepath.Join(tmpDir, "server")
	if err := copyDir(serverDir, destServerDir); err != nil {
		return "", fmt.Errorf("cannot copy server files: %w", err)
	}

	// Return the temp directory path — Pulumi's FileArchive will zip it.
	return tmpDir, nil
}

// copyDir recursively copies a directory tree.
func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		destPath := filepath.Join(dst, relPath)

		if info.IsDir() {
			return os.MkdirAll(destPath, info.Mode())
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(destPath, data, info.Mode())
	})
}

// sanitiseResourceName converts a file path into a valid Pulumi resource name.
func sanitiseResourceName(s string) string {
	replacer := strings.NewReplacer(
		"/", "-",
		".", "-",
		" ", "-",
		"_", "-",
	)
	result := replacer.Replace(s)
	// Remove any double dashes.
	for strings.Contains(result, "--") {
		result = strings.ReplaceAll(result, "--", "-")
	}
	return strings.Trim(result, "-")
}
