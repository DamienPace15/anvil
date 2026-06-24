# Forward References in Anvil

**Status:** Implemented in TypeScript and Python. Verified **end-to-end through a
real `anvil deploy`** — an out-of-order Queue → Lambda → DSQL graph was created on
AWS. Go is not yet designed.

## What it is

`ctx.ref('id')` references a resource (or block) by its logical name **before or
after it is declared**, decoupling declaration order from dependency order:

```ts
const lam = new anvil.aws.Lambda('lam', {
  environment: { DSQL_ENDPOINT: ctx.ref<anvil.aws.DSQL>('db').endpoint },  // forward ref
});
const db = new anvil.aws.DSQL('db', { /* ... */ });   // declared AFTER — fine
```

It's an **escape hatch** for mature codebases where something occasionally needs to
reference a resource declared later. Most programs declare in dependency order and
never need it — reach for it deliberately, not by default.

## How it works (Approach A — deferred-lookup refs)

Resources construct **eagerly**, exactly as before. The mechanism is purely
additive:

1. A per-program **`Stack` registry** maps each resource's logical id → its
   constructed object. Components **auto-register on construction** — in TS by
   wrapping each component class inside `utilities.lazyLoad` (so `anvil.aws.X`
   stays a real namespace, usable as a *type*; a namespace `Proxy` would break
   that); in Python via a wrapping subclass. The `new anvil.aws.X('id', …)`
   syntax is unchanged.
2. `ctx.ref('id').<output>` returns a **deferred `pulumi.Output`** backed by a
   per-id future/promise that resolves when `id` registers — which may happen
   **later** in the same synchronous `run()`.
3. Because it resolves to the resource's **real Output**, Pulumi's dependency
   tracking, ordering, and parallelism all work unchanged. **Pulumi builds the
   graph; the SDK does not.**
4. After `run()` returns, `validate()` turns any never-declared reference into a
   clean error, so a typo fails loudly instead of hanging.

Files: `sdk/nodejs/stack.ts`, `sdk/python/anvil_cloud/stack.py`, wired through
`app.*`, `block.*`, and the package entry, kept alive across `gen-sdk` via
`build.go` (backup list) + `scripts/sdk/fix-sdk.ts`.

### Why not a two-phase model?

The "textbook" alternative is two-phase: collect declarations as inert specs,
topologically sort, then materialize in dependency order (what CDKTF and Pulumi
YAML do internally). It gives **static** cycle/typo detection. We rejected it
because **the SDKs are codegen'd** — deferring construction would mean rewriting
the generators for every component. Approach A is additive and leans on real
Outputs. The trade-off: some errors two-phase catches *statically* become
**runtime** issues here (see Limits & gotchas).

## The API

- `ctx.ref<T>('id').<output>` — the generic `<T>` is **compile-time only**
  (editor autocomplete); at runtime every property access returns an Output.
- **Invariant:** `db.endpoint` (object in scope) and `ctx.ref('db').endpoint`
  (forward ref) produce the **same** Output — fully interchangeable.

## Scope: Mode 1 (pass), not Mode 2 (transform)

- **Mode 1 — shipping.** *Pass* a forward-referenced output into another
  resource's args or into `ctx.export`. Resolves to the real output at deploy.
  ```ts
  environment: { DSQL_ENDPOINT: ctx.ref('db').endpoint }   // ok
  ctx.export('endpoint', ctx.ref('db').endpoint)           // ok
  ```
- **Mode 2 — not built.** *Transforming* a forward ref at declaration time
  (`.apply`, `interpolate`) before the resource exists. The `Ref` keeps the seam
  open, but it is not implemented — forward refs are rare and transforms on them
  rarer.
