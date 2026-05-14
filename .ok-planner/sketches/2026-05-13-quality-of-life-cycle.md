# Quality-of-life cycle — parked-reason, barrier executor, atomic-staging pattern

**Date:** 2026-05-13
**Status:** sketch / cycle proposal
**Dependencies:** none.
**Supersedes:** `.ok-planner/history/sketches/2026-05-13-parked-state-dashboard-surface.md`,
`.ok-planner/history/sketches/2026-05-13-fan-in-conditional-subgraphs.md`,
`.ok-planner/history/sketches/2026-05-13-atomic-staging-pattern.md`

## What this cycle is

Three quality-of-life improvements that collectively sharpen rimsky's
observability and pattern surface. Bundled together because each is
self-contained and none earns its own cycle on the margin.

The three pieces:

1. **`parked_reason` enum** — typed park-reason categorization on
   `proto:executor.proto::Snooze`, surfaced through schema, diagnostics,
   CLI, and dashboards.
2. **`barrier` bundled executor** — first-class fan-in pattern for
   conditional subgraphs, depending on `PARK_REASON_BARRIER_WAIT` from (1).
3. **Atomic-staging pattern** — pattern doc + reference filesystem
   producer demonstrating stage-then-swap-on-Commit semantics for custom
   `ClaimProducer`s.

---

## Piece 1: `parked_reason` enum on `Snooze`

### The gap

`concept:parked` covers nodes paused for time-based wake, signal-based
wake, or watchdog timeout. The state itself is a clean primitive. What's
missing is **observability granularity**: today every parked node looks
alike to `concept:operational-health` and to operators reading dashboards.

Distinct usage shapes that converge on the same state:

- **Time-wait** — rate-limit retry, scheduled resume after backoff.
- **Signal-wait** — awaiting an external event (webhook callback, async
  API completion, message-queue notification).
- **Awaiting-human** — paused for operator approval, sign-off, manual
  intervention.
- **Barrier-wait** — see Piece 2 below; waiting for upstream subgraph
  completion signals.
- **Retry-backoff** — waiting between retry attempts after a transient
  failure.

From an operations perspective these are very different alerts:

- A long-parked rate-limit-retry is normal and expected.
- A long-parked awaiting-human is a paging signal (someone forgot a
  review).
- A long-parked signal-wait might mean the external system is broken.
- A long-parked barrier-wait probably means an upstream subgraph is stuck.

Today they all show up as "parked." Operators have to read the node's
context to understand which case applies.

### Wire change

```protobuf
message Snooze {
  optional google.protobuf.Timestamp resume_at = 1;
  optional bytes payload = 2;
  optional string session_token = 3;
  optional ParkReason reason = 4;       // new
  optional string reason_label = 5;     // new; free-form consumer label
}

enum ParkReason {
  PARK_REASON_UNSPECIFIED = 0;
  PARK_REASON_TIME_WAIT = 1;
  PARK_REASON_SIGNAL_WAIT = 2;
  PARK_REASON_AWAITING_HUMAN = 3;
  PARK_REASON_BARRIER_WAIT = 4;
  PARK_REASON_RETRY_BACKOFF = 5;
  PARK_REASON_OTHER = 99;
}
```

`reason_label` carries consumer-specific categorization for cases that
don't fit the enum (e.g.
`{reason: PARK_REASON_OTHER, reason_label: "awaiting-tax-bureau-approval"}`).

### Schema

```sql
ALTER TABLE rimsky_nodes ADD COLUMN parked_reason TEXT;
ALTER TABLE rimsky_nodes ADD COLUMN parked_reason_label TEXT;
```

Pre-v1, so a baseline migration update rather than a new migration.

### Supervisor surface

- Supervisor stores `parked_reason` + `parked_reason_label` alongside
  `parked_at`, `resume_at`, `max_park_duration` on the node row.
- The reason is the executor's call. The supervisor doesn't infer it.
- Reason updates on each `Snooze` — a barrier-wait that times out and
  re-parks as awaiting-human writes the new reason on the next park.

### Diagnostics endpoints

`route:GET /admin/diagnostics/parked-nodes` already exists per
`concept:parked`. Extend with optional `?reason=<name>` filter:

- `?reason=awaiting_human` — surface only human-awaiting parks for a
  "pending review" dashboard.
- `?reason=signal_wait` — surface external-signal-waiting parks for an
  "is the external system healthy" dashboard.
- `?reason=barrier_wait` — surface barrier-pattern parks for a "is the
  fan-in stuck" dashboard.

Compound filters welcome: `?reason=awaiting_human&older_than=1h`.

### `rimsky-cli`

