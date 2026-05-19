# Foundation Contract

**Status:** Authoritative until v1, 2026-05-04.
**Scope:** The minimal stable core of Rimsky — the layer that everything else is convenience on top of.
**Authority:** This document is the foundation contract. Layers above it (modeling, service protocols, bundled services) MAY change without notice; the surface defined here is stable until v1.

---

## 1. Purpose

This document defines Rimsky's **foundation layer**: the smallest self-contained subsystem that captures what Rimsky *is*, without reference to user-facing concepts like templates, instances, frames, schedules, or attributes.

The foundation is the durable integration of two well-understood components:

- a **reactive computation graph** (value-presence + invalidate cascade + worker dispatch), and
- a **lock manager** (scope-keyed claim handles with byte-equal conflict),

bound by an **integration** layer that ties terminal events on the graph to verb calls on the lock manager and that survives crashes via a persistence contract.

Everything Rimsky *does* — schedule cron-driven recomputation, run agentic workflows, gate concurrent work on shared resources, bind templates to instances — is built on this foundation. None of those higher concerns appear in this document. They live in the modeling-layer contract (`docs/specs/2026-05-04-modeling-layer-contract.md`) and the service-protocol contract (`docs/specs/2026-05-04-service-protocol-contract.md`).

## 2. Layer model

Rimsky is structured as four conceptual layers:

1. **Foundation** *(this document)* — cascade engine + lock manager + integration. Stable.
2. **Modeling layer** — templates, instances, frames, schedules, attributes, the public state-machine vocabulary, the YAML config shape, the control-plane API. The thing users learn.
3. **Service protocols** — `ClaimProducer`, `Executor`, `LifecycleSubscriber`. Wire contracts that external services implement. Partly cross-cutting: foundation calls a subset (claim verbs, executor dispatch); modeling calls a subset (lifecycle).
4. **Bundled services + examples** — reference implementations, agentic-workflow primitives, deployment recipes.

Higher layers depend on lower layers; lower layers MUST NOT reach into higher layers. The foundation has no awareness of templates, frames, attributes, schedules, or any modeling concept. The modeling layer programs the foundation; the foundation does not introspect the modeling layer.

## 3. Cascade engine

### 3.0 Subsystem package layout

The cascade engine, lock manager, and integration layer are realized as Go packages `foundation/cascade/`, `foundation/locks/`, and `foundation/integration/` respectively. Foundation persistence lives in `foundation/persistence/`. These names are settled and authoritative.

### 3.1 Node value-presence

Each node has, at any instant, two orthogonal bits of state:

- `has_value` — whether the node currently has a produced value.
- `has_outstanding_request` — whether there is an outstanding worker request for the node.

Plus one annotation:

- `auto_recovers` — whether the absence of a value should auto-trigger a worker request when conditions allow.

These three observables generate the foundation's node-state space. The four named states presented in user-facing docs (`fresh`, `stale`, `running`, `failed`) are the modeling layer's chosen presentation of this space:

| has_value | has_outstanding_request | auto_recovers | modeling-layer name |
|-----------|------------------------|---------------|---------------------|
| true      | false                  | n/a           | `fresh`             |
| false     | false                  | true          | `stale`             |
| false     | true                   | n/a           | `running`           |
| false     | false                  | false         | `failed`            |

The foundation traffics in the bits and the annotation; the modeling layer assigns names.

### 3.2 Cascade signal

The foundation defines exactly one graph-level message:

- **`invalidate(node, [targets])`** — when a node loses or replaces its value, the cascade signal propagates to a chosen target set, marking those targets as `has_value=false`. The target set is computed by a per-node predicate supplied by the modeling layer (typically: "all dependents whose dependency relation matches a `changed`-aware policy").

Recalculation is *not* a graph-level message. It is a per-node action initiated by the dispatch loop when a node is eligible (`has_value=false`, `has_outstanding_request=false`, `auto_recovers=true`, plus modeling-layer constraints).

### 3.3 Worker request lifecycle

A worker request represents one outstanding piece of work for one node. Its lifecycle:

