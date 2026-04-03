package bucket

import (
	provider "github.com/DamienPace15/anvil/provider/internal/shared"
	"github.com/DamienPace15/anvil/provider/internal/transform"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/s3"
	p "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi-go-provider/infer"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	c "github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

// BucketArgs matches the inputProperties in your modular schema.json
type BucketArgs struct {
	Lifecycle  int                               `pulumi:"lifecycle,optional"`
	Compliance []provider.ComplianceFramework    `pulumi:"compliance,optional"`
	Transform  map[string]map[string]interface{} `pulumi:"transform,optional"`
}

type Bucket struct {
	pulumi.ResourceState

	BucketName pulumi.StringOutput `pulumi:"bucketName"`
	Arn        pulumi.StringOutput `pulumi:"arn"`
	name       string
}

// Annotate helps the provider understand the Pulumi token
func (b *Bucket) Annotate(a infer.Annotator) {
	a.SetToken("aws", "Bucket")
	a.Describe(&b, "An Anvil-managed S3 bucket with secure-by-default settings and optional compliance enforcement.")
}

func NewBucket(ctx *pulumi.Context, name string, args BucketArgs, opts ...pulumi.ResourceOption) (*Bucket, error) {
	b := &Bucket{name: name}

	provider.NewContext(ctx)

	cfg := c.New(ctx, "anvil")
	stage := cfg.Require("stage")
	stageId := cfg.Require("stageId")
	environment := cfg.Require("environment")

	isProduction := environment == "prod"

	physicalName := provider.PhysicalName(stage, name, "bucket", stageId)

	opts = provider.WithDefault(opts, true)

	err := ctx.RegisterComponentResource(p.GetTypeToken(ctx), name, b, opts...)
	if err != nil {
		return nil, err
	}

	// ── Compliance resolution ──────────────────────────
	// 1. Read app-level defaults from Pulumi config (set by App class)
	// 2. Merge with component-level frameworks (additive, never replaces)
	// 3. Filter to only storage-applicable frameworks
	appCompliance := provider.GetAppCompliance(ctx)
	resolved := provider.ResolveCompliance(appCompliance, args.Compliance)
	compliance := provider.FilterFrameworks(resolved, provider.StorageFrameworks)

	// ── Enforcement flags ──────────────────────────────

	// Versioning: required by most frameworks and always on in prod
	needsVersioning := isProduction ||
		provider.HasFramework(compliance, provider.ComplianceSOC2) ||
		provider.HasFramework(compliance, provider.ComplianceISO27001) ||
		provider.HasFramework(compliance, provider.ComplianceCIS) ||
		provider.HasFramework(compliance, provider.CompliancePCIDSS) ||
		provider.HasFramework(compliance, provider.ComplianceHIPAA) ||
		provider.HasFramework(compliance, provider.ComplianceFedRAMP) ||
		provider.HasFramework(compliance, provider.ComplianceIRAP)

	// Encryption: all frameworks require at-rest encryption
	needsEncryption := len(compliance) > 0

	// KMS: stricter frameworks require SSE-KMS over SSE-S3 for key audit trails
	needsKMS :=
		provider.HasFramework(compliance, provider.ComplianceHIPAA) ||
			provider.HasFramework(compliance, provider.CompliancePCIDSS) ||
			provider.HasFramework(compliance, provider.ComplianceFedRAMP) ||
			provider.HasFramework(compliance, provider.ComplianceIRAP)

	// MFA delete: strictest frameworks only, always on in prod when these are active
	needsMFADelete :=
		provider.HasFramework(compliance, provider.ComplianceHIPAA) ||
			provider.HasFramework(compliance, provider.CompliancePCIDSS) ||
			provider.HasFramework(compliance, provider.ComplianceFedRAMP)

	// Logging: audit trail requirement across most frameworks
	needsLogging :=
		provider.HasFramework(compliance, provider.ComplianceSOC2) ||
			provider.HasFramework(compliance, provider.ComplianceISO27001) ||
			provider.HasFramework(compliance, provider.ComplianceCIS) ||
			provider.HasFramework(compliance, provider.CompliancePCIDSS) ||
			provider.HasFramework(compliance, provider.ComplianceHIPAA) ||
			provider.HasFramework(compliance, provider.ComplianceFedRAMP) ||
			provider.HasFramework(compliance, provider.ComplianceIRAP)

	// GDPR version expiry: versioning is required by other frameworks but GDPR
	// requires the ability to erase data — expire old versions via lifecycle so
	// deletion requests can be honoured.
	needsVersionExpiry :=
		provider.HasFramework(compliance, provider.ComplianceGDPR) && needsVersioning

	// TLS policy: any active compliance framework or production environment
	needsTLSPolicy := len(compliance) > 0 || isProduction

	// ── 1. Base Bucket ─────────────────────────────────
	bucketProps := transform.MergeTransform(args.Transform["bucket"], pulumi.Map{
		"bucket":       pulumi.String(physicalName),
		"forceDestroy": pulumi.Bool(true),
		"tags": pulumi.StringMap{
			"ManagedBy": pulumi.String("anvil"),
		},
	})

	res := &s3.Bucket{}
	err = ctx.RegisterResource("aws:s3/bucket:Bucket", name, bucketProps, res, pulumi.Parent(b))
	if err != nil {
		return nil, err
	}

	// ── 2. Public Access Block (always on) ─────────────
	pabProps := transform.MergeTransform(args.Transform["publicAccessBlock"], pulumi.Map{
		"bucket":                res.ID(),
		"blockPublicAcls":       pulumi.Bool(true),
		"blockPublicPolicy":     pulumi.Bool(true),
		"ignorePublicAcls":      pulumi.Bool(true),
		"restrictPublicBuckets": pulumi.Bool(true),
	})
	err = ctx.RegisterResource("aws:s3/bucketPublicAccessBlock:BucketPublicAccessBlock", name+"-pab", pabProps, &s3.BucketPublicAccessBlock{}, pulumi.Parent(b))
	if err != nil {
		return nil, err
	}

	// ── 3. Versioning ──────────────────────────────────
	if _, hasTransform := args.Transform["versioning"]; hasTransform || needsVersioning {
		versioningConfig := pulumi.Map{
			"status": pulumi.String("Enabled"),
		}

		vProps := transform.MergeTransform(args.Transform["versioning"], pulumi.Map{
			"bucket":                  res.ID(),
			"versioningConfiguration": versioningConfig,
		})

		// MFA delete — requires versioning to be enabled first.
		// Only applied when stricter frameworks are active.
		if needsMFADelete {
			vProps["mfa"] = pulumi.String("") // user must supply MFA via transform["versioning"].mfa
		}

		err = ctx.RegisterResource("aws:s3/bucketVersioningV2:BucketVersioningV2", name+"-ver", vProps, &s3.BucketVersioningV2{}, pulumi.Parent(b))
		if err != nil {
			return nil, err
		}
	}

	// ── 4. Server-side encryption ──────────────────────
	// SSE-KMS for stricter frameworks (HIPAA, PCI-DSS, FedRAMP, IRAP).
	// SSE-S3 (AES256) for all others. Both satisfy baseline encryption requirements.
	if needsEncryption {
		sseAlgorithm := "AES256"
		if needsKMS {
			sseAlgorithm = "aws:kms"
		}

		encProps := transform.MergeTransform(args.Transform["encryption"], pulumi.Map{
			"bucket": res.ID(),
			"serverSideEncryptionConfiguration": pulumi.Map{
				"rule": pulumi.Map{
					"applyServerSideEncryptionByDefault": pulumi.Map{
						"sseAlgorithm": pulumi.String(sseAlgorithm),
					},
					"bucketKeyEnabled": pulumi.Bool(true),
				},
			},
		})
		err = ctx.RegisterResource("aws:s3/bucketServerSideEncryptionConfigurationV2:BucketServerSideEncryptionConfigurationV2", name+"-enc", encProps, &s3.BucketServerSideEncryptionConfigurationV2{}, pulumi.Parent(b))
		if err != nil {
			return nil, err
		}
	}

	// ── 5. Server access logging ───────────────────────
	// Logs written to self by default. Override target bucket via
	// transform["logging"].targetBucket for production setups.
	if needsLogging {
		logProps := transform.MergeTransform(args.Transform["logging"], pulumi.Map{
			"bucket":       res.ID(),
			"targetBucket": res.ID(),
			"targetPrefix": pulumi.Sprintf("s3-access-logs/%s/", physicalName),
		})
		err = ctx.RegisterResource("aws:s3/bucketLoggingV2:BucketLoggingV2", name+"-log", logProps, &s3.BucketLoggingV2{}, pulumi.Parent(b))
		if err != nil {
			return nil, err
		}
	}

	// ── 6. TLS-only bucket policy ──────────────────────
	if needsTLSPolicy {
		bucketArn := res.Arn
		tlsPolicy := pulumi.Sprintf(`{
			"Version": "2012-10-17",
			"Statement": [{
				"Sid": "DenyNonTLS",
				"Effect": "Deny",
				"Principal": "*",
				"Action": "s3:*",
				"Resource": ["%s", "%s/*"],
				"Condition": {
					"Bool": { "aws:SecureTransport": "false" }
				}
			}]
		}`, bucketArn, bucketArn)

		policyProps := transform.MergeTransform(args.Transform["policy"], pulumi.Map{
			"bucket": res.ID(),
			"policy": tlsPolicy,
		})
		err = ctx.RegisterResource("aws:s3/bucketPolicy:BucketPolicy", name+"-policy", policyProps, &s3.BucketPolicy{}, pulumi.Parent(b))
		if err != nil {
			return nil, err
		}
	}

	// ── 7. GDPR version expiry lifecycle ──────────────
	// GDPR requires the ability to honour erasure requests. When versioning is
	// active (required by other frameworks), old versions must expire so deleted
	// objects are actually gone within a reasonable window.
	// Default: expire non-current versions after 90 days.
	if needsVersionExpiry {
		lifecycleProps := transform.MergeTransform(args.Transform["lifecycleRules"], pulumi.Map{
			"bucket": res.ID(),
			"rules": pulumi.Array{
				pulumi.Map{
					"id":     pulumi.String("gdpr-version-expiry"),
					"status": pulumi.String("Enabled"),
					"noncurrentVersionExpiration": pulumi.Map{
						"noncurrentDays": pulumi.Int(90),
					},
				},
			},
		})
		err = ctx.RegisterResource("aws:s3/bucketLifecycleConfigurationV2:BucketLifecycleConfigurationV2", name+"-lifecycle", lifecycleProps, &s3.BucketLifecycleConfigurationV2{}, pulumi.Parent(b))
		if err != nil {
			return nil, err
		}
	}

	b.BucketName = res.Bucket
	b.Arn = res.Arn

	ctx.RegisterResourceOutputs(b, pulumi.Map{
		"bucketName": res.Bucket,
		"arn":        res.Arn,
	})

	return b, nil
}