```sh
rimsky-cli parked list                       # all parked
rimsky-cli parked list --reason=awaiting_human
rimsky-cli parked list --reason=signal_wait --older-than=1h
```

### Dashboards

The bundled dashboard in `dashboards/` distinguishes the categories.
Awaiting-human stands out visually (often the operator-relevant case);
time-wait fades into background context.

### Bundled executors that emit `reason`

- **`barrier`** (Piece 2): `Snooze{reason: BARRIER_WAIT}`.
- **Rate-limit-aware HTTP executors** (a `http-node` extension):
  `Snooze{reason: TIME_WAIT, resume_at: <when rate-limit resets>}`.
- **Webhook-shaped executors** that emit `AsyncAccepted` and then `Snooze`
  awaiting callback: `Snooze{reason: SIGNAL_WAIT}`.
- **Approval-gate executors** (consumer-built, but conventional):
  `Snooze{reason: AWAITING_HUMAN}`.

### LifecycleSubscriber emission

`OnNodeParked` (currently not in `concept:lifecycle-subscriber` — see open
questions) would carry the reason for consumer-side alerting integrations.

### Per-reason `max_park_duration` (follow-up section)

Possible follow-up: node-level config of `max_park_duration` per reason:

```yaml
max_park_duration:
  time_wait: 1h
  awaiting_human: 7d
  signal_wait: 1d
```

Different categories have different reasonable bounds. Defer to a follow-up
unless brainstorm pressure-tests it as core.

### Open questions

1. **Default reason behavior.** Today's executors don't emit a reason.
   Migration: absent reason defaults to `PARK_REASON_UNSPECIFIED` (visible
   in dashboards as "uncategorized"). Operators encourage executors to
   adopt explicit reasons over time.
2. **`OnNodeParked` lifecycle event.** Adding this means a seventh
   `LifecycleSubscriber` method; non-trivial. Probably yes, but worth
   pressure-testing the alternative (operators poll the diagnostics
   endpoint).
3. **Reason granularity vs. taxonomy churn.** Five reasons + UNSPECIFIED +
   OTHER feels right; adding more without consumer pressure invites
   bikeshedding.

---

## Piece 2: `barrier` bundled executor

### The problem

Rimsky `concept:node` dependencies are strict-AND: a node becomes eligible
for dispatch when **all** its declared dependencies are `fresh`. There's
no "any-of" semantics, no optional dependencies, no soft dependencies.

This works cleanly for fixed-shape pipelines where every node always runs.
It works less cleanly for templates where some subgraphs are
**conditionally activated** — present in the template, but only active for
some instances or some runs.

Concrete shape that exposes the friction:

```
intake → spine_a → spine_b → ... → spine_z → finalize
            ↓
        optional_subgraph_1 → optional_check_1
            ↓
        optional_subgraph_2 → optional_check_2
```

`finalize` wants to depend on all of `spine_z`, `optional_check_1`,
`optional_check_2` — *but only when those optional subgraphs were
applicable for this run*. If they're not applicable, `finalize` should
proceed without them.

### Today's pattern (readiness nodes + parked)

The rimsky-native answer today uses existing primitives:

1. The optional subgraph has no spine dependency. Its entry node has
   `dependencies: []`. It's only reached via an `on_event` invalidate
   from `intake` (the node that knows the routing decision).
2. `intake`'s `on_event` handler fires `invalidate: { targets:
   [optional_subgraph_entry] }` for each applicable subgraph.
3. A **readiness node** sits between the spine and `finalize`:
   - Hard dep on `spine_z` (always required).
   - `on_event` handlers listening for completion signals from optional
     subgraphs.
   - On dispatch, reads `intake.value.applicable_subgraphs`. If empty,
     completes immediately. If non-empty, emits `Snooze` (parked) and
     waits for completion-signal invalidates from each applicable
     subgraph.
   - When all applicable subgraphs have signaled, the readiness node's
     resume dispatch completes, propagating to `finalize`.

This works. It uses existing primitives. It's correct. But it requires
the template author to design and implement a readiness node with
non-trivial state-machine logic, and the resulting graph has an extra
node per fan-in point with subtle wiring.

For workflows with many conditional subgraphs and many fan-in points,
this pattern multiplies — every fan-in needs a custom readiness node,
or a generic "wait for these named completion events" executor.

### The proposal: ship the readiness pattern as a bundled executor

A new bundled executor: `barrier`. Userdata declares "wait for these
named completion signals to fire before completing":

```yaml
nodes:
  - type: post-discovery-barrier
    executor: barrier
    dependencies: [spine_z]
    inputs_from: [intake]
    userdata:
      wait_for:
        - optional_check_1.completed
        - optional_check_2.completed
      filter:
        # only wait if intake declared the corresponding subgraph applicable
        from_attribute: applicable_subgraphs
      timeout_seconds: 300        # optional
      on_timeout: proceed         # or fail
