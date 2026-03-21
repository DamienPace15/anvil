package main

import (
	"github.com/DamienPace15/anvil/sdk/go/anvil/aws"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		_, err := aws.NewBucket(ctx, "my-aws-bucket", &aws.BucketArgs{
			DataClassification: pulumi.String("sensitive"),
			Lifecycle:          pulumi.Int(90),
		})
		if err != nil {
			return err
		}

		return err
	})
}
