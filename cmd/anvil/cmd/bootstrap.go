package cmd

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudtrail"
	cloudtrailtypes "github.com/aws/aws-sdk-go-v2/service/cloudtrail/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var bootstrapRegion string
var bootstrapStage string
var bootstrapEnvironment string

var bootstrapCmd = &cobra.Command{
	Use:   "bootstrap",
	Short: "Set up state storage for Anvil",
	Long:  `Creates an S3 bucket for Anvil state storage and writes the backend configuration to anvil.yaml.`,
	RunE:  runBootstrap,
}

func init() {
	bootstrapCmd.Flags().StringVar(&bootstrapRegion, "region", "", "AWS region for the state bucket (defaults to AWS CLI default)")
	bootstrapCmd.Flags().StringVar(&bootstrapStage, "stage", "dev", "Stage name (used in bucket naming)")
	bootstrapCmd.Flags().StringVar(&bootstrapEnvironment, "environment", "", "Environment type: prod or nonprod")
	rootCmd.AddCommand(bootstrapCmd)
}

// anvilConfig represents the anvil.yaml file with multi-stage support.
type anvilConfig struct {
	Project string                  `yaml:"project"`
	Active  string                  `yaml:"active,omitempty"`
	Stages  map[string]*stageConfig `yaml:"stages"`
}

type stageConfig struct {
	ID               string `yaml:"id"`
	Region           string `yaml:"region"`
	Environment      string `yaml:"environment"`
	LoggingBucket    string `yaml:"loggingBucket,omitempty"`
	CloudTrailArn    string `yaml:"cloudTrailArn,omitempty"`
	ExistingTrailArn string `yaml:"existingTrailArn,omitempty"`
}

// resolveBucketName reconstructs the bucket name from its parts.
func resolveBucketName(stage, project, id string) string {
	return fmt.Sprintf("%s-anvil-state-%s-%s", stage, project, id)
}