1. **Created** — modeling layer (or an internal mechanism) inserts a worker-request row.
2. **Claimed** — a runner instance atomically marks the row as owned by it (the *dispatch claim*). The runner SHOULD heartbeat ownership at a regular interval.
3. **In progress** — the runner has dispatched the work to its target service; awaits terminal.
4. **Terminal** — the runner records one of:
   - **Success-with-value**: node transitions to `has_value=true`; cascade fires per the per-node predicate.
   - **Failure-with-cascade-signal**: node transitions to `has_value=false`, with `auto_recovers` set to a runner-supplied flag and a runner-supplied (possibly empty) cascade target set.

The foundation's failure-terminal admits any (auto_recovers, cascade_targets) pair. The modeling layer's three-action vocabulary (`retry`, `invalidate(targets)`, `give_up`) is one chosen surface over this primitive; each modeling action is realized by the modeling layer translating to a specific (auto_recovers, cascade_targets) pair before invoking the foundation.

### 3.4 Dispatch claim

The dispatch claim brackets the running window. Until claim, the worker request is dispatchable; while claimed, it is in-flight; after terminal, the row is consumed.

**Foundation invariant 2** *(dispatch claim brackets the running window)*: a node's modeling-layer "running" presentation is computed from the worker-request's claim state (`worker_request.claimed_by IS NOT NULL` in the active phase post-Phase-5; `phase = 'active'`), never from any other source. Lock-eligibility counts (e.g., counting-mode named locks) join against this column. The integration layer's lifetime correlation depends on it.

### 3.5 State-machine integrity

**Foundation invariant 1** *(state machine rejects illegal transitions)*: updates that violate the cascade engine's transition rules error rather than silently no-op. Specifically: a node already in `(has_value=false, has_outstanding_request=true)` cannot be re-entered into the same state under the reason `dispatch_claimed`. The foundation does not silently absorb double-claims; it surfaces them as errors so callers cannot mistakenly believe two acquisitions succeeded.

## 4. Lock manager

### 4.1 Claim handles

A **claim handle** is a persistent row asserting "holder H has acquired scope S for purpose P." It carries:

- `id` — opaque foundation-generated identifier (also the correlation token producers see across verbs).
- `holder` — runner instance that acquired it.
- `scope` — opaque bytes (canonicalized by the producer; see service-protocol contract).
- `address` — opaque bytes returned by the producer at acquisition.
- `payload` — opaque bytes returned by the producer at acquisition.
- `purpose` — opaque tag identifying which producer / which acquisition path created the handle.
- worker-request correlation — links the handle to the worker request that caused its creation.

The foundation introspects only `id`, `holder`, `scope`, and the worker-request correlation. `address`, `payload`, and `purpose` are inert per **foundation invariant 20** *(claim content is inert)* — read by the modeling layer at substitution time only, never logged, formatted, or otherwise touched by the foundation.

### 4.2 Scope conflict

Two claim handles **conflict** iff their scope bytes are byte-equal. Canonicalization is the producer's responsibility (per service-protocol contract); the foundation compares as bytes.

The foundation does not parse, glob, range-match, or otherwise interpret scope bytes. Byte-equality is the *only* conflict primitive.

### 4.3 Acquisition-time predicate

When a runner attempts to insert a claim handle for scope S held by holder H, the foundation MUST reject the insert if any other claim handle with byte-equal scope exists held by a different holder, *unless* a modeling-layer-supplied coexistence predicate (typically derived from declared write-semantics) permits the pair to coexist.

The conflict predicate is evaluated against the (producer, scope-bytes) pair using the `realized_write_semantics` value carried on each claim handle. Per the byte-equal-scope uniformity invariant, all claim handles with the same (producer, scope-bytes) MUST have identical `realized_write_semantics`. The producer-declared envelope from `Capabilities()` constrains which `realized_write_semantics` values may be returned.

The conflict predicate is evaluated at insert time only. Existing handles are not retroactively rejected on policy change.

### 4.4 Claimant-guarded release

