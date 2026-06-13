---
concept: claim-handle
status: as-is
aliases: []
---

# Claim handle

## What it is

`claim` is the protocol-layer noun returned by a claim producer's open verb; `claim-handle` is the rimsky-persistence-layer noun for the same conceptual thing. They have different invariants by layer — `@blessed-invariant 20` (claim content inert) gates content; `@blessed-invariant 4` (claimant-guarded release) gates the persistence row.

The claim handle is the rimsky-side ledger row representing one acquired claim (or named-lock acquisition). It carries: a lock kind (named or claim-scope), the lock name, the claim scope bytes, a nullable holder-supervisor reference (see state below), an expiry timestamp, a held flag, the realized write semantics, and an optional node-run reference whose FK sets to null on the parent's deletion.

The row also carries:

- A nullable self-referential parent pointer to the parent claim in a sub-claim chain. Null for top-level claims; non-null for sub-claims spawned via the producer's split-scope verb. Auto-terminal walks bottom-up over this pointer.
- A lifetime field — `subgraph` (default) or `durable`. Selects auto-terminal behavior: `subgraph` rows are reaped by the retention sweep at cutoff after promotion; `durable` rows are reaped only by explicit Release (asset surface).
- A version identifier — the canonical version returned by the producer's commit verb for DataProcessing-capable claims; surfaces in lineage records (claim-terminal kind) and asset version-history queries.
- An opaque producer candidate handle from the DataProcessing begin-candidate verb; lives on sub-claim rows for fan-out-with-DataProcessing flows. Threaded through to the leaf executor's execution request.

The row carries the **state column**:

- A state field constrained to one of three values — the 3-state lifecycle.
  - `active`: currently held by a supervisor, heartbeating. The holder-supervisor reference is set.
  - `committed`: producer commit fired; row preserved past terminal. The holder-supervisor reference is null.
  - `abandoned`: producer abandon fired (natural or force-cancel); row preserved. The holder-supervisor reference is null.
- A resolved-at timestamp recording when the row exited `active`. Null while `active`; set to the promotion time. The retention sweep filters on this column.

Two CHECK constraints enforce holder-consistency:

- Active rows must have a holder.
- Non-active rows must not have a holder.

State transitions are claimant-guarded via the promote operation: the update sets the state, nulls the holder-supervisor reference, and sets the resolved-at timestamp in one atomic statement; a zero-rows-affected result returns an illegal-transition error. Revival from a terminal state back to active is not permitted at the Go layer.

Row deletion has two shapes:

- **Active-row deletion** (the verify-before-run ownership bail, performed inside the unified resolution engine under its ownership-bail source): claimant-guarded — the delete predicate matches both the row id and the holding supervisor.
- **Non-active-row deletion** (retention sweep, asset Release path): absence-guarded — the row has a null holder-supervisor reference by construction, so no per-row claimant guard is meaningful. Serialized across replicas via the scheduler-tick advisory lock (for the retention sweep) or via the operator-driven asset-release endpoint (for the asset Release path).

## Purpose

The single source of truth for "who holds what right now." Conflict-check predicates walk this table only; orphan reaping operates on this table; held-claim resolution deletes from this table. Decouples rimsky-side bookkeeping from producer-side state.

## Boundaries

Owns: the lock-state ledger, claimant-guarded mutation predicates, the held-flag plus null-on-parent-delete reference shape that lets held handles outlive their parent. Does NOT own: producer-internal state (see `concept:claim-producer`), heartbeats (those are on `concept:node-run`), claim-disposition verb dispatch (see `concept:auto-terminal`). Adjacent: `concept:claim`, `concept:node-run`, `concept:auto-terminal`, `concept:supervisor`, `concept:orphan-reaper`, `concept:inertness`.

## Invariants

- Every active-row mutation (promote, heartbeat-extend, the ownership-bail delete) matches the holding supervisor in its predicate (`@blessed-invariant 4` — claimant-guarded release).
- Non-active-row deletion (retention sweep, asset Release path) is absence-guarded: the row has a null holder-supervisor reference by construction; the row-discovery query filter (committed-durable rows for Release; committed-or-abandoned rows for the retention sweep) substitutes for the per-row claimant check.
- The holder-supervisor reference is set on active rows (per the first CHECK constraint), null on terminal rows (per the second CHECK constraint).
- The node-run reference nulls on the parent's deletion (rather than cascading) so terminal handles survive their parent's deletion until either the retention sweep reaps them or (for durable-committed) the asset Release path fires.
- Lock state lives only in this ledger; producers do not persist or shadow it (`@blessed-invariant 9a`).
- The orphan reaper sweeps active, expired rows but does NOT call the producer's abandon verb; the bail path is the deliberate exception that DOES fire abandon. The reaper skips terminal rows because those are owned by the retention sweep or by explicit Release.

### Held variant

A **held** claim is a claim whose lifetime extends past its acquirer's terminal to cover the holding subgraph: the acquirer plus every directly-declared co-holder. Marked by the held flag on the handle row. Per-member state tracked in co-holder rows keyed by claim handle plus holder run, each carrying an active/completed/failed state.

The holder key is the holder run (referencing the node-run ledger); holders are runs, not nodes. The acquirer's own holder row is inserted at acquire-time; co-holder rows (declared via `holds:`) are inserted at the co-holder's own acquire-time.

Held-variant invariants:

- Aggregate outcome is strict: all-completed → `Commit`; any-failed → `Abandon` (`@blessed-invariant 13`).
- Auto-terminal fires exactly once per held claim, race-safe via a row-level select-for-update.
- Held handles persist across the node-run parent's deletion (the reference nulls rather than cascading).
- The co-holder state field forbids values outside {active, completed, failed}; once a holder is `failed`, the aggregate is `failed` (no discard-then-retry recovery in scope).
- **Held-durable claim handles persist across instance dispatches** (`@blessed-invariant 22`). A committed-durable claim handle is not reaped by the retention sweep; released only by explicit operator action (the asset-release endpoint) or instance termination (the held-durable-release path). The orphan-claim reaper skips non-`active` rows.

### Authoring: held vs unheld

A template declares co-holders on each node's `holds:` clause; the claim opened by a node becomes "held" implicitly when one or more downstream runs declare it as a co-held claim. The author does not flip a flag — the holding-subgraph membership is derived from the template's edges. Auto-terminal fires for the claim when every run in the holding subgraph (acquirer plus co-holders) has reached a non-active state.

### Held-variant antipatterns

The held variant is a **lifetime-extension mechanism for one claim**, not a multi-node transactional unit. Authors sometimes reach for it expecting more than it offers:

- **No rollback on `Abandon`.** When the aggregate outcome is `failed` and rimsky fires `Abandon` on the held claim, rimsky tells the producer "the consumer of this claim failed"; the producer decides what to do with its own state per its own configuration. Rimsky does not orchestrate rollback, does not undo writes to the staging area, does not reverse-cascade attribute writes performed by other holding-subgraph members. If the workflow requires multi-resource rollback, encode that in a producer or a downstream compensating node — not in the holding-subgraph mechanism.
- **Not a transactional unit.** The holding subgraph is the set of runs over which one claim's lifetime spans. It is not a "transaction" over the claim *and* every other side effect performed by its members. Treat it as scope-lifetime extension; treat cross-resource atomicity as something the template's authors compose explicitly.
- **No partial commits or first-delete-wins.** There is exactly one resolution per held claim, and the rule is all-succeeded → `Commit`, any-failed → `Abandon`. Rimsky does not orchestrate partial commits, partial rollbacks, or reconciliation between simultaneously-resolving holding subgraphs.