func runBootstrap(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	printBanner()
	fmt.Printf("  Bootstrapping \"%s\"...\n\n", bootstrapStage)

	// ── Resolve environment (flag → prompt) ──
	if bootstrapEnvironment == "" {
		env, err := promptEnvironment()
		if err != nil {
			return err
		}
		bootstrapEnvironment = env
	}

	if bootstrapEnvironment != "prod" && bootstrapEnvironment != "nonprod" {
		return fmt.Errorf("Invalid environment %q. Must be \"prod\" or \"nonprod\".", bootstrapEnvironment)
	}

	// ── Load AWS config ──
	var cfgOpts []func(*awsconfig.LoadOptions) error
	if bootstrapRegion != "" {
		cfgOpts = append(cfgOpts, awsconfig.WithRegion(bootstrapRegion))
	}

	cfg, err := awsconfig.LoadDefaultConfig(ctx, cfgOpts...)
	if err != nil {
		return fmt.Errorf("AWS credentials not found or expired.\n  Run `aws configure` or set AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY.")
	}

	stsClient := sts.NewFromConfig(cfg)
	stsOut, err := stsClient.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return fmt.Errorf("AWS credentials not found or expired.\n  Run `aws configure` or set AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY.")
	}
	accountId := aws.ToString(stsOut.Account)

	region := cfg.Region
	if region == "" {
		return fmt.Errorf("No AWS region configured.\n  Set a default with `aws configure` or pass --region.")
	}

	// ── Read project name from anvil.yaml ──
	projectName, err := readProjectName()
	if err != nil {
		return err
	}

	// ── Load existing config (or create new) ──
	config, err := loadAnvilConfig()
	if err != nil {
		config = &anvilConfig{
			Project: projectName,
			Stages:  make(map[string]*stageConfig),
		}
	}

	// ── Check if this stage is already bootstrapped ──
	if sc, ok := config.Stages[bootstrapStage]; ok && sc.ID != "" {
		bucketName := resolveBucketName(bootstrapStage, config.Project, sc.ID)
		return verifyExistingBootstrap(ctx, cfg, accountId, region, bootstrapEnvironment, bucketName)
	}

	// ── Generate state bucket name ──
	suffix, err := randomSuffix(6)
	if err != nil {
		return fmt.Errorf("failed to generate random suffix: %w", err)
	}

	bucketName := resolveBucketName(bootstrapStage, projectName, suffix)

	// ── Create S3 state bucket ──
	s3Client := s3.NewFromConfig(cfg)

	createInput := &s3.CreateBucketInput{
		Bucket: aws.String(bucketName),
	}
	if region != "us-east-1" {
		createInput.CreateBucketConfiguration = &s3types.CreateBucketConfiguration{
			LocationConstraint: s3types.BucketLocationConstraint(region),
		}
	}

	_, err = s3Client.CreateBucket(ctx, createInput)
	if err != nil {
		return fmt.Errorf("failed to create state bucket: %s", mapError(err.Error()))
	}

	if isTTY() {
		fmt.Printf("  %s Created state bucket\n", green("✔"))
	} else {
		fmt.Printf("  [ok] Created state bucket\n")
	}

	// ── Enable versioning ──
	_, err = s3Client.PutBucketVersioning(ctx, &s3.PutBucketVersioningInput{
		Bucket: aws.String(bucketName),
		VersioningConfiguration: &s3types.VersioningConfiguration{
			Status: s3types.BucketVersioningStatusEnabled,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to enable versioning: %w", err)
	}
	printCheck("Versioning enabled")

	// ── Enable encryption ──
	_, err = s3Client.PutBucketEncryption(ctx, &s3.PutBucketEncryptionInput{
		Bucket: aws.String(bucketName),
		ServerSideEncryptionConfiguration: &s3types.ServerSideEncryptionConfiguration{
			Rules: []s3types.ServerSideEncryptionRule{
				{
					ApplyServerSideEncryptionByDefault: &s3types.ServerSideEncryptionByDefault{
						SSEAlgorithm: s3types.ServerSideEncryptionAes256,
					},
				},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("failed to enable encryption: %w", err)
	}
	printCheck("Encryption enabled")

	// ── Block public access ──
	_, err = s3Client.PutPublicAccessBlock(ctx, &s3.PutPublicAccessBlockInput{
		Bucket: aws.String(bucketName),
		PublicAccessBlockConfiguration: &s3types.PublicAccessBlockConfiguration{
			BlockPublicAcls:       aws.Bool(true),
			BlockPublicPolicy:     aws.Bool(true),
			IgnorePublicAcls:      aws.Bool(true),
			RestrictPublicBuckets: aws.Bool(true),
		},
	})
	if err != nil {
		return fmt.Errorf("failed to block public access: %w", err)
	}
	printCheck("Public access blocked")

	// ── TLS-only bucket policy ──
	stateBucketPolicy := fmt.Sprintf(`{
		"Version": "2012-10-17",
		"Statement": [
			{
				"Sid": "DenyNonTLS",
				"Effect": "Deny",
				"Principal": "*",
				"Action": "s3:*",
				"Resource": [
					"arn:aws:s3:::%s",
					"arn:aws:s3:::%s/*"
				],
				"Condition": {
					"Bool": {
						"aws:SecureTransport": "false"
					}
				}
			}
		]
	}`, bucketName, bucketName)

	_, err = s3Client.PutBucketPolicy(ctx, &s3.PutBucketPolicyInput{
		Bucket: aws.String(bucketName),
		Policy: aws.String(stateBucketPolicy),
	})
	if err != nil {
		return fmt.Errorf("failed to apply TLS policy to state bucket: %w", err)
	}
	printCheck("TLS policy applied")

	// ── Logging bucket + CloudTrail ──
	loggingBucket, trailArn, err := setupLoggingInfrastructure(ctx, cfg, accountId, region, bootstrapEnvironment)
	if err != nil {
		return fmt.Errorf("logging setup failed: %w", err)
	}

	// ── Write stage to anvil.yaml ──
	config.Project = projectName
	if config.Stages == nil {
		config.Stages = make(map[string]*stageConfig)
	}

	config.Stages[bootstrapStage] = &stageConfig{
		ID:            suffix,
		Region:        region,
		Environment:   bootstrapEnvironment,
		LoggingBucket: loggingBucket,
		CloudTrailArn: trailArn,
	}

	err = writeAnvilConfig(*config)
	if err != nil {
		return fmt.Errorf("failed to write anvil.yaml: %w", err)
	}
	printCheck("Config written to anvil.yaml")

	fmt.Println()
	if isTTY() {
		fmt.Println(dim("  Ready. Run `anvil deploy` to get started."))
	} else {
		fmt.Println("  Ready. Run `anvil deploy` to get started.")
	}

	return nil
}

// verifyExistingBootstrap checks that an already-bootstrapped bucket is correctly configured,
// and ensures logging infrastructure exists, creating it if missing.
func verifyExistingBootstrap(ctx context.Context, cfg aws.Config, accountId, region, environment, bucketName string) error {
	s3Client := s3.NewFromConfig(cfg)

	_, err := s3Client.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: aws.String(bucketName),
	})
	if err != nil {
		return fmt.Errorf("State bucket not found.\n  It may have been deleted. Remove the stage from anvil.yaml and run `anvil bootstrap` again.")
	}

	if isTTY() {
		fmt.Printf("  %s State bucket exists\n", green("✔"))
	} else {
		fmt.Printf("  [ok] State bucket exists\n")
	}

	versioning, err := s3Client.GetBucketVersioning(ctx, &s3.GetBucketVersioningInput{
		Bucket: aws.String(bucketName),
	})
	if err == nil && versioning.Status == s3types.BucketVersioningStatusEnabled {
		printCheck("Verified: versioning enabled")
	} else {
		printWarn("Versioning is not enabled")
	}

	pab, err := s3Client.GetPublicAccessBlock(ctx, &s3.GetPublicAccessBlockInput{
		Bucket: aws.String(bucketName),
	})
	if err == nil && pab.PublicAccessBlockConfiguration != nil &&
		aws.ToBool(pab.PublicAccessBlockConfiguration.BlockPublicAcls) &&
		aws.ToBool(pab.PublicAccessBlockConfiguration.BlockPublicPolicy) {
		printCheck("Verified: public access blocked")
	} else {
		printWarn("Public access block is not fully configured")
	}

	// ── Ensure logging infrastructure exists ──
	loggingBucket, trailArn, err := setupLoggingInfrastructure(ctx, cfg, accountId, region, environment)
	if err != nil {
		printWarn(fmt.Sprintf("Logging setup incomplete: %v", err))
	} else {
		// Backfill anvil.yaml if logging fields are missing
		config, configErr := loadAnvilConfig()
		if configErr == nil {
			if sc, ok := config.Stages[bootstrapStage]; ok {
				if sc.LoggingBucket == "" || sc.CloudTrailArn == "" {
					sc.LoggingBucket = loggingBucket
					sc.CloudTrailArn = trailArn
					writeAnvilConfig(*config)
					printCheck("Config updated with logging info")
				}
			}
		}
	}

	printCheck("Config up to date")

	fmt.Println()
	if isTTY() {
		fmt.Println(dim("  Nothing to do. Already bootstrapped."))
	} else {
		fmt.Println("  Nothing to do. Already bootstrapped.")
	}

	return nil
}

// setupLoggingInfrastructure ensures the central logging bucket and CloudTrail trail exist.
// Safe to call on every bootstrap — checks before creating.
func setupLoggingInfrastructure(ctx context.Context, cfg aws.Config, accountId, region, environment string) (loggingBucket string, trailArn string, err error) {
	s3Client := s3.NewFromConfig(cfg)
	ctClient := cloudtrail.NewFromConfig(cfg)

	// ── Logging bucket ──
	loggingBucket = fmt.Sprintf("anvil-logging-%s-%s", accountId, region)

	_, err = s3Client.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: aws.String(loggingBucket),
	})
	if err != nil {
		// Bucket doesn't exist — create it
		createInput := &s3.CreateBucketInput{
			Bucket: aws.String(loggingBucket),
		}
		if region != "us-east-1" {
			createInput.CreateBucketConfiguration = &s3types.CreateBucketConfiguration{
				LocationConstraint: s3types.BucketLocationConstraint(region),
			}
		}
		if _, err = s3Client.CreateBucket(ctx, createInput); err != nil {
			return "", "", fmt.Errorf("failed to create logging bucket: %w", err)
		}

		// Block public access
		if _, err = s3Client.PutPublicAccessBlock(ctx, &s3.PutPublicAccessBlockInput{
			Bucket: aws.String(loggingBucket),
			PublicAccessBlockConfiguration: &s3types.PublicAccessBlockConfiguration{
				BlockPublicAcls:       aws.Bool(true),
				BlockPublicPolicy:     aws.Bool(true),
				IgnorePublicAcls:      aws.Bool(true),
				RestrictPublicBuckets: aws.Bool(true),
			},
		}); err != nil {
			return "", "", fmt.Errorf("failed to block public access on logging bucket: %w", err)
		}

		// SSE-S3 encryption
		if _, err = s3Client.PutBucketEncryption(ctx, &s3.PutBucketEncryptionInput{
			Bucket: aws.String(loggingBucket),
			ServerSideEncryptionConfiguration: &s3types.ServerSideEncryptionConfiguration{
				Rules: []s3types.ServerSideEncryptionRule{
					{
						ApplyServerSideEncryptionByDefault: &s3types.ServerSideEncryptionByDefault{
							SSEAlgorithm: s3types.ServerSideEncryptionAes256,
						},
					},
				},
			},
		}); err != nil {
			return "", "", fmt.Errorf("failed to enable encryption on logging bucket: %w", err)
		}

		// Versioning
		if _, err = s3Client.PutBucketVersioning(ctx, &s3.PutBucketVersioningInput{
			Bucket: aws.String(loggingBucket),
			VersioningConfiguration: &s3types.VersioningConfiguration{
				Status: s3types.BucketVersioningStatusEnabled,
			},
		}); err != nil {
			return "", "", fmt.Errorf("failed to enable versioning on logging bucket: %w", err)
		}

		// TLS-only + CloudTrail write policy
		tlsPolicy := fmt.Sprintf(`{
			"Version": "2012-10-17",
			"Statement": [
				{
					"Sid": "DenyNonTLS",
					"Effect": "Deny",
					"Principal": "*",
					"Action": "s3:*",
					"Resource": [
						"arn:aws:s3:::%s",
						"arn:aws:s3:::%s/*"
					],
					"Condition": {
						"Bool": {
							"aws:SecureTransport": "false"
						}
					}
				},
				{
					"Sid": "AllowCloudTrailWrite",
					"Effect": "Allow",
					"Principal": {
						"Service": "cloudtrail.amazonaws.com"
					},
					"Action": "s3:PutObject",
					"Resource": "arn:aws:s3:::%s/cloudtrail/AWSLogs/%s/*",
					"Condition": {
						"StringEquals": {
							"s3:x-amz-acl": "bucket-owner-full-control"
						}
					}
				},
				{
					"Sid": "AllowCloudTrailACLCheck",
					"Effect": "Allow",
					"Principal": {
						"Service": "cloudtrail.amazonaws.com"
					},
					"Action": "s3:GetBucketAcl",
					"Resource": "arn:aws:s3:::%s"
				}
			]
		}`, loggingBucket, loggingBucket, loggingBucket, accountId, loggingBucket)

		if _, err = s3Client.PutBucketPolicy(ctx, &s3.PutBucketPolicyInput{
			Bucket: aws.String(loggingBucket),
			Policy: aws.String(tlsPolicy),
		}); err != nil {
			return "", "", fmt.Errorf("failed to apply bucket policy on logging bucket: %w", err)
		}

		// Lifecycle rules based on environment
		var lifecycleRules []s3types.LifecycleRule
		if environment == "prod" {
			lifecycleRules = []s3types.LifecycleRule{
				{
					ID:     aws.String("prod-audit-retention"),
					Status: s3types.ExpirationStatusEnabled,
					Filter: &s3types.LifecycleRuleFilter{
						Prefix: aws.String(""),
					},
					Transitions: []s3types.Transition{
						{Days: aws.Int32(90), StorageClass: s3types.TransitionStorageClassStandardIa},
						{Days: aws.Int32(365), StorageClass: s3types.TransitionStorageClassGlacier},
					},
					Expiration: &s3types.LifecycleExpiration{
						Days: aws.Int32(2555), // 7 years
					},
				},
			}
		} else {
			lifecycleRules = []s3types.LifecycleRule{
				{
					ID:     aws.String("nonprod-audit-retention"),
					Status: s3types.ExpirationStatusEnabled,
					Filter: &s3types.LifecycleRuleFilter{
						Prefix: aws.String(""),
					},
					Expiration: &s3types.LifecycleExpiration{
						Days: aws.Int32(30),
					},
				},
			}
		}

		if _, err = s3Client.PutBucketLifecycleConfiguration(ctx, &s3.PutBucketLifecycleConfigurationInput{
			Bucket: aws.String(loggingBucket),
			LifecycleConfiguration: &s3types.BucketLifecycleConfiguration{
				Rules: lifecycleRules,
			},
		}); err != nil {
			return "", "", fmt.Errorf("failed to set lifecycle rules on logging bucket: %w", err)
		}

		printCheck("Logging bucket created")
	} else {
		printCheck("Logging bucket exists")
	}

	// ── CloudTrail ──
	// Check for an existing trail first — CloudTrail is free for one trail per region.
	describeOut, err := ctClient.DescribeTrails(ctx, &cloudtrail.DescribeTrailsInput{
		IncludeShadowTrails: aws.Bool(false),
	})
	if err != nil {
		return "", "", fmt.Errorf("failed to describe CloudTrail trails: %w", err)
	}

	for _, trail := range describeOut.TrailList {
		if trail.TrailARN != nil {
			trailArn = *trail.TrailARN
			printCheck(fmt.Sprintf("Using existing CloudTrail trail: %s", aws.ToString(trail.Name)))
			return loggingBucket, trailArn, nil
		}
	}

	// No existing trail — create one
	trailName := fmt.Sprintf("anvil-trail-%s-%s", accountId, region)
	createTrailOut, err := ctClient.CreateTrail(ctx, &cloudtrail.CreateTrailInput{
		Name:                    aws.String(trailName),
		S3BucketName:            aws.String(loggingBucket),
		S3KeyPrefix:             aws.String("cloudtrail"),
		IsMultiRegionTrail:      aws.Bool(true),
		EnableLogFileValidation: aws.Bool(true),
	})
	if err != nil {
		return "", "", fmt.Errorf("failed to create CloudTrail trail: %w", err)
	}
	trailArn = aws.ToString(createTrailOut.TrailARN)

	// Tag the trail separately
	ctClient.AddTags(ctx, &cloudtrail.AddTagsInput{
		ResourceId: aws.String(trailArn),
		TagsList: []cloudtrailtypes.Tag{
			{Key: aws.String("ManagedBy"), Value: aws.String("anvil")},
		},
	})

	// Management events + S3 data events
	if _, err = ctClient.PutEventSelectors(ctx, &cloudtrail.PutEventSelectorsInput{
		TrailName: aws.String(trailArn),
		EventSelectors: []cloudtrailtypes.EventSelector{
			{
				ReadWriteType:           cloudtrailtypes.ReadWriteTypeAll,
				IncludeManagementEvents: aws.Bool(true),
				DataResources: []cloudtrailtypes.DataResource{
					{
						Type:   aws.String("AWS::S3::Object"),
						Values: []string{"arn:aws:s3"},
					},
				},
			},
		},
	}); err != nil {
		return "", "", fmt.Errorf("failed to configure CloudTrail event selectors: %w", err)
	}

	// Start logging
	if _, err = ctClient.StartLogging(ctx, &cloudtrail.StartLoggingInput{
		Name: aws.String(trailArn),
	}); err != nil {
		return "", "", fmt.Errorf("failed to start CloudTrail logging: %w", err)
	}

	printCheck("CloudTrail trail created")
	return loggingBucket, trailArn, nil
}

