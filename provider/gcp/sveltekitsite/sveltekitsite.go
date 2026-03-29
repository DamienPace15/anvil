package gcp

import (
	"fmt"

	"github.com/DamienPace15/anvil/provider/internal/sites"
	"github.com/pulumi/pulumi-go-provider/infer"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	p "github.com/pulumi/pulumi-go-provider"
)

// SvelteKitSiteArgs defines the inputs for a GCP SvelteKit site deployment.
// Shared inputs are embedded from the framework layer.
//
//anvil:inputs-from sites.SvelteKitSiteInputs
type SvelteKitSiteArgs struct {
	sites.SvelteKitSiteInputs

	// Minimum number of Cloud Run instances to keep warm.
	// Set to 1 or higher to eliminate cold starts. Defaults to 0.
	MinInstances int `pulumi:"minInstances,optional"`
}

// SvelteKitSite is an Anvil-managed SvelteKit deployment on GCP.
// Static assets go to Cloud Storage behind Cloud CDN. Server code runs on Cloud Run.
//
//anvil:outputs-from sites.SvelteKitSiteOutputs
type SvelteKitSite struct {
	pulumi.ResourceState

	sites.SvelteKitSiteOutputs

	// The Cloud Run service URL.
	CloudRunURL pulumi.StringOutput `pulumi:"cloudRunUrl"`

	// The Cloud Storage bucket name storing static assets.
	BucketName pulumi.StringOutput `pulumi:"bucketName"`
}

func (s *SvelteKitSite) Annotate(a infer.Annotator) {
	a.Describe(&s, "An Anvil-managed SvelteKit site deployed on GCP. "+
		"Static assets are served from Cloud Storage via Cloud CDN. "+
		"Server-side rendering runs on Cloud Run.")
}

// TODO: Implement NewSvelteKitSite once the provider layer (Cloud Storage + Cloud CDN + Cloud Run wiring) is built.
// func NewSvelteKitSite(ctx *pulumi.Context, name string, args SvelteKitSiteArgs, opts ...pulumi.ResourceOption) (*SvelteKitSite, error) {
// 	// 1. Call sites.BuildSvelteKit() to get BuildResult
// 	// 2. Create Cloud Storage bucket, upload StaticDir
// 	// 3. Create Cloud Run service from ServerDir/ServerEntry
// 	// 4. Create external HTTPS load balancer + Cloud CDN → Cloud Storage + Cloud Run
// 	// 5. Optional: Cloud DNS + managed SSL for Domain
// 	// 6. RegisterResourceOutputs
// }

func NewSvelteKitSite(ctx *pulumi.Context, name string, args SvelteKitSiteArgs, opts ...pulumi.ResourceOption) (*SvelteKitSite, error) {
	s := &SvelteKitSite{}
	err := ctx.RegisterComponentResource(p.GetTypeToken(ctx), name, s, opts...)
	if err != nil {
		return nil, err
	}

	return s, fmt.Errorf("anvil:gcp:SvelteKitSite is not yet implemented")
}
