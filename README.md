# Anvil

[![Release](https://img.shields.io/github/v/release/DamienPace15/anvil?style=flat-square)](https://github.com/DamienPace15/anvil/releases)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue?style=flat-square)](LICENSE)

Secure-by-default cloud infrastructure. No boilerplate.

## Quick start

**1. Install**

```sh
curl -fsSL https://raw.githubusercontent.com/DamienPace15/anvil/master/install.sh | sh
```

**2. Create a project**

```sh
mkdir my-app && cd my-app
echo "project: my-app" > anvil.yaml
npm install @anvil-cloud/sdk
```

**3. Write infrastructure** — `anvil.config.ts`

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

That S3 bucket ships with public access blocked, encryption enabled, and versioning on — because that's how every bucket should start.

**4. Deploy**

```sh
anvil deploy
```

## Multi-language

<table>
<tr><td><strong>TypeScript</strong></td><td><code>npm install @anvil-cloud/sdk</code></td></tr>
<tr><td><strong>Python</strong></td><td><code>pip install anvil-cloud</code></td></tr>
<tr><td><strong>Go</strong></td><td><code>go get github.com/DamienPace15/anvil/sdk/go/anvil</code></td></tr>
</table>

**Python** — `anvil.config.py`

```python
import anvil_cloud as anvil

def infra(ctx: anvil.Context):
    bucket = anvil.aws.Bucket("uploads",
        data_classification="sensitive",
    )
    ctx.export("bucketName", bucket.bucket_name)

anvil.App(run=infra)
```

**Go** — `main.go`

```go
package main

import (
    "github.com/DamienPace15/anvil/sdk/go/anvil"
    anvilaws "github.com/DamienPace15/anvil/sdk/go/anvil/aws"
    "github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func main() {
    anvil.Run(anvil.AppConfig{
        Run: func(ctx *anvil.Context) error {
            _, err := anvilaws.NewBucket(ctx.PulumiCtx(), "uploads", &anvilaws.BucketArgs{
                DataClassification: pulumi.String("sensitive"),
            })
            return err
        },
    })
}
```

## Multi-cloud

AWS and GCP today. More coming.

## Install

**macOS / Linux**

```sh
curl -fsSL https://raw.githubusercontent.com/DamienPace15/anvil/master/install.sh | sh
```

**Windows (PowerShell)**

```powershell
irm https://raw.githubusercontent.com/DamienPace15/anvil/master/install.ps1 | iex
```

## Local development

Prerequisites: Go 1.22+, Node.js 18+, Pulumi CLI.

```sh
git clone https://github.com/DamienPace15/anvil.git
cd anvil
make build
```

Add `bin/` to your PATH to use the local provider:

```sh
export PATH="$PATH:$(pwd)/bin"
```

### Build commands

| Command               | What it does                                                |
| --------------------- | ----------------------------------------------------------- |
| `make build`          | Full pipeline: generate → merge → registry → compile → SDKs |
| `make binary`         | CLI binary only (fast, for CLI-only changes)                |
| `make build-provider` | Compile the provider binary                                 |
| `make build-sdk`      | Generate + build the Node.js SDK                            |
| `make gen-python-sdk` | Generate Python SDK                                         |
| `make clean`          | Remove build artifacts                                      |

## Docs

[anvilcloud.dev](https://anvilcloud.dev)

## Contributing

PRs welcome. Open an issue first for larger changes.

## License

Apache-2.0
