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

// SiteWafArgs attaches a WAF to the site. Composes freely with
// originProtection. While a WAF is attached, protection defaults to
// "edge-oac" so the server Function URL can't be used to bypass the WAF.
type SiteWafArgs struct {
	// Arn is the ARN of a WAF WebACL with CLOUDFRONT scope (us-east-1).
	// Pass waf.arn from an Anvil Waf component.
	Arn pulumi.StringInput `pulumi:"arn" schema:"required"`
}

type SvelteKitSiteArgs struct {
	Path string `pulumi:"path"`

	// Environment vars available at BOTH build time and runtime.
	// Values must be string literals since they're needed before the build runs.
	Environment map[string]string `pulumi:"environment,optional"`

	// Runtime-only environment vars set on the Lambda function.
	// Supports Pulumi Output values (e.g. bucket.name, fn.arn).
	// Only available at request time, NOT during build/prerendering.
	RuntimeEnvironment map[string]pulumi.StringInput `pulumi:"runtimeEnvironment,optional"`

	Domain    string                            `pulumi:"domain,optional"`
	Transform map[string]map[string]interface{} `pulumi:"transform,optional"`

	// OriginProtection locks CloudFront to a CDN/proxy in front of it
	// (Cloudflare, Fastly, Akamai, nginx, ...). Requests without the
	// x-origin-secret header are rejected at the edge; configure the proxy to
	// send it on every request with the originSecret output as the value.
	// Protection defaults to "edge-oac" while it's enabled, so the server
	// Function URL can't be reached around the proxy.
	OriginProtection bool `pulumi:"originProtection,optional"`

	// Waf attaches a WAF WebACL to the site's CloudFront distribution.
	// Protection defaults to "edge-oac" while it's attached.
	Waf *SiteWafArgs `pulumi:"waf,optional"`

	// Protection controls who can invoke the server Lambda's Function URL.
	// Default: "none", or "edge-oac" while a WAF is attached or origin
	// protection is enabled. An explicit value always wins.
	// "none": public Function URL; anyone who learns it can call the server
	// directly, bypassing CloudFront. "oac": locked to CloudFront via IAM;
	// requests with a body are rejected unless they already carry an
	// x-amz-content-sha256 header. "edge-oac": "oac" plus a Lambda@Edge
	// function that adds the header, following AWS's guidance — nothing is
	// needed in the app; request bodies over 1 MB are rejected. In "oac" and
	// "edge-oac", a viewer Authorization header is replaced by CloudFront's
	// signature.
	Protection string `pulumi:"protection,optional"`
}

