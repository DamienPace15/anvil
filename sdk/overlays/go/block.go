package anvil

import (
	"fmt"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

// BlockArgs holds optional arguments for a Block.
// Embed this in your own args struct if you need custom fields.
type BlockArgs struct{}

// Block is an optional organisational grouping for Anvil resources.
//
// Unlike TypeScript and Python, Go does not have Pulumi auto-parenting.
// Use block.Opts() when creating child resources to pass the parent
// relationship explicitly.
//
// Stage and Project are automatically populated from Pulumi config —
// the same config that App sets during initialisation.
//
// Tagging is automatic — the App's provider injection applies defaultTags
// to all resources, including those inside Blocks.
//
// Blocks are purely optional — flat top-level resources work identically.
//
// Example:
//
//	type RulesEngine struct {
//	    anvil.Block
//	}
//
//	func NewRulesEngine(ctx *pulumi.Context, name string, opts ...pulumi.ResourceOption) (*RulesEngine, error) {
//	    r := &RulesEngine{}
//	    err := ctx.RegisterComponentResource(anvil.TypeName("RulesEngine"), name, r, opts...)
//	    if err != nil {
//	        return nil, err
//	    }
//	    // r.Stage and r.Project are now available
//
//	    bucket, err := aws.NewBucket(ctx, "events", &aws.BucketArgs{
//	        DataClassification: pulumi.String("internal"),
//	    }, r.Opts()...)
//	    if err != nil {
//	        return nil, err
//	    }
//
//	    ctx.RegisterResourceOutputs(r, pulumi.Map{})
//	    return r, nil
//	}
type Block struct {
	pulumi.ResourceState

	// Stage is the current deployment stage (e.g. "dev", "staging", "prod", or OS username).
	Stage string

	// Project is the project name from anvil.yaml.
	Project string
}

// TypeName returns the Pulumi type token for a Block.
// Pass your struct name to keep URNs consistent: anvil.TypeName("RulesEngine")
func TypeName(name string) string {
	return fmt.Sprintf("anvil:block:%s", name)
}

// InitBlock reads stage and project from Pulumi config and populates
// the Block fields. Call this after RegisterComponentResource:
//
//	err := ctx.RegisterComponentResource(anvil.TypeName("RulesEngine"), name, r, opts...)
//	if err != nil { return nil, err }
//	r.InitBlock(ctx)
func (b *Block) InitBlock(ctx *pulumi.Context) {
	cfg := config.New(ctx, "anvil")
	b.Stage = cfg.Require("stage")
	b.Project = ctx.Project()
}

// Opts returns a slice of ResourceOption that parents child resources to this Block.
// Pass the result when creating resources inside the Block:
//
//	bucket, err := aws.NewBucket(ctx, "events", &aws.BucketArgs{}, block.Opts()...)
func (b *Block) Opts(extra ...pulumi.ResourceOption) []pulumi.ResourceOption {
	return append([]pulumi.ResourceOption{pulumi.Parent(b)}, extra...)
}

// RegisterBlock registers a Block as a ComponentResource and initialises
// its Stage, Project, and Environment fields from Pulumi config.
//
// Usage:
//
//	s := &Storage{}
//	if err := ctx.RegisterBlock("Storage", name, &s.Block, opts...); err != nil {
//	    return nil, err
//	}
func (c *Context) RegisterBlock(typeName string, name string, block *Block, opts ...pulumi.ResourceOption) error {
	err := c.ctx.RegisterComponentResource(TypeName(typeName), name, block, opts...)
	if err != nil {
		return err
	}
	block.InitBlock(c.ctx)
	return nil
}
