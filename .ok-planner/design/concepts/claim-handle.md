---
concept: claim-handle
status: as-is
aliases: []
---

# Claim handle

## What it is

The claim handle is the rimsky-persistence-layer shadow of a `concept:claim` — the layer split between the protocol noun and this persistence noun is defined there. The claim handle is the rimsky-side ledger row representing one acquired claim (or named-lock acquisition). It records the lock identity (kind and name), the acquired claim scope, an expiry and held marker, the realized write semantics, and a nullable link back to the holding node-run that survives the parent's deletion rather than cascading.

The row also carries:

- A nullable self-referential parent pointer to the parent claim in a sub-claim chain. Null for top-level claims; non-null for sub-claims spawned via the producer's split-scope verb. Auto-terminal walks bottom-up over this pointer.
- A snapshotted fan-out aggregation policy plus expected, committed, and abandoned child-count counters, present on parent rows in a claim-tree fan-out. Drive the parent's own terminal-resolution walk (see `concept:claim-tree`).
- A lifetime selector choosing between subgraph and durable behavior. Selects auto-terminal behavior: subgraph rows are reaped by the retention sweep at cutoff after promotion; a committed-durable row is exempt from the retention sweep and reaped only by explicit Release (asset surface), but an abandoned-durable row is swept like any other abandoned row.
- A version identifier — the canonical version returned by the producer's commit verb, persisted from the data-processing commit-candidate response at settlement or applied best-effort when the outbox-delivered base commit reports one; surfaces in lineage records (claim-terminal kind) and, for data-processing-capable claims, asset version-history queries.
- An opaque producer candidate handle from the data-processing begin-candidate verb; lives on sub-claim rows for fan-out-with-data-processing flows. Threaded through to the leaf executor's execution request.

The row carries a state field with a closed three-value lifecycle — active (currently held by a supervisor; liveness observed by the supervisor), committed (commit disposition recorded and the producer commit enqueued on the terminal-verb outbox; row preserved past terminal), and abandoned (abandon disposition recorded and enqueued at natural resolution or sibling/descendant force-cancel, or promoted directly without the producer verb when an instance-terminate force-abandons the row's still-active in-flight claim; row preserved) — plus a resolved-at timestamp recording when the row exited active. The retention sweep filters on the resolved-at timestamp.

Holder-consistency is an invariant of the row: an active row holds a holder-supervisor reference, and a non-active row holds none.

State transitions are claimant-guarded: an active row moves to a settled state atomically by an update that matches both the row id and the holding supervisor; mismatch returns an illegal-transition error. Revival from a terminal state back to active is not permitted.

Row deletion takes several shapes:

- **Active-row deletion**: claimant-guarded — the delete matches both the row id and the holding supervisor. Covers the verify-before-run ownership bail, the orphan reaper's claimant-guarded delete of an active row past its expiry, and the ordinary release of a named-lock acquisition at its holder's terminal.
- **Non-active-row deletion** (retention sweep, asset Release path): absence-guarded — the row carries no holder-supervisor reference by construction, so no per-row claimant guard is meaningful. Serialized across replicas via the scheduler-tick advisory lock (for the retention sweep) or via the operator-driven asset-delete endpoint (for the asset Release path).
- **Structural deletion**: a claim-handle row is removed as a foreign-key cascade when its holding node is deleted, independent of the row's own state.

## Purpose

The single source of truth for "who holds what right now." Conflict-check predicates walk this table only; orphan reaping operates on this table; held-claim resolution promotes rows in this table to a settled state (the verify-before-run ownership bail is the deletion exception). Decouples rimsky-side bookkeeping from producer-side state.

## Boundaries

Owns: the lock-state ledger, claimant-guarded mutation predicates, the held-flag plus null-on-parent-delete reference shape that lets held handles outlive their parent. Does NOT own: producer-internal state (see `concept:claim-producer`), liveness tracking (those are on `concept:node-run`), claim-disposition verb dispatch (see `concept:auto-terminal`). Adjacent: `concept:claim`, `concept:node-run`, `concept:auto-terminal`, `concept:supervisor`, `concept:orphan-reaper`, `concept:inertness`, `concept:claim-co-holdership`, `concept:claim-lifetime`, `concept:claim-tree`, `concept:asset`.

## Invariants

- Every active-row mutation — whether it deletes the row, nullifies its holder reference (promote to a settled state, the ownership-bail delete), or repoints a field on an otherwise-active row (address, payload, realized write semantics, claim scope, version id, aggregation policy, child-count counters, or the linked node-run) — matches the holding supervisor in its predicate (invariant 4 — claimant-guarded release). There is no field-repoint carve-out.
- Non-active-row deletion (retention sweep, asset Release path) is absence-guarded: the row has a null holder-supervisor reference by construction; the row-discovery query filter (committed-durable rows for Release; committed-subgraph-or-abandoned rows, excluding committed-durable, for the retention sweep) substitutes for the per-row claimant check.
- The holder-supervisor reference is set on active rows (per the first CHECK constraint), null on terminal rows (per the second CHECK constraint).
- The node-run reference nulls on the parent's deletion (rather than cascading) so terminal handles survive their parent's deletion until either the retention sweep reaps them or (for durable-committed) the asset Release path fires.
- Lock state lives only in this ledger; producers do not persist or shadow it (invariant 9a).
- The orphan reaper sweeps active, expired rows but does NOT call the producer's abandon verb; the bail path is the deliberate exception that DOES fire abandon. The reaper skips terminal rows because those are owned by the retention sweep or by explicit Release.

### Held variant

A **held** claim is a claim whose lifetime extends past its acquirer's terminal to cover the holding subgraph: the acquirer plus every directly-declared co-holder. Marked by the held flag on the handle row. Per-member state tracked in co-holder rows keyed by claim handle plus holder run, each carrying an active/completed/failed state.

The holder key is the holder run (referencing the node-run ledger); holders are runs, not nodes. The acquirer's own holder row is inserted at acquire-time; co-holder rows (declared via `holds:`) are inserted at the co-holder's own acquire-time.

**Held is a first-class node-run state, not just a claim-handle phase.** When a node-run's terminal includes a held=true claim, the node-run itself transitions to state `held` (one of the seven states in `concept:node-run`'s state machine) and stays there until auto-terminal Commit or Abandon resolves. When a member of the holding subgraph reaches its own held terminal, the cascade walker fires a member-filtered walk to the rest of the holding subgraph immediately — that member-directed signal is how a co-holder member is triggered. The walk to subscribers outside the holding subgraph is deferred to the auto-terminal handler, per `decision:held-as-state-not-phase` and `concept:auto-terminal`; those non-member subscribers see `terminal/success` only at Commit and `terminal/error/abandoned` only at Abandon.