**Foundation invariant 4** *(claimant-guarded release)*: every operation that deletes a claim handle, or that nullifies a worker-request's `claimed_by`, MUST be conditioned on `holder = <expected>` (or its dispatch-row equivalent). Stale orphan sweeps and verb-driven releases share the same guard. The foundation MUST NOT expose any path that removes a holder's row without holder verification.

### 4.5 Orphan reaping

**Foundation invariant 6** *(orphan cutoff)*: a runner instance that has not heartbeated within `5 × heartbeat_interval` is considered dead. Its outstanding active-phase worker-requests become eligible for reap.

- Reaping a worker request: revert `phase` to `pending` and nullify `claimed_by` (claimant-guarded), making the row dispatchable again. Claim-handle children persist; producer-side TTL handles their cleanup.
- Held-phase worker-requests are NOT reaped at the worker-request level. Their resolution is auto-terminal (§5.5); their claim-handle children are cleaned up via producer TTL per service-protocol obligation.

The foundation does NOT call producer verbs (Commit/Abandon/Release) during orphan reap; producers are responsible for their own state TTL via service-protocol obligations.

## 5. Integration

The integration layer ties the cascade engine to the lock manager. The primary integration type is `foundation.integration.Conductor`. Five mechanisms:

### 5.1 Atomic acquisition

When a runner takes ownership of a worker request that requires claim handles, the dispatch claim and all required claim-handle inserts MUST occur in one foundation-side transaction.

**Foundation invariant 10** *(acquisition atomicity, foundation side)*: either the worker request becomes claimed AND all claim handles are inserted with their producer-returned addresses, or none of these.

The foundation transaction does not extend into the producer. Producer-side state mutations commit in the producer's own transaction. Atomicity across the boundary is achieved by orphan reapers on both sides — foundation-side reaping handles partial foundation commits; producer-side TTL handles partial producer commits. The cross-boundary invariant is not "both sides commit together" but "any orphan state on either side is eventually cleaned up."

### 5.2 Producer call inside the acquisition transaction

**Foundation invariant 15** *(`Open` fires inside the foundation-side acquisition transaction)*: the producer's acquisition call is invoked between the claim-handle row INSERT and the foundation-side COMMIT. Its return values populate the inserted row's `address`, `payload`, and `realized_write_semantics` fields.

This is the *only* way claim-handle `address`, `payload`, and `realized_write_semantics` are written. They cannot be back-filled, mutated, or refreshed after the acquisition transaction commits.

### 5.3 Multi-handle deterministic order

**Foundation invariant 3** *(sorted multi-handle acquisition)*: when a worker request requires multiple claim handles, all required handles MUST be acquired in a deterministic sort order shared by all runners. The current implementation sorts by (kind, scope-bytes, purpose). This prevents cross-runner deadlock under contention.

### 5.4 Verify-before-run

**Foundation invariant 5** *(verify-before-run)*: immediately before invoking a service implementation that acts on a claim, the runner MUST re-read the worker request's `claimed_by` and confirm it equals the runner's identity. If ownership has moved (an orphan reaper raced ahead), the runner MUST bail without acting on the claim.

### 5.5 Auto-terminal resolution

**Foundation invariant 13** *(auto-terminal aggregate-outcome resolution)*: at any modeling-layer-defined "holding subgraph completion" point, the foundation:

1. Locks the relevant claim handle row (`SELECT … FOR UPDATE`).
2. Evaluates the modeling-layer-supplied aggregate outcome (typical mapping: any-failed → abandon; all-completed → commit).
3. Fires exactly one terminal verb on the producer (Commit or Abandon).
4. Deletes the claim-handle row, claimant-guarded.

Resolution is single, terminal, and aggregate-outcome-driven. The foundation never emits partial resolutions or first-delete-wins / last-released-wins reconciliations.

The "holding subgraph" itself is a modeling-layer concept; the foundation requires only that the modeling layer supplies (a) a completion predicate and (b) an aggregate-outcome predicate. The foundation contributes the row lock, the verb dispatch, and the row cleanup.

