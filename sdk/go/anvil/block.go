package anvil

import (
	"fmt"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
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
// Blocks are purely optional — flat top-level resources work identically.
//
// Example:
//
//	type Storage struct {
//	    anvil.Block
//	    BucketName pulumi.StringOutput
//	}
//
//	func NewStorage(ctx *pulumi.Context, name string, opts ...pulumi.ResourceOption) (*Storage, error) {
//	    s := &Storage{}
//	    err := ctx.RegisterComponentResource("anvil:block:"+name, name, s, opts...)
//	    if err != nil {
//	        return nil, err
//	    }
//
//	    bucket, err := aws.NewBucket(ctx, "data", &aws.BucketArgs{...}, s.Opts()...)
//	    if err != nil {
//	        return nil, err
//	    }
//	    s.BucketName = bucket.BucketName
//
//	    ctx.RegisterResourceOutputs(s, pulumi.Map{"bucketName": s.BucketName})
//	    return s, nil
//	}
type Block struct {
	pulumi.ResourceState
}

// TypeName returns the Pulumi type token for this Block.
func TypeName(name string) string {
	return fmt.Sprintf("anvil:block:%s", name)
}

// Opts returns a slice of ResourceOption that parents child resources to this Block.
// Pass the result when creating resources inside the Block:
//
//	bucket, err := aws.NewBucket(ctx, "data", &aws.BucketArgs{}, block.Opts()...)
func (b *Block) Opts(extra ...pulumi.ResourceOption) []pulumi.ResourceOption {
	return append([]pulumi.ResourceOption{pulumi.Parent(b)}, extra...)
}
