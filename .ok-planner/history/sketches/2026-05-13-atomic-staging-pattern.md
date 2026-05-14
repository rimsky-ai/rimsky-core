# Atomic staging pattern — worked example for custom claim producers

**Date:** 2026-05-13
**Status:** sketch / wishlist
**Companion sketches:** `2026-05-13-blessed-typed-attributes.md`

## What this is

A pattern doc + worked example for consumers building custom
`ClaimProducer`s where the desired semantics are:

- `Open` creates a staging area against the target substrate.
- Writes during the claim's lifetime go to staging, not to canonical state.
- `Commit` atomically swaps staging into canonical position.
- `Abandon` drops the staging area; canonical state is untouched.

Generic across substrates. The "atomic" part lives in the producer's
implementation, not in rimsky — rimsky just orchestrates the verb sequence
and gates concurrent acquisition.

Lands as `docs/agents/examples/atomic-staging.md` upstream; this sketch is
the design that produces it.

## Why this is worth a worked example

The bundled producers don't cover this shape directly. `stores/postgres` is
queue-shaped (regional access + items-table queue). `stores/filesystem` is
folders-as-queue-items with `pop_and_move` / `pop_and_delete` actions —
close in spirit to staging, but limited to filesystem folders.

Consumers who want "writable canonical state with stage-then-swap
semantics" against substrates like:

- Postgres schemas (atomic schema rename swap)
- S3 prefixes (atomic prefix rename or manifest pointer flip)
- BigQuery datasets (table copy + swap)
- Iceberg / Delta tables (manifest pointer flip)
- Filesystem trees (symlink swap)
- Manifest-pointer architectures (atomic pointer update)

… all want the same conceptual shape. Today they each invent it from
scratch. A worked example + the pattern documentation makes this a known
shape consumers can adopt.

## The pattern

The producer implements the four ClaimProducer verbs against the target
substrate:

### `Open(scope, intent: rw)`

1. Generate a staging area unique to this claim. Conventions:
   - Postgres schema: `staging_{scope}_{claim_id}`.
   - S3 prefix: `staging/{scope}/{claim_id}/`.
   - Filesystem tree: `staging/{scope}/{claim_id}/`.
   - Iceberg table: a new branch off the canonical table.
2. Return the staging area's substrate-native address (schema name, prefix,
   path, branch name) as `OpenResponse.address`.
3. Capture relevant metadata as `OpenResponse.payload` if useful for the
   executor (e.g. canonical address for write-through patterns, expected
   schema, etc.).
4. Declare `realized_write_semantics: staged_async` — writes against the
   address don't conflict with reads against the canonical address.
5. Producer-internal: record `(claim_id, staging_address, canonical_address)`
   in a producer-managed table so commit/abandon can find it.

### `Commit(claim_id)`

Atomically promote staging to canonical. The atomicity is substrate-
specific:

- Postgres: `BEGIN; DROP SCHEMA canonical_X CASCADE; ALTER SCHEMA
  staging_X_C RENAME TO canonical_X; COMMIT;`
- S3: list staging objects, copy with canonical key prefix, delete staging
  prefix. Less atomic — see the "atomicity caveats" section below.
- Iceberg: fast-forward the canonical branch to the staging branch.
- Filesystem: `mv staging_X_C canonical_X` (POSIX rename is atomic on same
  filesystem).

On success, the producer's internal record is cleaned up.

### `Abandon(claim_id)`

Drop the staging area:

- Postgres: `DROP SCHEMA staging_X_C CASCADE`.
- S3: delete the staging prefix.
- Iceberg: drop the staging branch.
- Filesystem: `rm -rf staging_X_C`.

Producer's internal record cleaned up.

### `Release(claim_id)`

For `r`-intent claims that don't hold staging, `Release` is a no-op.
For `rw`-intent claims that never need their changes promoted, treat
`Release` as `Abandon`.

### `Capabilities()`

Declares the producer's protocol, write-semantics envelope, scope-conflict
matrix:

- `protocols: [claim_producer]`
- `write_semantics_envelope: [staged_async]`
- `scope_conflict_matrix`: `rw`-`rw` on same scope = conflict; `rw`-`r` and
  `r`-`r` = compatible.

## Held-subgraph integration

The pattern shines when the held-claim spans multiple nodes:

```yaml
nodes:
  - type: stage-data
    executor: http-node
    stores:
      - { name: my-staging-store, alias: target, selector: my-scope, intent: rw }
    userdata: { url: "http://my-loader.internal:8080/load" }

  - type: verify-staged
    executor: verifier-shape-checks
    dependencies: [stage-data]
    inherits:
      - { claim: target }
    userdata: { checks: [...] }

  - type: verify-staged-domain
    executor: verifier-http
    dependencies: [stage-data]
    inherits:
      - { claim: target }
    userdata: { url: "http://my-checks.internal:8080/verify" }
```

