package sites

import "github.com/pulumi/pulumi/sdk/v3/go/pulumi"

// SvelteKitSiteInputs defines the shared inputs for all SvelteKit site components.
// These inputs are identical across providers (AWS, GCP). Provider-specific options
// live on the provider component's own args struct, not here.
//
// The schema generator reads this struct to produce the inputProperties section
// of each provider's schema.json.
type SvelteKitSiteInputs struct {
	// Path to the SvelteKit project directory, relative to the project root
	// (where anvil.yaml lives). Not relative to the anvil.config file.
	Path string `pulumi:"path" schema:"required"`

	// Environment variables injected at both build time and runtime.
	// Build time: available via $env/static and import.meta.env during
	// static generation and prerendering.
	// Runtime: set on the compute service (Lambda/Cloud Run) for SSR
	// and API routes via $env/dynamic.
	Environment map[string]string `pulumi:"environment,optional"`

	// Custom domain name for the site. When set, Anvil configures DNS
	// and TLS certificates automatically.
	Domain string `pulumi:"domain,optional"`
}

// SvelteKitSiteOutputs defines the shared outputs for all SvelteKit site components.
// Provider-specific outputs (e.g. CloudFront distribution ID) live on the
// provider component's own struct.
type SvelteKitSiteOutputs struct {
	// The URL where the site is accessible.
	URL pulumi.StringOutput `pulumi:"url"`
}