```

A `barrier` is essentially the readiness-node pattern as a bundled
executor. It reads upstream attributes to determine which signals it
should wait for, parks (via `Snooze{reason: BARRIER_WAIT}`) until those
signals arrive (via `on_event` handlers attached to the barrier itself),
then completes.

### Signal vocabulary

The bulk of the barrier's design work is the signal vocabulary. Two
candidate signal sources:

1. **Named events** emitted by the upstream subgraph's terminal node.
   Explicit, declarative, but requires the upstream executor to know
   about the signal.
2. **Fresh transitions** on specific named upstream nodes. Implicit, no
   executor cooperation needed, but couples the barrier's correctness to
   cascade timing.

Decision needed during brainstorm. Lean: support named events first
(explicit), allow fresh-transition signals as a follow-up if demand
emerges.

### Userdata schema (first cut)

```yaml
wait_for:                        # list of signals to wait for
  - <node_name>.<event_name>     # named-event form
  - <node_name>:fresh            # fresh-transition form (deferred)

filter:                          # optional; gates which signals are required
  from_attribute: <attribute>    # reads upstream attribute as a list of
                                 # signal names; intersect with wait_for

timeout_seconds: 300             # optional
on_timeout: proceed | fail       # default fail
```

### Composition with held subgraphs

Can a barrier be a member of a holding subgraph? If yes, what happens at
auto-terminal — does the barrier's parked state delay the holding-
subgraph aggregate outcome? Probably yes (the barrier is `active` from
the holders ledger's perspective until it completes or fails), but needs
explicit modeling in the spec.

### Idempotency under invalidate

If an upstream subgraph is invalidated after the barrier already saw its
completion signal, what happens? Probably: the barrier should re-park
and wait again. Needs explicit semantics in the spec.

### Relationship to `concept:parked` heartbeating discipline

The barrier is parked while waiting. `concept:parked` says parked nodes
don't heartbeat, the orphan reaper skips them, held claims persist. The
barrier pattern fits this model — verify no surprises during
implementation.

### Why not change `concept:node` itself?

Two alternatives we considered and deferred:

- **Direction A: optional dependencies** (`dependencies: [foo?]`) —
  most ergonomic at the template level, but substantial foundation work
  (graph-reachability analysis, new state semantics, scheduling-logic
  changes). Once shipped, it's a permanent foundation feature.
- **Direction B: first-fresh-of-set** (`dependencies_any_of: [[a, b]]`)
  — simpler foundation work, but limited use case coverage and composes
  awkwardly with multi-subgraph fan-in.

Direction C (the bundled `barrier` executor) is the lowest-commitment
move. No foundation changes; just a bundled executor with a clean
userdata schema. If template authors consistently want syntactic sugar
that hides the barrier behind cleaner dependency notation, that's a
signal Direction A earns its place; the bundled `barrier` becomes the
underlying mechanism. If the bundled executor proves ergonomic enough,
Direction A may never need to happen.

### Open questions

1. **Signal vocabulary scope** — named events only first, or both named
   events and fresh transitions from day one?
2. **Timeout semantics** — fail vs proceed on timeout; whether to
   include the timed-out signals in writeback details.
3. **Composition with held subgraphs** — confirmed yes; design the auto-
   terminal interaction explicitly.
4. **Multi-instance barriers in a single template** — userdata should
   accommodate referencing the same barrier executor at multiple sites
   without conflict.
5. **Telemetry shape** — what does the barrier write back as its
   attribute value (which signals fired, which timed out, durations).

---

## Piece 3: Atomic-staging pattern

### What this is

A pattern doc + worked example for consumers building custom
`ClaimProducer`s where the desired semantics are:

- `Open` creates a staging area against the target substrate.
- Writes during the claim's lifetime go to staging, not to canonical
  state.
- `Commit` atomically swaps staging into canonical position.
- `Abandon` drops the staging area; canonical state is untouched.

Generic across substrates. The "atomic" part lives in the producer's
implementation, not in rimsky — rimsky just orchestrates the verb
sequence and gates concurrent acquisition.

Lands as `docs/agents/examples/atomic-staging.md` upstream; this cycle
produces the doc + a reference implementation.

### Why this is worth a worked example

The bundled producers don't cover this shape directly. `stores/postgres`
is queue-shaped (regional access + items-table queue).
`stores/filesystem` is folders-as-queue-items with `pop_and_move` /
`pop_and_delete` actions — close in spirit to staging, but limited to
filesystem folders.

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

### The pattern (four verbs)

The producer implements the four `ClaimProducer` verbs against the
target substrate.

#### `Open(scope, intent: rw)`

1. Generate a staging area unique to this claim. Conventions:
   - Postgres schema: `staging_{scope}_{claim_id}`.
   - S3 prefix: `staging/{scope}/{claim_id}/`.
   - Filesystem tree: `staging/{scope}/{claim_id}/`.
   - Iceberg table: a new branch off the canonical table.
2. Return the staging area's substrate-native address (schema name,
   prefix, path, branch name) as `OpenResponse.address`.
3. Capture relevant metadata as `OpenResponse.payload` if useful for the
   executor (e.g. canonical address for write-through patterns, expected
   schema, etc.).
4. Declare `realized_write_semantics: staged_async` — writes against the
   address don't conflict with reads against the canonical address.
5. Producer-internal: record `(claim_id, staging_address,
   canonical_address)` in a producer-managed table so commit/abandon can
   find it.

#### `Commit(claim_id)`

Atomically promote staging to canonical. The atomicity is substrate-
specific:

- Postgres: `BEGIN; DROP SCHEMA canonical_X CASCADE; ALTER SCHEMA
  staging_X_C RENAME TO canonical_X; COMMIT;`
- S3: list staging objects, copy with canonical key prefix, delete
  staging prefix. Less atomic — see "atomicity caveats" below.
- Iceberg: fast-forward the canonical branch to the staging branch.
- Filesystem: `mv staging_X_C canonical_X` (POSIX rename is atomic on
  same filesystem).

On success, the producer's internal record is cleaned up.

#### `Abandon(claim_id)`

Drop the staging area:

- Postgres: `DROP SCHEMA staging_X_C CASCADE`.
- S3: delete the staging prefix.
- Iceberg: drop the staging branch.
- Filesystem: `rm -rf staging_X_C`.

Producer's internal record cleaned up.

#### `Release(claim_id)`

For `r`-intent claims that don't hold staging, `Release` is a no-op.
For `rw`-intent claims that never need their changes promoted, treat
`Release` as `Abandon`.

#### `Capabilities()`

Declares the producer's protocol, write-semantics envelope, scope-
conflict matrix:

- `protocols: [claim_producer]`
- `write_semantics_envelope: [staged_async]`
- `scope_conflict_matrix`: `rw`-`rw` on same scope = conflict;
  `rw`-`r` and `r`-`r` = compatible.

### Held-subgraph integration

The pattern shines when the held claim spans multiple nodes:

```yaml
nodes:
  - type: stage-data
    executor: http-node
    stores:
      - { name: my-staging-store, alias: target, selector: my-scope, intent: rw }
    userdata: { url: "http://my-loader.internal:8080/load" }

  - type: verify-staged
    executor: http-node
    dependencies: [stage-data]
    inherits:
      - { claim: target }
    userdata: { url: "http://my-checks.internal:8080/verify-shape" }

  - type: verify-staged-domain
    executor: http-node
    dependencies: [stage-data]
    inherits:
      - { claim: target }
    userdata: { url: "http://my-checks.internal:8080/verify-domain" }