### 5.6 Lifecycle-handler-driven resolution

The foundation's terminal handler dispatches lifecycle-handler resolutions supplied by the modeling layer (`on_acquire_unavailable`, `on_executor_complete`, `on_executor_blocked`, `on_executor_errored`). Per the reactive-loops + lifecycle-handlers spec at `.ok-planner/specs/2026-05-05-reactive-loops-and-lifecycle-handlers-design.md`, each handler resolves to one of: `pass` (fresh terminal with `last_outcome=passed`, no cascade), `error` (route through the modeling layer's error-class chain), or `retry` (re-enqueue without retry counter bump). The foundation provides the dispatch primitive; the modeling layer owns the resolution semantics and the per-error-class routing.

The foundation also exposes a `last_outcome` column on the node-state table (`rimsky_nodes.last_outcome`) — a sibling field to `state` capturing the resolution flavor of the most recent terminal (`fresh_changed | fresh_unchanged | passed | pure_cascade | failed`). The cascade-firing gate is `last_outcome == fresh_changed` (functionally identical to the prior `t.Changed` gate under default `by_changed`, divergent under `always_propagate` / `never_propagate`). The column is a foundation-layer surface — the cascade engine consults it; the modeling layer assigns the values per its lifecycle-handler resolution.

## 6. Persistence contract

### 6.1 Tables owned by the foundation

The foundation defines and owns the following persistence schema:

- **Node-state table.** Per-node `has_value` / `has_outstanding_request` / `auto_recovers` plus a `last_outcome` sibling field expressing the most recent terminal's resolution flavor (`fresh_changed | fresh_unchanged | passed | pure_cascade | failed`), plus modeling-layer correlation columns. Implementation: `rimsky_nodes` (split-owned with the modeling layer per modeling contract §11.3). The cascade-firing gate is `last_outcome == fresh_changed`.
- **Worker-request table.** Outstanding work, with `claimed_by`, heartbeat timestamp, `phase` column expressing active/held lifecycle. Implementation: `rimsky_worker_request`.
- **Claim-handle table.** Acquired scopes, with `holder`, `scope_data BYTEA`, address, payload, purpose tag, `realized_write_semantics`, `is_held` flag, worker-request FK. Implementation: `rimsky_claim_handle`.

The modeling layer MAY add columns to the node-state table for its own tracking (e.g., frame correlation), but MUST NOT add behavior that bypasses foundation invariants over them.

The modeling layer's own tables (templates, instances, schedules, frames, attributes-schemas, lifecycle-idempotency tracking, etc.) are not foundation-owned.

### 6.2 Driver protocol

The foundation publishes a driver interface set scoped to foundation tables only:

- **Cascade** — node-state CRUD; cascade-signal application.
- **WorkerRequests** — worker-request CRUD; claim/heartbeat/release/EnterHeldPhase/Complete operations; claim-handle CRUD; conflict-predicate query; orphan reap.
- **AdvisoryLocker** — distributed-lock primitives needed by the foundation (advisory lock for migrations; advisory lock for the dispatch tick).

The `WorkerRequests` interface owns both the worker-request rows and their child claim-handle rows; see §8.4 of `2026-05-04-layer-crystallization-design.md` for the methods.

These are *the* interfaces foundation code uses. Modeling-layer driver methods (template lookup, schedule queries, frame state) live in modeling-owned interfaces composed alongside the foundation set.

### 6.3 Migrations

**Foundation invariant 8** *(session advisory lock on migrations)*: the foundation's migration runner holds a session-scoped advisory lock for the duration of a migration batch. The lock is released at session close. Postgres uses `pg_advisory_lock`; SQLite uses an in-process mutex (single-process is the only supported topology for SQLite).

Foundation migrations and modeling-layer migrations may coexist in the same migration directory, but MUST be ordered such that foundation migrations precede modeling migrations that depend on them.

### 6.4 Tick

**Foundation invariant 7** *(advisory lock on dispatch tick)*: the foundation's dispatch tick acquires an advisory lock per-tick. Postgres uses `pg_try_advisory_lock(SCHEDULER_TICK_KEY)`; SQLite uses `sync.Mutex`. Replicas that fail to acquire skip the tick.