- **Grant targets — not supported.** `ctx.ref` surfaces *outputs only*; it is not
  a valid grant target (`dsql.grantConnect(ctx.ref('fn'), …)` won't work because a
  ref doesn't implement `grantName()`/`grantRoleArn()`). Call grants on the real
  object. A possible follow-up.

## Blocks

A block is **transparent to the dependency graph** — Pulumi parenting affects URNs
and grouping, never ordering — so forward refs across block boundaries are
ordinary refs.

- **Block-level references only.** Reference a block's *public outputs*, not its
  internal children (keeps encapsulation, avoids child-name collisions).
- **Explicit output surface.** A block publishes via `this.output(name, value)`
  (symmetric with `ctx.export`); `ctx.ref<Block>('id').name` reaches it.
- **Owns only what it publishes.** By design a block should publish only outputs
  from resources it owns — not relay a foreign `ctx.ref(...)`. **Note:** this is a
  convention, **not yet enforced in code** (a follow-up guard).

```ts
class Storage extends anvil.Block {
  constructor(name: string) {
    super(name);
    const events = new anvil.aws.Bucket('events', { /* ... */ });
    this.output('bucketName', events.bucketName);   // public contract
  }
}
ctx.ref<Storage>('storage').bucketName   // ok — published output
```

## Limits & gotchas (learned in practice)

1. **Cycles deadlock — they are not statically detected.** Forward refs make it
   easy to write a cycle that in-order code couldn't, e.g.:
   - Queue's consumer = `ctx.ref('fn').arn` (Queue needs the Lambda), **and**
   - Lambda's env = `ctx.ref('queue').url` (Lambda needs the Queue).

   Queue ⇄ Lambda. The program runs fine (both refs resolve), but the resulting
   dependency graph has a cycle and **Pulumi's engine hangs at apply** — each
   resource waits on the other's output forever. It creates everything *outside*
   the cycle, then stalls, with **no "cycle detected" error**. This is the sharpest
   downside of Approach A (a two-phase model would catch it statically).
   **Keep references flowing one direction.** (The Queue/Lambda fix: drop the
   back-reference — an SQS-triggered Lambda receives messages via the event-source
   mapping and doesn't need the queue URL.)

2. **No concrete value at declaration time.** You can pass a forward-referenced
   output but never read or branch on it synchronously — true of any Pulumi /
   Terraform output, not an Anvil quirk.

3. **Build-mode discovery.** `anvil deploy` first runs the program with
   `ANVIL_BUILD_MODE=true` to discover Lambda functions. That path serialises
   component inputs **synchronously during construction**, so a forward ref to a
   later-declared resource would deadlock it. **Fixed:** in build mode `ctx.ref`
   returns a resolved placeholder (`""`) — the real value is irrelevant there
   (components are empty stubs; only the Lambda manifest matters). Normal
   preview/deploy is unaffected. (`stack.py` `_resolve`, `stack.ts` `ref`.)

4. **Mode 1 only** — no `.apply` / `interpolate` on a forward ref (that's Mode 2).

## Error behaviour

| Case | What happens |
| --- | --- |
| Undeclared id (typo / never declared) | `validate()` raises after `run()` — clean pre-deploy error |
| Duplicate id | `register()` raises immediately |
| Unknown output on a ref | rejected Output at apply time (`"id" has no output "x"`) — not static |
| Circular dependency | **deadlock / hang at apply** — *not* detected (see Limits) |

## Per-language status

- **TypeScript** — promise-backed `ctx.ref`; auto-registration by wrapping
  component classes inside `utilities.lazyLoad` (keeps `anvil.aws.X` a usable type).
- **Python** — per-id `asyncio.Future` resolved by `register()`, surfaced via
  `Output.from_input(coro).apply(...)`; namespace wrapped via a proxy + per-class
  subclass. Verified on Python 3.13 / pulumi 3.247: unit tests + build-mode
  discovery + a real `anvil deploy` creating DSQL / Lambda / Queue.
- **Go** — not started. No dynamic property access, no clean deferred Output, no
  namespace interception; needs a more explicit API, e.g.
  `ctx.Ref[*aws.DSQL]("id").Output("endpoint")`.

## Operational note (Python)

`anvil deploy` runs the program with whatever `python3` is on `PATH`. The SDK +
`pulumi` must be importable there, so **activate the project venv first**
(`source .venv/bin/activate`) — otherwise the program fails with
`ModuleNotFoundError: No module named 'pulumi'`. This is unrelated to forward refs
but is the most common cause of a "deploy does nothing" report. (anvil regenerates
`.anvil/Pulumi.yaml` each run without a `virtualenv` option, so pinning it there
does not stick — a worthwhile CLI improvement.)

## Symmetry across the three layers

| Layer    | Publish               | Reach                |
| -------- | --------------------- | -------------------- |
| App      | `ctx.export('x', v)`  | (stack output)       |
| Block    | `this.output('x', v)` | `ctx.ref('block').x` |
| Resource | (its own outputs)     | `ctx.ref('res').arn` |

Same Output, same deferred resolution, at every layer.
