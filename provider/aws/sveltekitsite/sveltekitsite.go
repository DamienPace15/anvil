package aws

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/DamienPace15/anvil/provider/internal/awssite"
	"github.com/DamienPace15/anvil/provider/internal/transform"
	"github.com/DamienPace15/anvil/provider/sites/sveltekit"
	p "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi-go-provider/infer"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/cloudfront"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/lambda"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/s3"
)

type SvelteKitSiteArgs struct {
	Path string `pulumi:"path"`

	// Environment vars available at BOTH build time and runtime.
	// Values must be string literals since they're needed before the build runs.
	Environment map[string]string `pulumi:"environment,optional"`

	// Runtime-only environment vars set on the Lambda function.
	// Supports Pulumi Output values (e.g. bucket.name, fn.arn).
	// Only available at request time, NOT during build/prerendering.
	RuntimeEnvironment map[string]interface{} `pulumi:"runtimeEnvironment,optional"`

	Domain    string                            `pulumi:"domain,optional"`
	Transform map[string]map[string]interface{} `pulumi:"transform,optional"`
}

type SvelteKitSite struct {
	pulumi.ResourceState
	URL                      pulumi.StringOutput `pulumi:"url"`
	CloudFrontDistributionID pulumi.StringOutput `pulumi:"cloudFrontDistributionId"`
	BucketName               pulumi.StringOutput `pulumi:"bucketName"`
	FunctionName             pulumi.StringOutput `pulumi:"functionName"`
	DNSRecords               pulumi.StringOutput `pulumi:"dnsRecords"`
}

func (s *SvelteKitSiteArgs) Annotate(a infer.Annotator) {
	a.SetToken("aws", "SvelteKitSite")
}

func (s *SvelteKitSite) Annotate(a infer.Annotator) {
	a.SetToken("aws", "SvelteKitSite")
	a.Describe(&s, "An Anvil-managed SvelteKit site deployed on AWS. Static assets are served from S3 via CloudFront. Server-side rendering runs on Lambda.")
}

