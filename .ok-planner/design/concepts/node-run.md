---
concept: node-run
status: as-is
aliases: []
---

# Node-run

## What it is

The node-run row is the parent row for one execution of one node within a frame. Its state machine is a single seven-state column (no separate phase column): `pending`, `stale`, `running`, `held`, `parked`, `fresh`, `failed`. The row carries a supervisor binding populated only while the run is actively dispatched, a non-null frame reference, the required-claim-producers list, and the parked-reason metadata when applicable.

The row carries liveness and async-callback fields covering the run's last-progress timestamp and the async-acknowledgement identity. The progress timestamp is bumped by scratch writeback and by keepalive notifications; the async-acknowledgement identity is populated when the executor returns an await-callback outcome and is consulted by the callback handler to correlate inbound notifications back to the dispatch row.

The row also carries a nullable reference to a preceding dispatch row, set whenever a new dispatch is enqueued to follow a predecessor one (under any of the predecessor-bearing dispositions — stale-recovery, recalculate). Policy retry does not create a new row (see `decision:in-place-retry`); the retry loop reuses the same dispatch row in place. Optional scratch fields carry executor-attached opaque bytes per dispatch, with spill following `concept:blob-backend`. The executor sets scratch either by attaching scratch bytes to the settling terminal outcome or mid-dispatch via a scratch-callback notification, the dispatch protocol's sole mid-dispatch callback route; both writes persist on the dispatch row that received them. When a subsequent dispatch row is created for the same node and the new row carries a non-null predecessor reference, the enqueue path copies scratch from the predecessor dispatch row onto the new row at row creation, and the executor reads it from its own row on next dispatch.

A tags representation is populated from the settling terminal verdict.

The row carries the run-tree extension and all state-bearing fields for the node-run. Per-node attributes are a child record keyed to this row and cascade-deleted with it; modulo derived caches, every state-bearing field for a node-run lives on this row or cascades from it. The parent/child relationship lives on the run-scope record (per `concept:run-scope`), referenced from a non-null run-scope field on the row:

- A non-null run-scope reference (per `concept:run-scope`). All scoping — parent/child relationship for fan-out, sub-graph membership for delegation — is expressed through this reference chain.
- An aggregation-policy snapshot — populated only on fan-out parent runs, snapshotted from the node's fan-out error-policy spec at fan-out dispatch time (not at row creation); encodes the failure policy for parent-run aggregation. Non-fan-out runs and fan-out children leave the field unset.
- A state field — the node-run's single unified lifecycle state (see "Seven-state state machine" below).
- A sequence field — monotonic per (node_id, run_scope_id), assigned at row creation. The dispatcher's candidate order is enqueue-time primary; sequence breaks ties among candidates enqueued at the same instant within the same (node, run-scope) — it is not a global ordering key. Sequence also drives the gate evaluator's predecessor lookup for bag composition and the latest-run lookup that operator surfaces project into the per-state categorical summary (the per-scope monotonicity makes "the latest run for this node in this scope" well-defined within the RunScope; RunScopes never span frames per `concept:run-scope`, so per-scope monotonicity is also per-frame).
- A creation-reason field — `cascade | operator_invalidate | recalculate | message_delivery`. Determines whether the row participates in cascade-walker accumulation (cascade only), goes through `pending` (cascade only), and is subject to per-template `cascade_mode` rules (cascade only). Non-cascade rows are created directly in state `stale` with the carry-forward bag (see `decision:non-cascade-direct-to-stale`). The `message_delivery` reason marks a row created when a named message is delivered to its message-receiver-node; the bag is the message body (not carry-forward), and the run dispatches via the empty-executor `pure_cascade` settle path (see `concept:message`). Policy retry is in-place on the existing row (see `decision:in-place-retry`) — no new row created.
- A last-outcome field — the gate for cascade-firing.
- Parked-reason metadata — parked-state taxonomy (see `concept:parked-state`).
- Policy-evaluation cursor — a single per-dispatch `retry_counter` field that holds the error-policy retry count (see `concept:error-policy` and `decision:in-place-retry`). Initialized to zero at row creation; mutated only during executor retry loops on this row.

