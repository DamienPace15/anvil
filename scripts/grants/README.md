# `grants/` — generate grant methods as companion files

> **Pipeline stage 8** (see [`scripts/README.md`](../README.md)). `generate-grants.ts` (run via `ts-node`).
>
> | | |
> |---|---|
> | **Runs from** | repo root |
> | **Reads** | the declarative configs in `configs/*.ts` (the actions tables) |
> | **Writes** | `sdk/nodejs/aws/<resource>.grants.ts` and `sdk/python/anvil_cloud/aws/<resource>_grants.py`, plus a side-effect import appended to `index.ts` / `__init__.py` |
> | **Invoked by** | `build.go` after `fix-sdk`: `generate-grants.ts --ts` / `--python` |
> | **Why** | grant methods (`bucket.grantRead(fn)`) are Anvil's core permission API; they can't be schema-expressed (they create IAM resources in the user's program at deploy time) |

## What a grant is

A grant wires least-privilege IAM from one resource to another:

```ts
bucket.grantRead(fn);                  // fn's role may GetObject/ListBucket on bucket
bucket.grantRead(fn, ["uploads/*"]);   // …scoped to a path prefix
```

```python
bucket.grant_read(fn)
```

Under the hood each method builds a scoped `aws.iam.RolePolicy` on the **target's**
execution role, parented to the granting resource.

## The design: companion files, not splicing

The grant methods are **added to the generated component classes from the
outside** — the generated `bucket.ts` / `bucket.py` are never edited.

- **TypeScript — declaration merging.** A `bucket.grants.ts` file does
  `declare module "./bucket" { interface Bucket { grantRead(...): void } }`
  (the types) and `Bucket.prototype.grantRead = function (...) { ... }`
  (the implementation).
- **Python — monkeypatch.** A `bucket_grants.py` module defines the functions and
  assigns them: `Bucket.grant_read = grant_read`.

Each companion is wired in via a **side-effect import** appended to the SDK's
barrel (`index.ts` / `__init__.py`) so the prototype/attribute assignments run at
import time. The public API is identical to a normal method — `bucket.grantRead(fn)`
shows up in autocomplete and works at runtime.

> This replaced an older `fix-sdk-grants.ts` that spliced method bodies *inside*
> the generated classes via fragile string anchors (`findClassEnd`,
> `__pulumiType` regex, a literal `resourceInputs["…"]` marker hunt). All gone.

## The three layers

```
configs/*.ts        declarative: which methods → which IAM actions → which scoping   (you edit this)
   │
generate-grants.ts  turns a config into a companion file (decl-merge / monkeypatch)
   │
sdk/overlays/<lang>/grants.{ts,py}   runtime: createGrant / buildResourceArns / grantResourceName  (shared, hand-written)
```

- **Configs** are pure data and the only thing you normally touch.
- **`generate-grants.ts`** is the generator (one file).
- **The runtime** (`createGrant`, `buildResourceArns`, `GrantTarget`) lives in the
  overlays and does the actual IAM plumbing, so each generated method is a thin
  1–3 line call.

## Config types (`types.ts`)

| Type | For | Example |
|---|---|---|
| `GrantConfig` | standard grants on a single-ARN resource | Bucket (`supportsPaths`), DynamoDB (`supportsIndexes`), Queue, EventBus, Lambda |
| `GrantTargetConfig` | makes a compute resource a **grant target** (adds `grantName`/`grantRoleArn`) | Lambda |
| `GrantMapConfig` | resources whose ARN is a `Record<region, arn>` map | DSQL `clusterArns` |
| `CustomGrantConfig` | bespoke grants the generic shapes can't express (raw method body) | DSQL `grantConnect` (spawns a `DSQLConnect` component) |

## The `__name` mechanism (important)

A grant must name its child `RolePolicy` **synchronously** (Pulumi resource names
can't be Outputs). Components don't expose their logical name as a sync property,
so the **construction wrapper** (`sdk/overlays/<lang>/stack.{ts,py}` — the same one
that powers forward references) stashes it on the instance:

- TS: `instance.__anvilName = name`
- Python: `self._anvil_name = name`

Grant methods read it via `grants.grantResourceName(this)` /
`grants.grant_resource_name(self)`. **No per-class name injection.** (This also
fixed a pre-existing Python bug where methods referenced a never-set `self._name`.)

## Adding a grant or a grantable resource

You almost always just edit a config:

1. **New grant on an existing resource** — add a `{ method, actions }` entry to
   that resource's config in `configs/`.
2. **New grantable resource** — add a `GrantConfig` (or `GrantMapConfig`) in
   `configs/`, add it to the arrays in `generate-grants.ts`, and make sure the
   ARN property exists on the schema (e.g. `arn`, `tableArn`).
3. **New grant *target*** (a compute resource that can receive grants) — add a
   `GrantTargetConfig` so it gets `grantName`/`grantRoleArn`, and ensure its role
   ARN is a schema output (e.g. `roleArn`).
4. **Bespoke grant** — use `CustomGrantConfig` with a raw `tsMethod`/`pyMethod`
   body. Reference any output via the schema (don't inject properties).

Then run `go run build.go gen-nodejs` / `gen-python-sdk`. The companion files
regenerate; the generated classes are untouched.

## Go is a different API (not generated here)

The Go SDK has **no per-resource grant methods**. Go consumers call a free
function:

```go
anvil.CreateGrant(ctx, bucket, fn, anvil.GrantOptions{Actions: []string{"read"}, Path: []string{"user/*"}})
```

The runtime lives in `sdk/overlays/go/grants.go`. (The typed
`func (b *Bucket) GrantRead(...)` methods in `provider/aws/*/grants.go` are
orphaned dead code — on the provider types, not the SDK types — and unused.)
Unifying Go with the TS/Python method API would be a new feature, not part of this
generator.

## Gotchas

- **Construct via the namespace.** `grantResourceName` relies on the construction
  wrapper, so resources must be built as `new anvil.aws.Bucket(...)` (the normal
  path). A directly-imported class bypasses the wrapper and grants will throw a
  clear "could not resolve the resource name" error.
- **Companion files are generated** — don't hand-edit `*.grants.ts` / `*_grants.py`;
  edit the config or the generator.
- **`CustomGrantConfig` outputs belong in the schema.** If a custom grant body
  references an output (e.g. DSQL `rolesTableName`), add it to that component's
  schema so `gen-sdk` emits it — don't inject it.
