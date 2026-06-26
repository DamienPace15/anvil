# `sdk/` — post-process the generated SDKs (packaging + wiring)

> **Pipeline stage 7** (see [`scripts/README.md`](../README.md)). `fix-sdk.ts` (run via `ts-node`).
>
> | | |
> |---|---|
> | **Runs from** | repo root |
> | **Reads** | the freshly generated `sdk/nodejs/` & `sdk/python/`, `provider/base-schema.json` (version, via `ANVIL_VERSION`), `docs/{nodejs,python}/README.md` |
> | **Writes** | small, targeted edits to the generated SDKs (see below) |
> | **Invoked by** | `build.go` after `gen-sdk` + overlay copy: `fix-sdk.ts --ts` / `--python` |
> | **Why** | `gen-sdk` produces a correct-but-bare SDK; this adds the packaging and the wiring that Pulumi can't express in the schema |

## What it does

Runs after `pulumi package gen-sdk` and after the hand-written
`sdk/overlays/<lang>/` files have been copied in. It does **only** the things the
schema *can't* carry — almost all of it is robust JSON/file writes, plus a few
small wiring patches.

> History: this file used to splice **types, enums, boolean shorthands, grant
> methods, and `fromId`** into generated files via fragile string anchors. All of
> that is gone — enums/metadata moved to the schema, grants moved to
> [companion files](../grants/README.md), and the rest was removed. What remains
> is the irreducible packaging/wiring below.

### TypeScript (`sdk/nodejs/`)

| Step | What | Kind |
|---|---|---|
| tsconfig | set `ignoreDeprecations` (Pulumi pins an old TS that errors on deprecated `moduleResolution`) | robust JSON |
| index wiring | append `export * from "./_extras"` (the hand-written overlay barrel: `App`, `Block`, grants, Pulumi re-exports) | 1-line append |
| utilities.ts | inject `maybeWrapComponent` into `lazyLoad` (powers `ctx.ref` forward references) | string-anchored, **warns on miss** |
| package.json | set `main`/`types`/`version` placeholder/build script | robust JSON |

### Python (`sdk/python/`)

| Step | What | Kind |
|---|---|---|
| pyproject | fill the `version = "0.0.0"` placeholder `gen-sdk` emits | robust |
| README | copy `docs/python/README.md` in | robust |
| _utilities.py | fix the distribution-name lookup (`importlib.metadata.version`) | string-anchored, **warns on miss** |
| __init__.py | wrap the `lazy_import` block with `wrap_namespace` (forward refs) + append `from ._extras import *` (the overlay barrel) | string-anchored, **warns on miss** |

## The two categories of work

1. **Robust packaging/file writes** (most of it) — these parse JSON or copy files;
   they don't string-match generated code, so they can't silently drift.
2. **Forward-reference / lookup wraps** — these inject behavior into files
   `gen-sdk` *owns* (`utilities.ts`, `__init__.py`, `_utilities.py`), so there's
   no overlay for them; they must be patched. All three **`console.warn` on a
   missed anchor** (fail loud, not silent).

## Where the version comes from

`getVersion()` prefers `process.env.ANVIL_VERSION` (passed in by `build.go`,
which reads `provider/base-schema.json` once) and falls back to reading
`base-schema.json` directly. One source of truth, threaded in.

## What this script deliberately does NOT do

- **Types / enums / unions** → expressed in the schema, emitted natively by
  `gen-sdk`. Don't patch them here.
- **Grant methods** → [`grants/`](../grants/README.md) companion files.
- **Hand-written modules** (`app`, `block`, `grants` runtime, `stack`) → live in
  `sdk/overlays/<lang>/`, copied in by `build.go` (not here).

## Gotchas

- If you see a `⚠ … anchor not found` warning during a build, `gen-sdk`'s output
  shifted and a forward-ref/lookup patch needs its anchor updated. The build still
  produces an SDK, but that feature won't be wired — treat the warning as an error.
- Adding a new hand-written export? Put it in `sdk/overlays/<lang>/_extras.*`, not
  here — this file only appends the single barrel import.
