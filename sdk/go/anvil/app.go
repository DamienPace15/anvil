package anvil

import (
	"fmt"

	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws"
	"github.com/pulumi/pulumi-gcp/sdk/v9/go/gcp"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

// ── ComplianceFramework ────────────────────────────────────

// ComplianceFramework represents a supported compliance standard.
// Mirrors the Go constants in provider/internal/shared/compliance.go.
type ComplianceFramework string

const (
	ComplianceSOC2     ComplianceFramework = "soc2"
	ComplianceISO27001 ComplianceFramework = "iso27001"
	ComplianceCIS      ComplianceFramework = "cis"
	CompliancePCIDSS   ComplianceFramework = "pci-dss"
	ComplianceHIPAA    ComplianceFramework = "hipaa"
	ComplianceFedRAMP  ComplianceFramework = "fedramp"
	ComplianceHITRUST  ComplianceFramework = "hitrust"
	ComplianceGDPR     ComplianceFramework = "gdpr"
	ComplianceSOC1     ComplianceFramework = "soc1"
	ComplianceIRAP     ComplianceFramework = "irap"
	ComplianceNISTCSF  ComplianceFramework = "nist-csf"
	ComplianceCSASTAR  ComplianceFramework = "csa-star"
)

// ── Context ────────────────────────────────────────────────

// Context is passed to the App's Run callback.
// It wraps pulumi.Context with Anvil-specific information.
type Context struct {
	ctx *pulumi.Context

	// Stage is the current deployment stage.
	Stage string

	// Project is the project name from anvil.yaml.
	Project string

	// Environment is "prod" or "nonprod".
	Environment string

	// IsProduction is true when environment is "prod".
	IsProduction bool

	// Providers holds named providers keyed by config name (e.g. "aws", "aws.us", "gcp").
	Providers map[string]pulumi.ProviderResource

	// Compliance holds the app-level compliance frameworks applied to all resources.
	// Pass this to resource args to inherit app defaults at the component level.
	//
	// Example:
	//   _, err := anvilaws.NewBucket(ctx.PulumiCtx(), "data", &anvilaws.BucketArgs{
	//       DataClassification: pulumi.String("sensitive"),
	//       Compliance:         ctx.Compliance,
	//   }, ctx.Provider("aws"))
	Compliance []ComplianceFramework
}

// PulumiCtx returns the underlying pulumi.Context.
func (c *Context) PulumiCtx() *pulumi.Context {
	return c.ctx
}

// Export exports a stack output value.
func (c *Context) Export(name string, value pulumi.Input) {
	c.ctx.Export(name, value)
}

// Provider returns the pulumi.Provider ResourceOption for a named provider.
// Use this when creating resources to attach the correct provider:
//
//	anvilaws.NewBucket(ctx.PulumiCtx(), "data", &args, ctx.Provider("aws"))
func (c *Context) Provider(name string) pulumi.ResourceOption {
	if p, ok := c.Providers[name]; ok {
		return pulumi.Provider(p)
	}
	return pulumi.Provider(nil)
}

// ── Config types ───────────────────────────────────────────

// AwsProviderConfig configures an AWS provider.
type AwsProviderConfig struct {
	Region  string
	Profile string
}

// GcpProviderConfig configures a GCP provider.
type GcpProviderConfig struct {
	Project     string
	Region      string
	Zone        string
	Credentials string
}

// DefaultsConfig holds default options applied to all resources.
type DefaultsConfig struct {
	// Tags merged into every taggable resource via defaultTags (AWS) / defaultLabels (GCP).
	// "stage" and "project" are auto-injected. User tags override auto-injected ones.
	Tags map[string]string

	// Compliance frameworks applied to all resources by default.
	// Pass ctx.Compliance to any resource args to inherit these at the component level.
	// Components extend app defaults — they never replace them.
	//
	// Example:
	//   Defaults: &anvil.DefaultsConfig{
	//       Compliance: []anvil.ComplianceFramework{
	//           anvil.ComplianceSOC2,
	//           anvil.ComplianceISO27001,
	//       },
	//   },
	Compliance []ComplianceFramework
}

// AppConfig is the configuration for the App.
type AppConfig struct {
	// Run is the infrastructure definition callback. Required.
	Run func(ctx *Context) error

	// Defaults holds default resource options.
	Defaults *DefaultsConfig

	// AwsProviders configures AWS providers.
	// Keys: "aws" (default), "aws.us" (named).
	AwsProviders map[string]*AwsProviderConfig

	// GcpProviders configures GCP providers.
	// Keys: "gcp" (default), "gcp.eu" (named).
	GcpProviders map[string]*GcpProviderConfig
}

// ── Run ────────────────────────────────────────────────────

// Run is the entry point for an Anvil infrastructure program.
// It wraps pulumi.Run so users never call it directly.
//
// Example:
//
//	func main() {
//	    anvil.Run(anvil.AppConfig{
//	        Defaults: &anvil.DefaultsConfig{
//	            Compliance: []anvil.ComplianceFramework{
//	                anvil.ComplianceSOC2,
//	                anvil.ComplianceISO27001,
//	            },
//	        },
//	        AwsProviders: map[string]*anvil.AwsProviderConfig{
//	            "aws": {Region: "ap-southeast-2"},
//	        },
//	        Run: func(ctx *anvil.Context) error {
//	            _, err := anvilaws.NewBucket(ctx.PulumiCtx(), "data", &anvilaws.BucketArgs{
//	                DataClassification: pulumi.String("sensitive"),
//	                Compliance:         ctx.Compliance,
//	            }, ctx.Provider("aws"))
//	            return err
//	        },
//	    })
//	}
func Run(appConfig AppConfig) {
	pulumi.Run(func(ctx *pulumi.Context) error {
		// ── Read config from Pulumi ────────────────────────
		cfg := config.New(ctx, "anvil")
		stage := cfg.Require("stage")
		environment := cfg.Require("environment")

		// ── Build default tags ─────────────────────────────
		autoTags := map[string]string{
			"stage":   stage,
			"project": ctx.Project(),
		}
		if appConfig.Defaults != nil && appConfig.Defaults.Tags != nil {
			for k, v := range appConfig.Defaults.Tags {
				autoTags[k] = v
			}
		}

		pulumiTags := pulumi.StringMap{}
		for k, v := range autoTags {
			pulumiTags[k] = pulumi.String(v)
		}

		// ── Resolve app-level compliance ───────────────────
		var appCompliance []ComplianceFramework
		if appConfig.Defaults != nil {
			appCompliance = appConfig.Defaults.Compliance
		}

		// ── Create providers ───────────────────────────────
		providers := map[string]pulumi.ProviderResource{}

		// AWS providers
		for key, providerCfg := range appConfig.AwsProviders {
			providerName := fmt.Sprintf("anvil-provider-%s", key)

			awsArgs := &aws.ProviderArgs{
				DefaultTags: &aws.ProviderDefaultTagsArgs{
					Tags: pulumiTags,
				},
			}
			if providerCfg.Region != "" {
				awsArgs.Region = pulumi.StringPtr(providerCfg.Region)
			}
			if providerCfg.Profile != "" {
				awsArgs.Profile = pulumi.StringPtr(providerCfg.Profile)
			}

			provider, err := aws.NewProvider(ctx, providerName, awsArgs)
			if err != nil {
				return fmt.Errorf("failed to create AWS provider %q: %w", key, err)
			}

			providers[key] = provider
		}

		// GCP providers
		for key, providerCfg := range appConfig.GcpProviders {
			providerName := fmt.Sprintf("anvil-provider-%s", key)

			gcpArgs := &gcp.ProviderArgs{
				DefaultLabels: pulumiTags,
			}
			if providerCfg.Project != "" {
				gcpArgs.Project = pulumi.StringPtr(providerCfg.Project)
			}
			if providerCfg.Region != "" {
				gcpArgs.Region = pulumi.StringPtr(providerCfg.Region)
			}
			if providerCfg.Zone != "" {
				gcpArgs.Zone = pulumi.StringPtr(providerCfg.Zone)
			}
			if providerCfg.Credentials != "" {
				gcpArgs.Credentials = pulumi.StringPtr(providerCfg.Credentials)
			}

			provider, err := gcp.NewProvider(ctx, providerName, gcpArgs)
			if err != nil {
				return fmt.Errorf("failed to create GCP provider %q: %w", key, err)
			}

			providers[key] = provider
		}

		// ── Create Context ─────────────────────────────────
		anvilCtx := &Context{
			ctx:          ctx,
			Stage:        stage,
			Project:      ctx.Project(),
			Environment:  environment,
			IsProduction: environment == "prod",
			Providers:    providers,
			Compliance:   appCompliance,
		}

		// ── Execute ────────────────────────────────────────
		return appConfig.Run(anvilCtx)
	})
}