The "dispatch tick" is the foundation's name for the periodic loop that scans for eligible worker requests and emits dispatch claims. Modeling-layer ticks (frame ticks, schedule ticks) MAY share this loop's outer scaffolding but are conceptually separate.

## 7. Foundation invariants (catalog)

The following invariants are foundation-layer and stable until v1. Code locations are not maintained here; see `@blessed-invariant N` annotations in source.

- **1.** State machine rejects illegal transitions.
- **2.** Dispatch claim brackets the running window.
- **3.** Multi-handle acquisition uses deterministic sorted order.
- **4.** Claimant-guarded release on every claim-handle deletion and worker-request `claimed_by` nullification.
- **5.** Verify-before-run.
- **6.** Orphan cutoff is `5 × heartbeat_interval` for active-phase worker requests.
- **7.** Advisory lock on the dispatch tick.
- **8.** Session advisory lock on migrations.
- **9a.** Lock state lives only in the foundation persistence layer; service implementations do not persist lock state.
- **10.** Acquisition is atomic on the foundation side: worker-request claim + claim-handle inserts + address record-back commit together or not at all.
- **13.** Auto-terminal resolution is single, claim-handle-row-locked, and aggregate-outcome-driven.
- **15.** Producer acquisition call (`Open`) fires inside the foundation-side acquisition transaction.
- **20.** Claim content (address, payload, scope) is inert in the foundation; introspection only happens at modeling-layer leaf-extraction.

Service-protocol invariants (9b, write-semantics uniformity, envelope conformance) and modeling-layer invariants (11 userdata-opaque, 12 attributes-validate-twice) live in their respective layer contracts and are out of scope here. Invariant 14 was retired in v3.

## 8. What is explicitly NOT in the foundation

The following are **not** foundation concerns. Code in foundation packages MUST NOT import or reference them; foundation persistence MUST NOT track them.

- **Templates** and the content-addressing scheme over template specs.
- **Instances** and the instance-key namespace.
- **Frames** and the frame-resolution model (`coalesce` / `serial_queue`).
- **Schedules** and cron-driven invalidation.
- **Attributes** schemas, substitution, and validation.
- **The public state-machine vocabulary** (`fresh`/`stale`/`running`/`failed`). Foundation uses bits + annotation.
- **The public message vocabulary** (`invalidate` / `recalculate`). Foundation has one cascade signal; recalculation is a per-node action, not a message.
- **The public error-action vocabulary** (`retry` / `invalidate(targets)` / `give_up`). Foundation has one parameterized terminal-with-cascade-signal primitive.
- **Service protocol surface details** (`ClaimProducer`, `Executor`, `LifecycleSubscriber` wire shapes). Foundation declares the *hooks* it requires (acquisition call, terminal verbs, dispatch call); the wire-protocol contract says what those hooks call.
- **The control-plane API**, the operator YAML, and the CLI.
- **Lifecycle events** (`OnTemplateRegistered/Deployed/Undeployed/Deregistered/OnInstanceCreated/Terminated`). These are control-plane fan-out; not foundation.

## 9. Stability commitments

Until v1:

- Foundation invariants 1–10, 13, 15, 20 are stable. They MAY be amended by a successor design doc; ad-hoc amendment is forbidden.
- Foundation table shapes are stable in *intent*. The pre-v1 break-freely rule applies to physical layout; the *invariants over the data* survive.
- The driver interface set (Cascade, WorkerRequests, AdvisoryLocker) is the stable contract foundation code programs against. Implementations may change; the interface set is committed.

After v1:

- Foundation surface is frozen. Changes require a major-version bump.

## 10. Implementation status

The code in `github.com/fallguy/rimsky/foundation` matches this contract. Cross-references from foundation packages to specific files are not maintained in this contract; see `@blessed-invariant N` annotations in source for current code locations.

## 11. Open questions

All open questions from the 2026-05-03 draft are closed.

---

*End of contract.*
