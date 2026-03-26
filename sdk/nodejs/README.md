# @anvil-cloud/sdk

**Cloud infrastructure that's secure by default — not by accident.**

Anvil wraps raw cloud resources into opinionated, production-ready components. No 200-line Terraform modules. No copy-pasting security configs. Just declare what you need and Anvil handles the rest.

## Install

```bash
npm install @anvil-cloud/sdk
```

## Quick start

Create `anvil.config.ts` at your project root:

```typescript
import { App } from '@anvil-cloud/sdk';
import * as anvil from '@anvil-cloud/sdk';

export default new App({
  run(ctx) {
    const bucket = new anvil.aws.Bucket('uploads', {
      dataClassification: 'sensitive',
    });
    ctx.export('bucketName', bucket.bucketName);
  },
});
```

Deploy:

```bash
anvil deploy
```

That S3 bucket ships with public access blocked, encryption enabled, and versioning on — because that's how every bucket should start. You opt _in_ to risk, not out of it.

## The App class

Every Anvil program starts with `new App()`. The `run` callback receives a `Context` with:

- `ctx.stage` — the current deployment stage (defaults to your OS username for dev isolation)
- `ctx.project` — the project name from `anvil.yaml`
- `ctx.export(name, value)` — export stack outputs
- `ctx.providers` — named cloud providers for multi-region / multi-account

## Multi-cloud

```typescript
export default new App({
  run(ctx) {
    // AWS
    const bucket = new anvil.aws.Bucket('data', {
      dataClassification: 'sensitive',
    });

    // GCP
    const gcsBucket = new anvil.gcp.StorageBucket('backup', {
      dataClassification: 'internal',
      location: 'US',
    });
  },
});
```

## Overrides

Every component accepts a `transform` argument to override the underlying resource config:

```typescript
const bucket = new anvil.aws.Bucket('custom', {
  dataClassification: 'non-sensitive',
  transform: {
    bucket: { forceDestroy: true, tags: { env: 'dev' } },
  },
});
```

## How it works

Anvil is built on [Pulumi](https://www.pulumi.com/). Each component wraps one or more cloud resources with secure defaults. The `App` class handles config, providers, and exports so you write infrastructure — not boilerplate.

## Links

- [GitHub](https://github.com/DamienPace15/anvil)
- [Python SDK](https://pypi.org/project/anvil-cloud/)
- [Go SDK](https://pkg.go.dev/github.com/DamienPace15/anvil/sdk/go/anvil)

## License

Apache-2.0
