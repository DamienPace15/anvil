# Anvil — Python SDK

Secure-by-default cloud infrastructure components for [Pulumi](https://www.pulumi.com/).

Anvil wraps raw cloud resources with opinionated, security-hardened defaults so you ship infrastructure that's secure from day one — without the boilerplate.

## Install

```bash
pip install anvil-cloud
```

## Quick start

```python
import anvil_cloud as anvil

# Create an S3 bucket with encryption, versioning, and public access
# blocked by default — no 30-line config required.
bucket = anvil.aws.Bucket("my-data",
    data_classification="sensitive",
    lifecycle=90,
)
```

```python
# GCP Cloud Storage with uniform bucket-level access
gcs = anvil.gcp.StorageBucket("analytics",
    data_classification="internal",
    location="US",
)
```

## Overrides

Every component accepts a `transform` argument to override or extend the underlying resource configuration when the defaults don't fit:

```python
import anvil_cloud as anvil

bucket = anvil.aws.Bucket("custom",
    data_classification="public",
    transform=anvil.aws.BucketTransformArgsArgs(
        overrides=anvil.aws.BucketOverridesArgs(
            force_destroy=True,
            tags={"env": "dev"},
        ),
    ),
)
```

## Requirements

- Python >= 3.8
- Pulumi >= 3.0.0
- The `pulumi-resource-anvil` provider binary (installed via `anvil` CLI or manually)

## Links

- [GitHub](https://github.com/anvil-cloud/anvil)
- [npm (Node SDK)](https://www.npmjs.com/package/@anvil-cloud/sdk)
- [Go SDK](https://pkg.go.dev/github.com/DamienPace15/anvil/sdk/go/anvil)

## License

Apache-2.0