func NewSvelteKitSite(ctx *pulumi.Context, name string, args SvelteKitSiteArgs, opts ...pulumi.ResourceOption) (*SvelteKitSite, error) {
	site := &SvelteKitSite{}
	err := ctx.RegisterComponentResource(p.GetTypeToken(ctx), name, site, opts...)
	if err != nil {
		return nil, err
	}

	projectRoot, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("cannot determine project root: %w", err)
	}

	// ── Build ────────────────────────────────────────────────────
	buildResult, err := sveltekit.BuildSvelteKit(sveltekit.BuildOptions{
		Path:        args.Path,
		ProjectRoot: projectRoot,
		Environment: args.Environment,
	})
	if err != nil {
		return nil, fmt.Errorf("SvelteKit build failed: %w", err)
	}

	// ── S3 Bucket ────────────────────────────────────────────────
	bucketProps := transform.MergeTransform(args.Transform["bucket"], pulumi.Map{
		"forceDestroy": pulumi.Bool(true),
		"tags": pulumi.StringMap{
			"ManagedBy": pulumi.String("anvil"),
			"Component": pulumi.String("SvelteKitSite"),
		},
	})
	bucket := &s3.Bucket{}
	err = ctx.RegisterResource("aws:s3/bucketV2:BucketV2", name+"-assets", bucketProps, bucket, pulumi.Parent(site))
	if err != nil {
		return nil, err
	}

	// ── OAC ──────────────────────────────────────────────────────
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

	// ── Lambda IAM role ──────────────────────────────────────────
	roleResult, err := awssite.CreateSiteLambdaRole(ctx, site, name)
	if err != nil {
		return nil, err
	}

	// ── Server archive ───────────────────────────────────────────
	serverArchive, err := createSvelteKitServerArchive(buildResult.ServerDir)
	if err != nil {
		return nil, fmt.Errorf("failed to package server code: %w", err)
	}

	// ── Lambda env vars ──────────────────────────────────────────
	lambdaEnv := pulumi.Map{
		"NODE_ENV":                pulumi.String("production"),
		"AWS_LWA_PORT":            pulumi.String("3000"),
		"AWS_LAMBDA_EXEC_WRAPPER": pulumi.String("/opt/bootstrap"),
	}
	for k, v := range args.Environment {
		lambdaEnv[k] = pulumi.String(v)
	}
	for k, v := range args.RuntimeEnvironment {
		lambdaEnv[k] = awssite.CoerceToStringOutput(v)
	}

	region, _ := ctx.GetConfig("aws:region")

	lambdaProps := transform.MergeTransform(args.Transform["function"], pulumi.Map{
		"runtime":       pulumi.String("nodejs20.x"),
		"handler":       pulumi.String("run.sh"),
		"role":          roleResult.Role.Arn,
		"code":          pulumi.NewFileArchive(serverArchive),
		"timeout":       pulumi.Int(30),
		"memorySize":    pulumi.Int(1024),
		"architectures": pulumi.StringArray{pulumi.String("arm64")},
		"layers":        pulumi.StringArray{awssite.SiteLWALayerARN(region)},
		"environment":   pulumi.Map{"variables": lambdaEnv},
		"tags":          pulumi.StringMap{"ManagedBy": pulumi.String("anvil")},
	})

	lambdaFn := &lambda.Function{}
	err = ctx.RegisterResource("aws:lambda/function:Function", name+"-server", lambdaProps, lambdaFn, pulumi.Parent(site))
	if err != nil {
		return nil, err
	}

	// ── Function URL ─────────────────────────────────────────────
	fnURL, err := awssite.CreateSiteFunctionURL(ctx, site, name, lambdaFn)
	if err != nil {
		return nil, err
	}

	// ── Upload static assets ─────────────────────────────────────
	// SvelteKit immutable paths: content-hashed, safe for long-term caching.
	err = awssite.UploadSiteAssets(ctx, site, name, bucket, buildResult.StaticDir, []string{
		"_app/immutable/",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to upload static assets: %w", err)
	}

	// ── Custom domain + ACM cert ─────────────────────────────────
	var certARN pulumi.StringOutput
	dnsRecordsOutput := pulumi.String("").ToStringOutput()
	var cfDependencies []pulumi.Resource

	if args.Domain != "" {
		dr, err := awssite.SetupCustomDomain(ctx, site, name, args.Domain)
		if err != nil {
			return nil, fmt.Errorf("custom domain setup failed: %w", err)
		}
		certARN = dr.CertARN
		dnsRecordsOutput = dr.DNSInstructions
		cfDependencies = append(cfDependencies, dr.Validation)
	}

	// ── CloudFront ───────────────────────────────────────────────
	s3OriginID := name + "-s3"
	lambdaOriginDomain := awssite.LambdaOriginDomainFromURL(fnURL.FunctionUrl)

	// SvelteKit-specific cache behaviors: immutable hashed assets and
	// general _app assets both route to S3.
	sveltekitCacheBehaviors := pulumi.Array{
		awssite.S3CacheBehavior("/_app/immutable/*", s3OriginID),
		awssite.S3CacheBehavior("/_app/*", s3OriginID),
	}

	cfOpts := []pulumi.ResourceOption{pulumi.Parent(site)}
	for _, dep := range cfDependencies {
		cfOpts = append(cfOpts, pulumi.DependsOn([]pulumi.Resource{dep}))
	}

	distribution := &cloudfront.Distribution{}
	err = ctx.RegisterResource("aws:cloudfront/distribution:Distribution", name+"-cdn",
		awssite.BuildCloudFrontArgs(awssite.CloudFrontArgs{
			Name:                  name,
			Bucket:                bucket,
			OAC:                   oac,
			LambdaOriginDomain:    lambdaOriginDomain,
			Domain:                args.Domain,
			CertARN:               certARN,
			OrderedCacheBehaviors: sveltekitCacheBehaviors,
		}),
		distribution, cfOpts...)
	if err != nil {
		return nil, err
	}

	// ── S3 bucket policy ─────────────────────────────────────────
	finalBucketPolicy := pulumi.All(bucket.Arn, distribution.Arn).ApplyT(func(vals []interface{}) string {
		bucketArn := vals[0].(string)
		distArn := vals[1].(string)
		return fmt.Sprintf(`{"Version":"2012-10-17","Statement":[{"Sid":"AllowCloudFrontOAC","Effect":"Allow","Principal":{"Service":"cloudfront.amazonaws.com"},"Action":"s3:GetObject","Resource":"%s/*","Condition":{"StringEquals":{"AWS:SourceArn":"%s"}}}]}`, bucketArn, distArn)
	}).(pulumi.StringOutput)

	err = ctx.RegisterResource("aws:s3/bucketPolicy:BucketPolicy", name+"-bucket-policy", pulumi.Map{
		"bucket": bucket.Bucket,
		"policy": finalBucketPolicy,
	}, &s3.BucketPolicy{}, pulumi.Parent(site))
	if err != nil {
		return nil, err
	}

	// ── Route53 records ──────────────────────────────────────────
	if args.Domain != "" {
		awssite.CreateRoute53Records(ctx, site, name, args.Domain, distribution)
	}

	// ── Outputs ──────────────────────────────────────────────────
	siteURL := distribution.DomainName.ApplyT(func(d string) string { return "https://" + d }).(pulumi.StringOutput)
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

// createSvelteKitServerArchive packages the adapter-node build output into a
// temp directory ready for Lambda deployment.
//
// adapter-node output layout:
//
//	build/
//	├── client/   ← uploaded to S3, not included here
//	└── server/   ← Node.js bundle
//	    └── index.js
//
// Lambda Web Adapter requires a run.sh entry point that execs the server.
func createSvelteKitServerArchive(serverDir string) (string, error) {
	tmpDir, err := os.MkdirTemp("", "anvil-sveltekit-lambda-*")
	if err != nil {
		return "", fmt.Errorf("cannot create temp dir: %w", err)
	}

	// LWA entry point: exec the Node.js server process.
	if err := os.WriteFile(filepath.Join(tmpDir, "run.sh"), []byte("#!/bin/bash\nexec node /var/task/index.js\n"), 0755); err != nil {
		return "", fmt.Errorf("cannot write run.sh: %w", err)
	}

	// Copy everything from the build dir except client/ (handled by S3 upload).
	buildDir := filepath.Dir(serverDir)
	entries, err := os.ReadDir(buildDir)
	if err != nil {
		return "", fmt.Errorf("cannot read build dir: %w", err)
	}

	for _, entry := range entries {
		if entry.Name() == "client" {
			continue
		}
		src := filepath.Join(buildDir, entry.Name())
		dst := filepath.Join(tmpDir, entry.Name())
		if entry.IsDir() {
			if err := awssite.CopyDir(src, dst); err != nil {
				return "", err
			}
		} else {
			data, _ := os.ReadFile(src)
			os.WriteFile(dst, data, 0644)
		}
	}

	return tmpDir, nil
}
