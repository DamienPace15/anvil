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

type SvelteKitSiteArgs struct {
	Path        string                            `pulumi:"path"`
	Environment map[string]string                 `pulumi:"environment,optional"`
	Domain      string                            `pulumi:"domain,optional"`
	Transform   map[string]map[string]interface{} `pulumi:"transform,optional"`
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

	buildResult, err := sites.BuildSvelteKit(sites.BuildOptions{
		Path:        args.Path,
		ProjectRoot: projectRoot,
		Environment: args.Environment,
	})
	if err != nil {
		return nil, fmt.Errorf("SvelteKit build failed: %w", err)
	}

	// ── S3 Bucket ───────────────────────────────────────────────
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

	// ── OAC ─────────────────────────────────────────────────────
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

	// ── Lambda for SSR ──────────────────────────────────────────
	assumeRolePolicy := `{"Version":"2012-10-17","Statement":[{"Action":"sts:AssumeRole","Principal":{"Service":"lambda.amazonaws.com"},"Effect":"Allow"}]}`
	lambdaRole := &iam.Role{}
	err = ctx.RegisterResource("aws:iam/role:Role", name+"-lambda-role", pulumi.Map{
		"assumeRolePolicy": pulumi.String(assumeRolePolicy),
		"tags":             pulumi.StringMap{"ManagedBy": pulumi.String("anvil")},
	}, lambdaRole, pulumi.Parent(site))
	if err != nil {
		return nil, err
	}

	err = ctx.RegisterResource("aws:iam/rolePolicyAttachment:RolePolicyAttachment", name+"-lambda-logs", pulumi.Map{
		"role":      lambdaRole.Name,
		"policyArn": pulumi.String("arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole"),
	}, &iam.RolePolicyAttachment{}, pulumi.Parent(site))
	if err != nil {
		return nil, err
	}

	serverArchive, err := createServerArchive(buildResult)
	if err != nil {
		return nil, fmt.Errorf("failed to package server code: %w", err)
	}

	lambdaEnv := pulumi.StringMap{
		"NODE_ENV":                pulumi.String("production"),
		"AWS_LWA_PORT":            pulumi.String("3000"),
		"AWS_LAMBDA_EXEC_WRAPPER": pulumi.String("/opt/bootstrap"),
	}
	for k, v := range args.Environment {
		lambdaEnv[k] = pulumi.String(v)
	}

	region, _ := ctx.GetConfig("aws:region")
	webAdapterLayerARN := pulumi.Sprintf("arn:aws:lambda:%s:753240598075:layer:LambdaAdapterLayerArm64:24", region)

	lambdaProps := transform.MergeTransform(args.Transform["function"], pulumi.Map{
		"runtime":       pulumi.String("nodejs20.x"),
		"handler":       pulumi.String("run.sh"),
		"role":          lambdaRole.Arn,
		"code":          pulumi.NewFileArchive(serverArchive),
		"timeout":       pulumi.Int(30),
		"memorySize":    pulumi.Int(1024),
		"architectures": pulumi.StringArray{pulumi.String("arm64")},
		"layers":        pulumi.StringArray{webAdapterLayerARN},
		"environment":   pulumi.Map{"variables": lambdaEnv},
		"tags":          pulumi.StringMap{"ManagedBy": pulumi.String("anvil")},
	})

	lambdaFn := &lambda.Function{}
	err = ctx.RegisterResource("aws:lambda/function:Function", name+"-server", lambdaProps, lambdaFn, pulumi.Parent(site))
	if err != nil {
		return nil, err
	}

	fnURL := &lambda.FunctionUrl{}
	err = ctx.RegisterResource("aws:lambda/functionUrl:FunctionUrl", name+"-fn-url", pulumi.Map{
		"functionName":      lambdaFn.Name,
		"authorizationType": pulumi.String("NONE"),
	}, fnURL, pulumi.Parent(site))
	if err != nil {
		return nil, err
	}

	// ── Upload static assets ────────────────────────────────────
	err = uploadStaticAssets(ctx, site, name, bucket, buildResult.StaticDir)
	if err != nil {
		return nil, fmt.Errorf("failed to upload static assets: %w", err)
	}

	// ── Custom domain + ACM cert (BEFORE CloudFront) ────────────
	var certARN pulumi.StringOutput
	dnsRecordsOutput := pulumi.String("").ToStringOutput()
	var cfDependencies []pulumi.Resource

	if args.Domain != "" {
		dr, err := setupCustomDomain(ctx, site, name, args.Domain)
		if err != nil {
			return nil, fmt.Errorf("custom domain setup failed: %w", err)
		}
		certARN = dr.certARN
		dnsRecordsOutput = dr.dnsInstructions
		cfDependencies = append(cfDependencies, dr.validation)
	}

	// ── CloudFront distribution ─────────────────────────────────
	lambdaOriginDomain := fnURL.FunctionUrl.ApplyT(func(url string) string {
		return strings.TrimSuffix(strings.TrimPrefix(url, "https://"), "/")
	}).(pulumi.StringOutput)

	cfArgs := buildCloudFrontArgs(name, bucket, oac, lambdaOriginDomain, args.Domain, certARN)
	cfOpts := []pulumi.ResourceOption{pulumi.Parent(site)}
	for _, dep := range cfDependencies {
		cfOpts = append(cfOpts, pulumi.DependsOn([]pulumi.Resource{dep}))
	}

	distribution := &cloudfront.Distribution{}
	err = ctx.RegisterResource("aws:cloudfront/distribution:Distribution", name+"-cdn", cfArgs, distribution, cfOpts...)
	if err != nil {
		return nil, err
	}

	// ── S3 bucket policy ────────────────────────────────────────
	finalBucketPolicy := pulumi.All(bucket.Arn, distribution.Arn).ApplyT(func(args []interface{}) string {
		bucketArn := args[0].(string)
		distArn := args[1].(string)
		return fmt.Sprintf(`{"Version":"2012-10-17","Statement":[{"Sid":"AllowCloudFrontOAC","Effect":"Allow","Principal":{"Service":"cloudfront.amazonaws.com"},"Action":"s3:GetObject","Resource":"%s/*","Condition":{"StringEquals":{"AWS:SourceArn":"%s"}}}]}`, bucketArn, distArn)
	}).(pulumi.StringOutput)

	err = ctx.RegisterResource("aws:s3/bucketPolicy:BucketPolicy", name+"-bucket-policy", pulumi.Map{
		"bucket": bucket.Bucket,
		"policy": finalBucketPolicy,
	}, &s3.BucketPolicy{}, pulumi.Parent(site))
	if err != nil {
		return nil, err
	}

	// ── Route53 records (best-effort) ───────────────────────────
	if args.Domain != "" {
		createRoute53Records(ctx, site, name, args.Domain, distribution)
	}

	// ── Outputs ─────────────────────────────────────────────────
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

type domainResult struct {
	certARN         pulumi.StringOutput
	dnsInstructions pulumi.StringOutput
	validation      pulumi.Resource
}

func setupCustomDomain(ctx *pulumi.Context, parent pulumi.Resource, name string, domain string) (*domainResult, error) {
	usEast1, err := createUSEast1Provider(ctx, name, parent)
	if err != nil {
		return nil, err
	}

	cert := &acm.Certificate{}
	err = ctx.RegisterResource("aws:acm/certificate:Certificate", name+"-cert", pulumi.Map{
		"domainName":       pulumi.String(domain),
		"validationMethod": pulumi.String("DNS"),
		"tags":             pulumi.StringMap{"ManagedBy": pulumi.String("anvil")},
	}, cert, pulumi.Parent(parent), pulumi.Provider(usEast1))
	if err != nil {
		return nil, err
	}

	dnsInstructions := cert.DomainValidationOptions.ApplyT(func(opts []acm.CertificateDomainValidationOption) string {
		if len(opts) == 0 {
			return ""
		}
		var instructions []string
		for _, opt := range opts {
			if opt.ResourceRecordName != nil && opt.ResourceRecordValue != nil && opt.ResourceRecordType != nil {
				instructions = append(instructions, fmt.Sprintf("  %s  Name: %s  Value: %s",
					*opt.ResourceRecordType, *opt.ResourceRecordName, *opt.ResourceRecordValue))

				ctx.Log.Warn(fmt.Sprintf(
					"\n⏳ Add this DNS record to validate your certificate for %s:\n"+
						"   Type:  %s\n"+
						"   Name:  %s\n"+
						"   Value: %s\n"+
						"   Deploy will continue automatically once the record is detected.\n",
					domain, *opt.ResourceRecordType, *opt.ResourceRecordName, *opt.ResourceRecordValue), nil)
			}
		}
		if len(instructions) == 0 {
			return ""
		}
		return fmt.Sprintf("DNS records for %s certificate validation:\n%s", domain, strings.Join(instructions, "\n"))
	}).(pulumi.StringOutput)

	certValidation := &acm.CertificateValidation{}
	err = ctx.RegisterResource("aws:acm/certificateValidation:CertificateValidation", name+"-cert-validation", pulumi.Map{
		"certificateArn": cert.Arn,
	}, certValidation, pulumi.Parent(parent), pulumi.Provider(usEast1))
	if err != nil {
		return nil, err
	}

	return &domainResult{
		certARN:         certValidation.CertificateArn,
		dnsInstructions: dnsInstructions,
		validation:      certValidation,
	}, nil
}

func createRoute53Records(ctx *pulumi.Context, parent pulumi.Resource, name string, domain string, distribution *cloudfront.Distribution) {
	zoneName := extractZoneName(domain)
	zone, err := route53.LookupZone(ctx, &route53.LookupZoneArgs{
		Name: pulumi.StringRef(zoneName), PrivateZone: pulumi.BoolRef(false),
	})
	if err != nil || zone == nil {
		ctx.Log.Info(fmt.Sprintf("No Route53 hosted zone for %s — point DNS to CloudFront manually.", zoneName), nil)
		return
	}

	for _, recordType := range []string{"A", "AAAA"} {
		err = ctx.RegisterResource("aws:route53/record:Record", fmt.Sprintf("%s-domain-%s", name, strings.ToLower(recordType)), pulumi.Map{
			"zoneId": pulumi.String(zone.ZoneId),
			"name":   pulumi.String(domain),
			"type":   pulumi.String(recordType),
			"aliases": pulumi.Array{pulumi.Map{
				"name":                 distribution.DomainName,
				"zoneId":               pulumi.String("Z2FDTNDATAQYW2"),
				"evaluateTargetHealth": pulumi.Bool(false),
			}},
		}, &route53.Record{}, pulumi.Parent(parent))
		if err != nil {
			ctx.Log.Warn(fmt.Sprintf("Failed to create Route53 %s record: %v", recordType, err), nil)
		}
	}
	ctx.Log.Info(fmt.Sprintf("Route53 DNS records created for %s", domain), nil)
}

func buildCloudFrontArgs(name string, bucket *s3.BucketV2, oac *cloudfront.OriginAccessControl, lambdaOriginDomain pulumi.StringOutput, domain string, certARN pulumi.StringOutput) pulumi.Map {
	s3OriginID := name + "-s3"
	lambdaOriginID := name + "-lambda"

	var viewerCertificate pulumi.Map
	if domain != "" {
		viewerCertificate = pulumi.Map{
			"acmCertificateArn":      certARN,
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
				"domainName":            bucket.BucketRegionalDomainName,
				"originId":              pulumi.String(s3OriginID),
				"originAccessControlId": oac.ID(),
			},
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
		},
		"defaultCacheBehavior": pulumi.Map{
			"targetOriginId":        pulumi.String(lambdaOriginID),
			"viewerProtocolPolicy":  pulumi.String("redirect-to-https"),
			"allowedMethods":        pulumi.StringArray{pulumi.String("GET"), pulumi.String("HEAD"), pulumi.String("OPTIONS"), pulumi.String("PUT"), pulumi.String("POST"), pulumi.String("PATCH"), pulumi.String("DELETE")},
			"cachedMethods":         pulumi.StringArray{pulumi.String("GET"), pulumi.String("HEAD")},
			"compress":              pulumi.Bool(true),
			"cachePolicyId":         pulumi.String("4135ea2d-6df8-44a3-9df3-4b5a84be39ad"),
			"originRequestPolicyId": pulumi.String("b689b0a8-53d0-40ab-baf2-68738e2966ac"),
		},
		"orderedCacheBehaviors": pulumi.Array{
			pulumi.Map{
				"pathPattern": pulumi.String("/_app/immutable/*"), "targetOriginId": pulumi.String(s3OriginID),
				"viewerProtocolPolicy": pulumi.String("redirect-to-https"),
				"allowedMethods":       pulumi.StringArray{pulumi.String("GET"), pulumi.String("HEAD")},
				"cachedMethods":        pulumi.StringArray{pulumi.String("GET"), pulumi.String("HEAD")},
				"compress":             pulumi.Bool(true), "cachePolicyId": pulumi.String("658327ea-f89d-4fab-a63d-7e88639e58f6"),
			},
			pulumi.Map{
				"pathPattern": pulumi.String("/_app/*"), "targetOriginId": pulumi.String(s3OriginID),
				"viewerProtocolPolicy": pulumi.String("redirect-to-https"),
				"allowedMethods":       pulumi.StringArray{pulumi.String("GET"), pulumi.String("HEAD")},
				"cachedMethods":        pulumi.StringArray{pulumi.String("GET"), pulumi.String("HEAD")},
				"compress":             pulumi.Bool(true), "cachePolicyId": pulumi.String("658327ea-f89d-4fab-a63d-7e88639e58f6"),
			},
		},
		"restrictions": pulumi.Map{
			"geoRestriction": pulumi.Map{"restrictionType": pulumi.String("none")},
		},
		"viewerCertificate": viewerCertificate,
		"tags":              pulumi.StringMap{"ManagedBy": pulumi.String("anvil")},
	}

	if domain != "" {
		cfArgs["aliases"] = pulumi.StringArray{pulumi.String(domain)}
	}
	return cfArgs
}

