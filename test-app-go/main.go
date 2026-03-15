package main

import (
	"github.com/anvil/pulumi-anvil/sdk/go/anvil/aws"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		aws.NewBucket(ctx, "he", &aws.BucketArgs{})
		_, err := aws.NewBucket(ctx, "my-test-bucket", &aws.BucketArgs{
			DataClassification: pulumi.String("sensitive"),
			Lifecycle:          pulumi.Int(90),
		})
		return err
	})
}