The `stage-data` node opens the held claim. Both verifier nodes inherit the
claim (reading from staging via the substituted address). On all-success,
the holding-subgraph auto-terminal fires `Commit` → staging swaps to
canonical. On any-failure → `Abandon` → staging drops; canonical state
unchanged.

This is the pattern's load-bearing benefit: **bad data never reaches
canonical state, because verification happens against staging within the
held claim, and Commit only fires on all-success aggregate outcome.**

## Atomicity caveats by substrate

The "atomic" part is the producer's responsibility, but not every substrate
supports true atomic swap. Honest accounting:

- **Postgres / Iceberg / Filesystem (POSIX rename)**: atomic. The swap
  succeeds or fails as a unit; readers see one consistent state.
- **S3 (with copy + delete)**: not atomic. There's a window where both
  staging and canonical exist; readers using "list prefix" patterns will
  see in-between state. Mitigations: manifest-pointer architectures
  (canonical isn't a prefix; it's a pointer file that gets atomically
  flipped via `If-Match` semantics); or accept the window and document it.
- **BigQuery**: depends on the swap strategy. Atomic with table-copy-then-
  drop within a single transaction-equivalent flow; non-atomic with
  load-then-promote.
- **Kafka / streaming substrates**: atomic swap is incoherent for these.
  The pattern doesn't apply.

Producer authors document their substrate's atomicity properties as part of
the producer's README. Consumers select producers whose properties match
their requirements.

## Concurrent stagers

If two `rw` claims try to open against the same scope simultaneously, the
scope-conflict matrix gates them serially — only one is acquired; the other
waits or fails per the holding node's `on_acquire_unavailable` handler.
Same as any `rw`-`rw` claim conflict in rimsky.

The producer doesn't need to handle concurrent staging on the same scope
internally; rimsky's claim-handle gating prevents it.

What the producer DOES need to handle: leaked staging areas from crashed
runs. The producer should run a periodic sweep that drops staging areas
whose claim-handle no longer exists in rimsky. Or use TTL on staging
areas. Or both. Worked example covers this with a sweep loop.

## Reference implementation outline

Worked example uses a generic illustrative substrate — probably **filesystem
with directory swap** as the simplest concrete case that exhibits atomicity:

- Producer binary: `examples/atomic-staging-fs-producer/`.
- Backing layout: configured root directory; subdirectories for canonical
  state per scope; staging tree alongside.
- `Open` creates a staging subdirectory; returns its absolute path.
- Executor (a thin `http-node` invocation against a small Go file-writer
  service in the example, or directly an `http-node` POST to a real
  consumer-side endpoint) writes files into the staging path.
- `Commit` atomically swaps via two-rename pattern: rename canonical to
  `_old`, rename staging to canonical, rm `_old`. Atomic on same
  filesystem; documented limitation.
- `Abandon` does `rm -rf` on staging.
- Sweep loop in the producer drops staging directories older than 24 hours
  whose claim_id isn't in rimsky's claim-handle table.

The example template uses two verifier nodes inheriting the staging claim,
demonstrating the all-success-Commit / any-failure-Abandon pattern end-to-
end.

## What consumers learn from this

- The four ClaimProducer verbs map cleanly onto stage-then-swap-or-abandon
  semantics.
- Held-subgraph membership is the right machinery for "atomicity over
  multiple nodes."
- Substrate-specific atomicity is the producer's responsibility; rimsky
  doesn't mediate it.
- Sweep / TTL / orphan handling is the producer's responsibility.
- The pattern composes with verifier executors for "verify staging before
  promoting" without any special machinery.

## Open design questions

1. **Should the rimsky reference producer set include an atomic-staging
   variant?** Today's bundled producers don't have one. Could add as
   `stores/atomic-staging-fs/` — bundled, conformance-tested, but mostly
   serving as a reference. Or leave it purely as a worked example without
   shipping a bundled producer. Leaning toward example-only; bundled
   producers should be load-bearing, not "look here for inspiration."
2. **Multi-substrate variants in one example?** Cover only filesystem (the
   simplest concrete case), or also include sketches for Postgres / S3 /
   Iceberg variants? Lean: one concrete case fully worked + a closing
   section listing substrate-by-substrate atomicity strategies.
3. **Relationship to `concept:claim-producer-fs-store#pop_and_move` action**?
   The bundled filesystem producer's `pop_and_move` action is a related
   shape (folder-as-queue-item; on Commit, move it to a target directory).
   The atomic-staging pattern is broader — it's about staging arbitrary
   substrate-shaped state, not about queue-item lifecycle. Worth cross-
   referencing in the doc.

## Phasing

**Phase 1**: doc as `docs/agents/examples/atomic-staging.md`.
- Pattern description with the four ClaimProducer verbs.
- Held-subgraph integration.
- Atomicity caveats per substrate.
- Reference impl walkthrough.

**Phase 2**: reference impl alongside the doc.
- `examples/atomic-staging-fs-producer/` Go binary.
- Sample template demonstrating it.
- Conformance run.

**Phase 3** (optional): bundled producer if a real consumer load warrants it.