// ── Helpers ──

func printCheck(msg string) {
	if isTTY() {
		fmt.Printf("  %s %s\n", green("✔"), msg)
	} else {
		fmt.Printf("  [ok] %s\n", msg)
	}
}

func printWarn(msg string) {
	if isTTY() {
		fmt.Printf("  %s %s\n", yellow("⚠"), msg)
	} else {
		fmt.Printf("  [warn] %s\n", msg)
	}
}

func randomSuffix(length int) (string, error) {
	bytes := make([]byte, length)
	_, err := rand.Read(bytes)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes)[:length], nil
}

func readProjectName() (string, error) {
	config, err := loadAnvilConfig()
	if err != nil {
		return "", fmt.Errorf("Could not find anvil.yaml in this directory.\n  Run `anvil init` to create a project, or cd into an existing one.")
	}

	if config.Project == "" {
		return "", fmt.Errorf("No project name found in anvil.yaml.\n  Add a `project:` field to anvil.yaml.")
	}

	return strings.ReplaceAll(config.Project, " ", "-"), nil
}

func loadAnvilConfig() (*anvilConfig, error) {
	data, err := os.ReadFile("anvil.yaml")
	if err != nil {
		return nil, err
	}

	var config anvilConfig
	err = yaml.Unmarshal(data, &config)
	if err != nil {
		return nil, err
	}

	return &config, nil
}

func writeAnvilConfig(config anvilConfig) error {
	data, err := yaml.Marshal(config)
	if err != nil {
		return err
	}

	return os.WriteFile("anvil.yaml", data, 0644)
}