func uploadStaticAssets(ctx *pulumi.Context, parent pulumi.Resource, name string, bucket *s3.BucketV2, staticDir string) error {
	return filepath.Walk(staticDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		relPath, _ := filepath.Rel(staticDir, path)
		key := filepath.ToSlash(relPath)
		contentType := mime.TypeByExtension(filepath.Ext(path))
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		cacheControl := "public, max-age=0, s-maxage=86400, stale-while-revalidate=8640"
		if strings.Contains(key, "_app/immutable/") {
			cacheControl = "public, max-age=31536000, immutable"
		}
		content, _ := os.ReadFile(path)
		hash := sha256.Sum256(content)
		hashStr := hex.EncodeToString(hash[:8])
		resourceName := fmt.Sprintf("%s-asset-%s-%s", name, sanitiseResourceName(key), hashStr)
		return ctx.RegisterResource("aws:s3/bucketObjectv2:BucketObjectv2", resourceName, pulumi.Map{
			"bucket": bucket.Bucket, "key": pulumi.String(key),
			"source": pulumi.NewFileAsset(path), "contentType": pulumi.String(contentType),
			"cacheControl": pulumi.String(cacheControl),
		}, &s3.BucketObjectv2{}, pulumi.Parent(parent))
	})
}

func createUSEast1Provider(ctx *pulumi.Context, name string, parent pulumi.Resource) (*aws.Provider, error) {
	return aws.NewProvider(ctx, name+"-us-east-1", &aws.ProviderArgs{Region: pulumi.String("us-east-1")}, pulumi.Parent(parent))
}

