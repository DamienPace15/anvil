# `registry/` — generate the provider's component registration

> **Pipeline stage 4** (see [`scripts/README.md`](../README.md)). `generate_registry.go`.
>
> | | |
> |---|---|
> | **Runs from** | `provider/` (cwd) |
> | **Reads** | every `provider/<cloud>/<resource>/*.go` (to find component constructors) + the version (CLI arg, falling back to `base-schema.json`) |
> | **Writes** | `provider/cmd/anvil/main.go` (marked `// Code generated … DO NOT EDIT`) |
> | **Invoked by** | `go run build.go registry` (and the provider build) |
> | **Why** | the provider binary must register every component and report a version; doing it by hand drifts as components are added |

## What it does

The Anvil provider binary (`pulumi-resource-anvil`) is a Go program whose `main()`
registers every component with Pulumi's `infer` framework and then calls
`p.Run(ctx, "anvil", <version>)`. This script **generates that `main.go`** so it
always matches the components on disk and the canonical version.

## How it works

1. **Auto-discovery.** Scans `provider/<cloud>/<resource>/` folders (skipping
   `cmd`, `scripts`, `sdk`, `internal`, `sites`).
2. **Constructor detection.** In each folder it looks for a function matching
   `New[A-Z]\w*(` (the Pulumi component convention, e.g. `NewBucket`,
   `NewLambda`). That constructor + its import path become a registry entry.
3. **Template render.** A `text/template` emits `provider/cmd/anvil/main.go`:
   the imports, the `infer.ComponentF(<pkg>.New<X>)` registrations, and
   `p.Run(ctx, "anvil", "<version>")`.
4. **Version baking.** The version comes from `resolveVersion()`:
   - `os.Args[1]` if `build.go` passed it (the normal path — `build.go` reads
     `provider/base-schema.json` once and passes it in), else
   - read `base-schema.json` directly (so the script still works run by hand).

   This is why the provider reports the same version as the SDKs: there is **one**
   source (`base-schema.json`), threaded through here.

## Adding a new component

1. Create `provider/<cloud>/<resource>/` with a `New<Name>(ctx, name, args, opts)`
   constructor and a `schema.json`.
2. Run any build. The component is auto-discovered, registered in `main.go`, and
   merged into the schema — no manual edits.

## Gotchas

- **Constructor must be named `New<Pascal>`** and return `(*T, error)`. A
  differently-named constructor is silently skipped (the component won't register).
- **`provider/cmd/anvil/main.go` is generated** (`DO NOT EDIT`). Change behavior
  by editing the template in `generate_registry.go`, not the output.
- **Version is not hardcoded here anymore.** If you need to bump it, edit
  `provider/base-schema.json` — `build.go` passes it in.