## Seven-state state machine

| State | Meaning | Bag persisted? | Dispatch-eligible? |
|---|---|---|---|
| `pending` | created by cascade walker, waiting for upstream cascades to settle (wait-set draining) | no | no |
| `stale` | gates cleared, bag built and persisted, ready to dispatch | yes (frozen) | yes (subject to dispatcher serialization gate) |
| `running` | claimed by dispatcher, executor in flight (includes async-callback wait) | yes (frozen) | no (in-flight) |
| `held` | executor returned with held=true claim; cascade paused awaiting auto-terminal commit/abandon | yes (frozen) | no (in-flight via held) |
| `parked` | executor returned park terminal | yes (frozen) | no (in-flight via park) |
| `fresh` | settled successfully — TERMINAL, no outgoing transitions | yes (final) | no (settled) |
| `failed` | settled with terminal/error or auto_terminal_abandon — TERMINAL, no outgoing transitions | yes (final) | no (settled) |

Transitions:

```
pending → stale      (gate_cleared: wait-set drained + no in-flight subscribed upstream)
pending → failed     (instance_killed)

stale → running      (dispatch_claimed — second leg of the split claim, after the re-read confirms ownership)
stale → fresh        (pure_cascade, acquire_pass)
stale → failed       (dispatch_impossible, policy_give_up, instance_killed)

running → fresh      (handler_complete with no active claim participation; handler_pass)
running → held       (handler_held — runner classifies a terminal outcome with active claim participation; fanout_dispatched — fan-out parent has yielded its synchronous dispatch phase and is acquirer of an active claim handle awaiting child aggregation, per `decision:held-as-state-not-phase`)
running → parked     (handler_park)
running → failed     (policy_give_up, auto_terminal_abandon, instance_killed)

(`policy_retry` is in-place on the existing row with no state transition firing — claims and bag preserved; see `decision:in-place-retry`.)

held → fresh         (auto_terminal_commit — at this moment cascade fires terminal/success)
held → failed        (auto_terminal_abandon — at this moment cascade fires terminal/error/abandoned)
held → failed        (instance_killed)

parked → stale       (deadline_resume — bag preserved on the same row, re-eligible for dispatch)
parked → failed      (park_timeout, instance_killed)
```

The dispatcher's claim is a two-leg operation: the first leg stamps a non-null claim on the row while leaving state at `stale`; the second leg re-reads the claim out-of-band, then transitions `stale → running` only if the row is still owned by this supervisor. The serialization-gate predicate covers any row with a non-null claim plus all rows in `{held, parked}`, so a stale-with-claim row blocks concurrent claims for the same (node, scope) — there is no "orphan window" where a row is `running` without a claim.

`fresh` and `failed` are terminal — no outgoing transitions. Cascade events targeting a settled (or in-flight) node-run create a NEW node-run instead; they never mutate the existing row.

The in-flight set is `{pending, stale, running, held, parked}`; runs in these states are sealed against cascade-driven mutation per `concept:cascade`'s in-flight-sealed invariant. **At most one node-run per (node, run-scope) is in a state past pending at any moment.** Two layers cooperate to enforce this. The gate evaluator's pending→stale precondition refuses to transition a pending row while any sibling for the same (node, run-scope) is in `{stale, running, held, parked}`; the pending row stays pending and re-evaluates when the sibling settles (via the post-settle sibling-kick). The dispatcher's claim-time gate is the second line of defense: it refuses to claim a stale row while any sibling is in `{running, held, parked}` or already carries a non-null claim. Multiple pending rows for the same (node, run-scope) can and do coexist (cascade-driven accumulation + non-cascade queued); only one transitions to stale at a time, and the next pending advances after the prior row terminates.

Every dispatch loads its persisted attribute bag (per `concept:attribute`) from its own row (no rebuild-at-dispatch branch). The bag is built at exactly one moment per row: either at the gate evaluator's pending→stale transition (cascade-driven rows; carry-forward + wait-set overlay), or at row creation (non-cascade rows; carry-forward only). The deadline-wake from `parked` reuses the bag that was persisted at the original dispatch — no special "resuming" state, since there is no rebuild branch to gate.