func extractZoneName(domain string) string {
	parts := strings.Split(domain, ".")
	if len(parts) >= 2 {
		return strings.Join(parts[len(parts)-2:], ".") + "."
	}
	return domain + "."
}

func createServerArchive(buildResult *sites.BuildResult) (string, error) {
	tmpDir, err := os.MkdirTemp("", "anvil-lambda-*")
	if err != nil {
		return "", fmt.Errorf("cannot create temp dir: %w", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "run.sh"), []byte("#!/bin/bash\nexec node /var/task/index.js\n"), 0755); err != nil {
		return "", fmt.Errorf("cannot write run.sh: %w", err)
	}
	buildDir := filepath.Dir(buildResult.ServerDir)
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
			if err := copyDir(src, dst); err != nil {
				return "", err
			}
		} else {
			data, _ := os.ReadFile(src)
			os.WriteFile(dst, data, 0644)
		}
	}
	return tmpDir, nil
}

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		relPath, _ := filepath.Rel(src, path)
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

func sanitiseResourceName(s string) string {
	result := strings.NewReplacer("/", "-", ".", "-", " ", "-", "_", "-").Replace(s)
	for strings.Contains(result, "--") {
		result = strings.ReplaceAll(result, "--", "-")
	}
	return strings.Trim(result, "-")
}
