# @anvil-cloud/sdk

**Cloud infrastructure that's secure by default — not by accident.**

Anvil wraps raw cloud resources into opinionated, production-ready components. No 200-line Terraform modules. No copy-pasting security configs from Stack Overflow. Just declare what you need and Anvil handles the rest.

```typescript
import * as anvil from '@anvil-cloud/sdk';

const bucket = new anvil.aws.Bucket('uploads', {
  versioning: true,
});
```

That S3 bucket ships with public access blocked, encryption enabled, and ACLs disabled — because that's how every bucket should start. You opt _in_ to risk, not out of it.

## Multi-cloud

Anvil supports any cloud provider. AWS and GCP are available today, with more coming soon.

## Quick start

**1. Install the CLI**

```bash
brew install anvil-cloud/tap/anvil
```

**2. Install the SDK**

```bash
npm install @anvil-cloud/sdk
```

**3. Write infrastructure**

```typescript
// index.ts
import * as anvil from '@anvil-cloud/sdk';

const bucket = new anvil.aws.Bucket('uploads');
```

**4. Deploy**

```bash
anvil deploy
```

## How it works

Anvil is built on top of [Pulumi](https://www.pulumi.com/). Each component wraps one or more raw cloud resources with secure defaults baked in. You write TypeScript, Anvil handles the wiring.

## Links

- [GitHub](https://github.com/anvil-cloud/anvil)

## License

Apache-2.0
