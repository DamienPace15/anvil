package provider

import (
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

// Context holds Anvil runtime information available to all components.
// It reads from the same Pulumi config that the App class sets.
type Context struct {
	// Stage is the current deployment stage (e.g. "dev", "staging", "prod").
	Stage string

	// Project is the project name from anvil.yaml.
	Project string

	Environment string // "prod" or "nonprod"

}

// NewContext reads Anvil context from Pulumi config.
// Call once per component constructor.
func NewContext(ctx *pulumi.Context) Context {
	cfg := config.New(ctx, "anvil")

	return Context{
		Stage:       cfg.Require("stage"),
		Project:     ctx.Project(),
		Environment: cfg.Require("environment"),
	}
}

// WithDefaultProtect prepends pulumi.Protect(value) to opts.
// User-supplied opts come after and override via last-write-wins.
func WithDefault(opts []pulumi.ResourceOption, value bool) []pulumi.ResourceOption {
	return append([]pulumi.ResourceOption{pulumi.Protect(value)}, opts...)
}
