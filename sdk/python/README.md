# anvil-cloud

**Cloud infrastructure that's secure by default — not by accident.**

Anvil wraps raw cloud resources into opinionated, production-ready components. No boilerplate. No copy-pasting security configs. Just declare what you need.

## Install

```bash
pip install anvil-cloud
```

## Quick start

Create `anvil.config.py` at your project root:

```python
import anvil_cloud as anvil

def infra(ctx: anvil.Context):
    bucket = anvil.aws.Bucket("uploads",
        data_classification="sensitive",
    )
    ctx.export("bucketName", bucket.bucket_name)

anvil.App(run=infra)
```

Deploy:

```bash
anvil deploy
```

That S3 bucket ships with public access blocked, encryption enabled, and versioning on — because that's how every bucket should start.

## The App class

Every Anvil program starts with `anvil.App()`. The `run` callback receives a `Context` with:

- `ctx.stage` — the current deployment stage (defaults to your OS username for dev isolation)
- `ctx.project` — the project name from `anvil.yaml`
- `ctx.export(name, value)` — export stack outputs
- `ctx.providers` — named cloud providers for multi-region / multi-account

## Multi-cloud

```python
import anvil_cloud as anvil

def infra(ctx: anvil.Context):
    # AWS
    bucket = anvil.aws.Bucket("data",
        data_classification="sensitive",
    )

    # GCP
    gcs = anvil.gcp.StorageBucket("backup",
        data_classification="internal",
        location="US",
    )

anvil.App(run=infra)
```

## Overrides

Every component accepts a `transform` argument to override the underlying resource config:

```python
bucket = anvil.aws.Bucket("custom",
    data_classification="non-sensitive",
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
- The Anvil CLI (`curl -fsSL https://raw.githubusercontent.com/DamienPace15/anvil/master/install.sh | sh`)

## Links

- [GitHub](https://github.com/DamienPace15/anvil)
- [npm (Node SDK)](https://www.npmjs.com/package/@anvil-cloud/sdk)
- [Go SDK](https://pkg.go.dev/github.com/DamienPace15/anvil/sdk/go/anvil)

## License

Apache-2.0
