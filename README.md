# Anvil

[![Release](https://img.shields.io/github/v/release/DamienPace15/anvil?style=flat-square)](https://github.com/DamienPace15/anvil/releases)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue?style=flat-square)](LICENSE)

Secure-by-default cloud infrastructure. No boilerplate.

## Install

**macOS / Linux**

```sh
curl -fsSL https://raw.githubusercontent.com/DamienPace15/anvil/master/install.sh | sh
```

**Windows (PowerShell)**

```powershell
irm https://raw.githubusercontent.com/DamienPace15/anvil/master/install.ps1 | iex
```

## SDKs

- **TypeScript / JavaScript:** `npm install @anvil-cloud/sdk`
- **Python:** `pip install anvil-cloud`
- **Go:** `go get github.com/DamienPace15/anvil/sdk/go/anvil`

## Local Development

Prerequisites: Go 1.22+, Node.js 18+, Pulumi CLI.

```sh
git clone https://github.com/DamienPace15/anvil.git
cd anvil
make build
```

This builds everything — schemas, provider binary, and SDKs. To test locally, add the `bin/` directory to your PATH:

```sh
export PATH="$PATH:$(pwd)/bin"
anvil --help
```

### Build Commands

| Command               | What it does                                                |
| --------------------- | ----------------------------------------------------------- |
| `make build`          | Full pipeline: generate → merge → registry → compile → SDKs |
| `make generate`       | Fetch upstream schemas and enrich component schemas         |
| `make merge`          | Merge all component schemas into `provider/schema.json`     |
| `make registry`       | Generate provider entrypoint from component registrations   |
| `make build-provider` | Compile the provider binary to `bin/pulumi-resource-anvil`  |
| `make gen-go-sdk`     | Generate Go SDK                                             |
| `make gen-nodejs`     | Generate Node.js SDK                                        |
| `make gen-python-sdk` | Generate Python SDK                                         |
| `make clean`          | Remove build artifacts                                      |

## Docs

[anvilcloud.dev](https://anvilcloud.dev)

## Contributing

PRs welcome. Open an issue first for larger changes.

## License

Apache-2.0