```

The `stage-data` node opens the held claim. Both verifier nodes inherit
the claim (reading from staging via the substituted address). On all-
success, the holding-subgraph auto-terminal fires `Commit` → staging
swaps to canonical. On any-failure → `Abandon` → staging drops; canonical
state unchanged.

This is the pattern's load-bearing benefit: **bad data never reaches
canonical state, because verification happens against staging within the
held claim, and Commit only fires on all-success aggregate outcome.**

### Atomicity caveats by substrate

The "atomic" part is the producer's responsibility, but not every
substrate supports true atomic swap. Honest accounting:

- **Postgres / Iceberg / Filesystem (POSIX rename)**: atomic. The swap
  succeeds or fails as a unit; readers see one consistent state.
- **S3 (with copy + delete)**: not atomic. There's a window where both
  staging and canonical exist; readers using "list prefix" patterns will
  see in-between state. Mitigations: manifest-pointer architectures
  (canonical isn't a prefix; it's a pointer file that gets atomically
  flipped via `If-Match` semantics); or accept the window and document
  it.
- **BigQuery**: depends on the swap strategy. Atomic with table-copy-
  then-drop within a single transaction-equivalent flow; non-atomic with
  load-then-promote.
- **Kafka / streaming substrates**: atomic swap is incoherent for these.
  The pattern doesn't apply.

Producer authors document their substrate's atomicity properties as part
of the producer's README. Consumers select producers whose properties
match their requirements.

### Concurrent stagers

If two `rw` claims try to open against the same scope simultaneously,
the scope-conflict matrix gates them serially — only one is acquired;
the other waits or fails per the holding node's
`on_acquire_unavailable` handler. Same as any `rw`-`rw` claim conflict
in rimsky.

The producer doesn't need to handle concurrent staging on the same
scope internally; rimsky's claim-handle gating prevents it.

What the producer DOES need to handle: leaked staging areas from
crashed runs. The producer should run a periodic sweep that drops
staging areas whose claim handle no longer exists in rimsky. Or use TTL
on staging areas. Or both. Worked example covers this with a sweep
loop.

### Reference implementation

Worked example uses **filesystem with directory swap** as the simplest
concrete case that exhibits atomicity:

- Producer binary: `examples/atomic-staging-fs-producer/`.
- Backing layout: configured root directory; subdirectories for
  canonical state per scope; staging tree alongside.
- `Open` creates a staging subdirectory; returns its absolute path.
- Executor (a thin `http-node` invocation against a small Go file-writer
  service in the example, or directly an `http-node` POST to a real
  consumer-side endpoint) writes files into the staging path.
- `Commit` atomically swaps via two-rename pattern: rename canonical to
  `_old`, rename staging to canonical, `rm` the `_old`. Atomic on same
  filesystem; documented limitation.
- `Abandon` does `rm -rf` on staging.
- Sweep loop in the producer drops staging directories older than 24
  hours whose claim_id isn't in rimsky's claim-handle table.

The example template uses two `http-node` nodes inheriting the staging
claim and POSTing to consumer-side check endpoints, demonstrating the
all-success-Commit / any-failure-Abandon pattern end-to-end.

### Relationship to `concept:claim-producer-fs-store#pop_and_move`

