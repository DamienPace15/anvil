package bucket

import (
	"github.com/anvil/pulumi-anvil/internal/transform"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/s3"
	p "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi-go-provider/infer"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// BucketArgs matches the inputProperties in your modular schema.json
type BucketArgs struct {
	DataClassification string                            `pulumi:"dataClassification"`
	Lifecycle          int                               `pulumi:"lifecycle"`
	Transform          map[string]map[string]interface{} `pulumi:"transform,optional"`
}

type Bucket struct {
	pulumi.ResourceState

	BucketName pulumi.StringOutput `pulumi:"bucketName"`
	Arn        pulumi.StringOutput `pulumi:"arn"`
}

// Annotate helps the provider understand the Pulumi token
func (b *Bucket) Annotate(a infer.Annotator) {
	a.SetToken("aws", "Bucket")
	a.Describe(&b, "An Anvil-managed S3 bucket with sensible security defaults.")
}

func NewBucket(ctx *pulumi.Context, name string, args BucketArgs, opts ...pulumi.ResourceOption) (*Bucket, error) {
	b := &Bucket{}
	err := ctx.RegisterComponentResource(p.GetTypeToken(ctx), name, b, opts...)
	if err != nil {
		return nil, err
	}

	isSensitive := args.DataClassification == "sensitive" || args.DataClassification == "restricted"

	// 1. Base Bucket
	bucketProps := transform.MergeTransform(args.Transform["bucket"], pulumi.Map{
		"forceDestroy": pulumi.Bool(true),
		"tags": pulumi.StringMap{
			"ManagedBy":          pulumi.String("anvil"),
			"DataClassification": pulumi.String(args.DataClassification),
		},
	})

	res := &s3.Bucket{}
	err = ctx.RegisterResource("aws:s3/bucket:Bucket", name, bucketProps, res, pulumi.Parent(b))
	if err != nil {
		return nil, err
	}

	// 2. Public Access Block
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

	// 3. Versioning
	if vTransform, ok := args.Transform["versioning"]; ok || isSensitive {
		vProps := transform.MergeTransform(vTransform, pulumi.Map{
			"bucket": res.ID(),
			"versioningConfiguration": pulumi.Map{
				"status": pulumi.String("Enabled"),
			},
		})
		err = ctx.RegisterResource("aws:s3/bucketVersioningV2:BucketVersioningV2", name+"-ver", vProps, &s3.BucketVersioningV2{}, pulumi.Parent(b))
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
