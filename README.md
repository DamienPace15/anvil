# Anvil

[![Release](https://img.shields.io/github/v/release/DamienPace15/anvil?style=flat-square)](https://github.com/DamienPace15/anvil/releases)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue?style=flat-square)](LICENSE)

Cloud infrastructure with secure, cost-aware defaults built in. No boilerplate, no shortcuts.

Anvil is built on top of [Pulumi](https://pulumi.com) and lets you write infrastructure in TypeScript, Python, or Go.

---

## Why Anvil

Most IaC tools give you primitives and leave the hard decisions to you. Encryption, access controls, cost tagging, compliance-aligned configuration — that's all on you to remember, every time, across every resource.

Anvil flips that. Every component ships with defaults that:

- **Block public access and enforce encryption** across storage, compute, and networking
- **Enforce tagging** so costs are attributable from day one
- **Align to SOC 2 and ISO 27001 controls** — not certified out of the box, but configured in a way that does the heavy lifting when you're building toward compliance

You still own your compliance posture. Anvil just makes sure you're not starting from zero.

---

## Quick start

**Install**

```sh
curl -fsSL https://raw.githubusercontent.com/DamienPace15/anvil/master/install.sh | sh
```

**Windows (PowerShell)**

```powershell
irm https://raw.githubusercontent.com/DamienPace15/anvil/master/install.ps1 | iex
```

---

## Multi-language

Full docs and examples at [anvilcloud.dev](https://anvilcloud.dev).

<table>
<tr><td><strong>TypeScript</strong></td><td><code>npm install @anvil-cloud/sdk</code></td></tr>
<tr><td><strong>Python</strong></td><td><code>pip install anvil-cloud</code></td></tr>
<tr><td><strong>Go</strong></td><td><code>go get github.com/DamienPace15/anvil/sdk/go/anvil</code></td></tr>
</table>

---

## Multi-cloud

AWS and GCP today. More coming.

---

## Local development

**Prerequisites:** Go 1.22+, Node.js 18+, Pulumi CLI

```sh
git clone https://github.com/DamienPace15/anvil.git
cd anvil
go run build.go build
```

Add `bin/` to your PATH to use the local provider:

```sh
export PATH="$PATH:$(pwd)/bin"
```

### Build commands

| Command                          | What it does                                                |
| -------------------------------- | ----------------------------------------------------------- |
| `go run build.go build`          | Full pipeline: generate → merge → registry → compile → SDKs |
| `go run build.go binary`         | CLI binary only (fast, for CLI-only changes)                |
| `go run build.go build-provider` | Compile the provider binary                                 |
| `go run build.go build-sdk`      | Generate + build the Node.js SDK                            |
| `go run build.go gen-python-sdk` | Generate Python SDK                                         |
| `go run build.go clean`          | Remove build artifacts                                      |

---

## Docs

[anvilcloud.dev](https://anvilcloud.dev)

---

## Contributing

PRs welcome. Open an issue first for larger changes.

## License

Apache-2.0