The bundled filesystem producer's `pop_and_move` action is a related
shape (folder-as-queue-item; on Commit, move it to a target directory).
The atomic-staging pattern is broader — it's about staging arbitrary
substrate-shaped state, not about queue-item lifecycle. Worth cross-
referencing in the doc.

### What consumers learn

- The four `ClaimProducer` verbs map cleanly onto stage-then-swap-or-
  abandon semantics.
- Held-subgraph membership is the right machinery for "atomicity over
  multiple nodes."
- Substrate-specific atomicity is the producer's responsibility; rimsky
  doesn't mediate it.
- Sweep / TTL / orphan handling is the producer's responsibility.
- The pattern composes with verifier executors for "verify staging
  before promoting" without any special machinery.

### Open questions

1. **Bundled producer vs example-only.** Should `stores/atomic-staging-
   fs/` ship as a bundled producer alongside the existing three? Lean
   no — bundled producers should be load-bearing, not "look here for
   inspiration." Example-only.
2. **Multi-substrate variants in one example.** Cover only filesystem
   (the simplest concrete case), or also include sketches for Postgres
   / S3 / Iceberg variants? Lean: one concrete case fully worked + a
   closing section listing substrate-by-substrate atomicity strategies.

---

## Cross-piece interactions

- **`parked_reason` enables `barrier`.** The barrier executor emits
  `Snooze{reason: PARK_REASON_BARRIER_WAIT}`; without the enum, its
  parks look like every other park.
- **Atomic-staging is independent of the other two.** No shared
  surface; uses `http-node` for the worked example's check nodes.

## Phasing within the cycle

1. **`parked_reason` enum + supervisor surface + DB column.** Proto
   regen; supervisor stores and surfaces; diagnostics endpoint accepts
   filter; CLI flag; dashboard updates.
2. **`barrier` bundled executor.** Userdata schema design; Go binary
   under `executors/barrier/`; conformance run; worked example template.
3. **Atomic-staging pattern doc + reference producer.** Doc at
   `docs/agents/examples/atomic-staging.md`; reference producer at
   `examples/atomic-staging-fs-producer/`; sample template
   demonstrating it.
4. **(Follow-up)** Per-reason `max_park_duration`. Optional; defer
   unless brainstorm pressure-tests it as core.

Pieces 1 and 3 are independent and can run in parallel. Piece 2 waits
on Piece 1 (`PARK_REASON_BARRIER_WAIT` must exist).

## What this isn't

- Not a new `concept:node` primitive. The `barrier` is a bundled
  executor, not a foundation change. Optional-dependencies and
  any-of-dependencies (Directions A and B from the original sketch) are
  explicitly deferred.
- Not a substrate-specific atomic-swap implementation. The pattern is
  generic; the reference impl is filesystem-only as a teaching aid.
- Not a typed-attribute change.