While in state `held`, the node-run is in the in-flight set (`{pending, stale, running, held, parked}`) and is sealed against cascade-driven mutation per `concept:cascade`. Cascade events targeting a held node-run create a NEW cascade-driven pending row; the held row is left untouched.

Held-variant invariants:

- Aggregate outcome is strict: all-completed → `Commit`; any-failed → `Abandon`.
- Auto-terminal fires exactly once per held claim; each backend enforces this via its own row-level serialization, whether a locking select or the backend's single-writer transaction ordering.
- Held handles persist across the node-run parent's deletion (the reference nulls rather than cascading).
- The co-holder state field forbids values outside {active, completed, failed}; once a holder is `failed`, the aggregate is `failed`; the held variant does not support discard-then-retry recovery.
- The held node-run's `running → held` transition fires a member-filtered cascade to the rest of the holding subgraph immediately. The walk to non-member subscribers is deferred to the auto-terminal handler and fires at the same atomic moment the handle row is promoted: Commit → `terminal/success` walk; Abandon → `terminal/error/abandoned` walk.
- **Held-durable claim handles persist across instance dispatches.** A committed-durable claim handle is not reaped by the retention sweep; released only via the operator-driven asset-delete endpoint or via instance deletion (permitted only once the instance has reached a terminal state), both of which enqueue the producer's release verb on the terminal-verb outbox and drop the row at decision time. Instance termination alone abandons the instance's still-active in-flight claims but does not release committed-durable rows. The orphan-claim reaper skips non-`active` rows.
- **Poison rule (forward-propagation through abandoned claims).** Aggregate outcome resolves to `Abandon` the moment any holder reaches `failed` — not when the last holder settles. At that resolution moment, every still-active holder's co-holder row is marked `failed`; the held claim handle is promoted to `abandoned` and Abandon fires. Holders whose own execution is still in flight at this moment will, when they eventually settle, transition to node-run state `failed` with `terminal/error/abandoned` regardless of what their executor returned — their successful or failed terminal cannot un-poison the aggregate, and their outputs are treated as part of an abandoned coordinated unit. The poison rule preserves the held-claim's transactional integrity: once one part of the coordination has failed, every other part is treated as failed for cascade purposes.
- **Member-vs-non-member cascade routing at Commit and Abandon.** The deferred cascade walks fired by the auto-terminal handler (`terminal/success` for Commit, `terminal/error/abandoned` for Abandon) deliver the resolution signal only to subscribers that are NOT themselves members of the holding subgraph for this claim. Members of the holding subgraph coordinate internally through the held machinery: a member's own held terminal fires a member-filtered cascade to the rest of the holding subgraph immediately, which is how a co-holder's `held → fresh` or `held → failed` transition is triggered. Members do not also receive the deferred resolution signal that the auto-terminal handler fires to non-members at Commit or Abandon; delivering that signal to members too would double-fire the receiver. Non-members (subscribers outside the holding subgraph) see the resolution signal as a single deferred event at the auto-terminal moment, and never observe a held node-run's terminal before that.

### Authoring: held vs unheld

A template declares co-holders on each node's `holds:` clause; the claim opened by a node becomes "held" implicitly when one or more downstream runs declare it as a co-held claim. The author does not flip a flag — the holding-subgraph membership is derived from the template's edges. Auto-terminal fires for the claim when every run in the holding subgraph (acquirer plus co-holders) has reached a non-active state.

### Held-variant antipatterns

The held variant is a **lifetime-extension mechanism for one claim**, not a multi-node transactional unit. Authors sometimes reach for it expecting more than it offers:

- **No rollback on `Abandon`.** When the aggregate outcome is `failed` and rimsky fires `Abandon` on the held claim, rimsky tells the producer "the consumer of this claim failed"; the producer decides what to do with its own state per its own configuration. Rimsky does not orchestrate rollback, does not undo writes to the staging area, does not reverse-cascade attribute writes performed by other holding-subgraph members. If the workflow requires multi-resource rollback, encode that in a producer or a downstream compensating node — not in the holding-subgraph mechanism.
- **Not a transactional unit.** The holding subgraph is the set of runs over which one claim's lifetime spans. It is not a "transaction" over the claim *and* every other side effect performed by its members. Treat it as scope-lifetime extension; treat cross-resource atomicity as something the template's authors compose explicitly.
- **No partial commits or first-delete-wins.** There is exactly one resolution per held claim, and the rule is all-succeeded → `Commit`, any-failed → `Abandon`. Rimsky does not orchestrate partial commits, partial rollbacks, or reconciliation between simultaneously-resolving holding subgraphs.