## Purpose

One queryable lifecycle row per node-run means every cross-process question ("is this run still active?", "what stores does it need?", "which frame is it in?", "has it gone stale?") is a SQL predicate over indexed columns. The frame ⊃ node-run hierarchy is the model: `concept:frame` is "one run of the cascade"; `concept:node-run` is the per-node execution within that frame.

**Run-tree**: node-runs are organized into RunScopes (per `concept:run-scope`) via the run-scope reference. The tree shape lives on the run-scope record via its parent-run-scope reference. Walking the RunScope tree from a leaf RunScope to the frame's root RunScope recovers the full execution stack for that frame. A run represents the dispatch of one node within one RunScope; a fan-out parent's children live in fanout-partition RunScopes (one per partition); a sub-graph's internal nodes live in a sub-graph RunScope. Trees may be arbitrarily deep: fan-out of fan-outs, sub-graphs containing fan-outs, fan-outs of sub-graphs. State aggregation walks bottom-up through the RunScope tree in a single state-propagation transaction.

## Boundaries

Owns: the seven-state machine and transitions, candidate-selection inputs, liveness fields (the run's last-progress timestamp and the async-acknowledgement identity), park fields, sequence + creation-reason columns; executor-attached opaque scratch bytes per dispatch; the predecessor-dispatch linkage across re-dispatches of the same node. Does NOT own: per-claim ledger rows (see `claim-handle`), per-holder subgraph state (see `claim-handle`), the parent-child run relationship (lives on the run-scope record per `concept:run-scope`), the cascade walker's accumulate-or-queue decision (lives in `concept:cascade`), the gate evaluator's pending→stale transition (lives in `concept:wait-set`), the dispatcher's serialization gate (lives at the dispatcher; conceptually anchored here by the in-flight state set). Adjacent: `claim-handle`, `frame`, `supervisor`, `parked-state`, `run-scope`, `terminal-tag`, `cascade`, `wait-set`.

## Invariants

- The frame reference is non-null — every node-run carries its frame (frames are the unit of cascade resolution).
- The supervisor binding is non-null only while the run is actively dispatched.
- Orphan reaper covers only actively-dispatched rows; parked, held, and pending rows are skipped explicitly (settled with respect to liveness). For active (running) rows the orphan signal is the supervisor's dispatch-channel connection state (sync dispatches) or quiet-period exceeded plus absolute-deadline exceeded, each enforced only when the corresponding deadline is non-zero.
- The state column is the single source of truth for lifecycle. No parallel phase column exists.
- The queue's claimant guard is unconditional: every claim-guarded queue verb (release, remove) verifies the caller's expected claimant against the row's recorded claim and errors on mismatch — a stale supervisor's late write can never clobber a reassigned row. Administrative callers with no claim to assert (pure-cascade settle, parked-row sweep) use an explicitly-named force variant; no sentinel argument value disarms the guard.
- The sequence column is monotonic per (node_id, run_scope_id) and is assigned exactly once at row creation; never rewritten. The latest run for a node within a RunScope is well-defined; RunScopes never span frames per `concept:run-scope`, so this also makes "the latest run in a frame" well-defined.
- The creation-reason column is set at row creation and never rewritten. It governs whether the row participates in cascade-walker accumulation, goes through pending, and is subject to mode rules.
- In-flight states (`pending`, `stale`, `running`, `held`, `parked`) are sealed against cascade-driven mutation per `concept:cascade`. Cascade events targeting an in-flight run create a new node-run; never mutate the existing one.
- Every row's persisted attribute bag (per `concept:attribute`) is built at exactly one moment: at the gate evaluator's pending→stale transition for cascade-driven rows, or at row creation for non-cascade rows. The bag is the executor's input on every invocation of the row, including deadline-wake from `parked` and in-place policy retry.
- The policy-evaluation cursor (a single per-dispatch `retry_counter`) is per-run state: initialized to zero at row creation and mutated only by error-policy evaluation within the runner loop on this row. A new node-run for the same node starts at zero (see `decision:in-place-retry`).