type SvelteKitSite struct {
	pulumi.ResourceState
	URL                      pulumi.StringOutput `pulumi:"url"`
	CloudFrontDistributionID pulumi.StringOutput `pulumi:"cloudFrontDistributionId"`
	BucketName               pulumi.StringOutput `pulumi:"bucketName"`
	FunctionName             pulumi.StringOutput `pulumi:"functionName"`
	DNSRecords               pulumi.StringOutput `pulumi:"dnsRecords"`
	// OriginSecret is the x-origin-secret header value to configure in the
	// CDN/proxy. Secret. Stable across deploys. Only populated when
	// originProtection is enabled.
	OriginSecret pulumi.StringOutput `pulumi:"originSecret"`
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

	// ── Protection mode ──────────────────────────────────────────
	resolved, err := awssite.ResolveProtection(args.Protection, args.Waf != nil, args.OriginProtection)
	if err != nil {
		return nil, err
	}
	protection := resolved.Mode
	if resolved.Notice != "" {
		ctx.Log.Info(resolved.Notice, &pulumi.LogArgs{Resource: site})
	}
	if resolved.Warning != "" {
		ctx.Log.Warn(resolved.Warning, &pulumi.LogArgs{Resource: site})
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

	// Lambda OAC: CloudFront signs requests to the AWS_IAM Function URL.
	var lambdaOAC *cloudfront.OriginAccessControl
	if awssite.IsIAMProtected(protection) {
		lambdaOAC = &cloudfront.OriginAccessControl{}
		err = ctx.RegisterResource("aws:cloudfront/originAccessControl:OriginAccessControl", name+"-lambda-oac", pulumi.Map{
			"name":                          pulumi.Sprintf("%s-lambda-oac", name),
			"originAccessControlOriginType": pulumi.String("lambda"),
			"signingBehavior":               pulumi.String("always"),
			"signingProtocol":               pulumi.String("sigv4"),
		}, lambdaOAC, pulumi.Parent(site))
		if err != nil {
			return nil, err
		}
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
		lambdaEnv[k] = v
	}

	region, _ := ctx.GetConfig("aws:region")

	lambdaProps := transform.MergeTransform(args.Transform["function"], pulumi.Map{
		"runtime":       pulumi.String("nodejs22.x"),
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
	fnURL, err := awssite.CreateSiteFunctionURL(ctx, site, name, lambdaFn, protection)
	if err != nil {
		return nil, err
	}

	// ── Upload static assets ─────────────────────────────────────
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

	// ── WAF / origin protection (opt-in) ─────────────────────────
	var webACLArn, originGuardArn, edgeSignerArn pulumi.StringOutput
	originSecretOutput := pulumi.String("").ToStringOutput()

	if args.Waf != nil {
		webACLArn = args.Waf.Arn.ToStringOutput()
	}

	if awssite.UsesEdgeSigner(protection) {
		edgeSignerArn, err = awssite.SetupEdgeSigner(ctx, site, name)
		if err != nil {
			return nil, err
		}
	}

	if args.OriginProtection {
		pr, err := awssite.SetupOriginProtection(ctx, site, name)
		if err != nil {
			return nil, fmt.Errorf("origin protection setup failed: %w", err)
		}
		originGuardArn = pr.FunctionArn
		originSecretOutput = pr.OriginSecret

		ctx.Log.Info("Origin protection enabled: CloudFront only accepts requests carrying the "+
			awssite.OriginSecretHeader+" header. Configure your CDN/proxy to send it on every request, "+
			"with the originSecret output as the value — until then the site returns 403.",
			&pulumi.LogArgs{Resource: site})
	}

	// ── CloudFront ───────────────────────────────────────────────
	s3OriginID := name + "-s3"
	lambdaOriginDomain := awssite.LambdaOriginDomainFromURL(fnURL.FunctionUrl)

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
			Name:                     name,
			Bucket:                   bucket,
			OAC:                      oac,
			LambdaOriginDomain:       lambdaOriginDomain,
			LambdaOAC:                lambdaOAC,
			Domain:                   args.Domain,
			CertARN:                  certARN,
			OrderedCacheBehaviors:    sveltekitCacheBehaviors,
			WebACLArn:                webACLArn,
			ViewerRequestFunctionArn: originGuardArn,
			EdgeSignerArn:            edgeSignerArn,
		}),
		distribution, cfOpts...)
	if err != nil {
		return nil, err
	}

	// ── Function URL access ──────────────────────────────────────
	err = awssite.GrantSiteFunctionURLAccess(ctx, site, name, lambdaFn, protection, distribution.Arn)
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
	site.OriginSecret = originSecretOutput

	ctx.RegisterResourceOutputs(site, pulumi.Map{
		"url":                      siteURL,
		"cloudFrontDistributionId": distribution.ID(),
		"bucketName":               bucket.Bucket,
		"functionName":             lambdaFn.Name,
		"dnsRecords":               dnsRecordsOutput,
		"originSecret":             originSecretOutput,
	})

	return site, nil
}

// createSvelteKitServerArchive packages the adapter-node build output into a
// temp directory ready for Lambda deployment.
func createSvelteKitServerArchive(serverDir string) (string, error) {
	tmpDir, err := os.MkdirTemp("", "anvil-sveltekit-lambda-*")
	if err != nil {
		return "", fmt.Errorf("cannot create temp dir: %w", err)
	}

	if err := os.WriteFile(filepath.Join(tmpDir, "run.sh"), []byte("#!/bin/bash\nexec node /var/task/index.js\n"), 0755); err != nil {
		return "", fmt.Errorf("cannot write run.sh: %w", err)
	}

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
