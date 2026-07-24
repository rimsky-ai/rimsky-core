# Reactive Loops + Lifecycle Handlers — Design

## Status

- Spec, 2026-05-05.
- Outcome of the 2026-05-05 brainstorm covering Rimsky's expressivity for "loop until there's nothing left to do" patterns and the supervisor refinements that make claim-Unavailable a clean noop transition rather than an error or a silent-retry footgun.
- Driving consumer: the Verantel docs-corpus pipeline sketch at `/Users/patrick/Documents/projects/research/verantel/.ok-planner/sketches/2026-05-05-docs-corpus-rimsky-pipeline-sketch.md`. The pipeline depends on this spec landing before its queue mode can ship.
- Foundational dependencies (read these before non-trivial implementation):
  - `docs/specs/2026-05-04-foundation-contract.md` — what foundation owns (cascade engine, claim/lock primitives, atomic acquisition tx, persistence, sweeps).
  - `docs/specs/2026-05-04-modeling-layer-contract.md` — what modeling owns (templates, instances, frames, scheduling, control-api).
  - `docs/concepts/invalidate.md`, `docs/concepts/cascade.md`, `docs/concepts/node-state.md` — current vocabulary and behavior.
  - `foundation/cascade/state.go` — the existing transition table.
  - `foundation/integration/runner_acquire.go`, `runner_terminal.go`, `cascade_invalidate.go` — current supervisor; this spec extends the acquisition tx and the terminal handler.
  - `foundation/persistence/postgres/migrations/001-initial.sql` — current schema (post-Phase-5 layer-crystallization: `rimsky_worker_request`, `rimsky_claim_handle`, `rimsky_nodes`, `rimsky_frames`).
- The starting sketch at `.ok-planner/sketches/2026-05-05-reactive-loops-and-lifecycle-handlers-sketch.md` carries the full pre-spec discussion; this spec subsumes and supersedes it.

## Context

Rimsky is a reactive node graph: nodes communicate via `invalidate` (the only graph-level message); recalculation is a scheduler action, not a peer message. The cascade engine, scheduler, and supervisor coordinate state transitions in response to invalidates and to commits. The model handles forward data flow (cascade-on-commit), backward repair (error-policy invalidate), and scheduled fan-out (cron-fired pure-cascade nodes) cleanly.

What it does **not** handle natively is "loop until there's nothing left to do." Today, expressing this pattern requires:

1. **Cron-as-heartbeat.** A scheduled `tick` node that fires every N seconds, with a downstream that processes one item per tick. Approximates "as soon as the previous frame finished" — badly. Idle gap per item; ticks bouncing off running frames; the cron keeps firing forever after the queue drains, even into a `failed` downstream.
2. **Retry-as-loop.** A node that retries on `claim_unavailable`-shaped error classes, abusing the retry mechanism for what should be a non-error termination signal. Conflates "the node erred" with "the node had nothing to do."
3. **Error-state-as-terminator.** A drained queue producing an `Open Unavailable` response that gets routed through error_types as `give_up` to put the node in `failed`. Operator-confusing: a successful drain looks like a failure on the dashboard.

All three are forward-workflow primitives wedged into a reactive system. They work, but they leak the wrong mental model.

This spec adds three small composable refinements that let the same pattern be expressed natively, **without changing today's cascade semantics.** Today's cascade-on-commit (one level deep, gated on `Changed: true`) and today's lazy-mark-on-commit-only behavior are preserved end-to-end. The new pieces:

- **Configurable lifecycle handlers** on each node: `on_acquire_unavailable`, `on_executor_complete`, `on_executor_blocked`, `on_executor_errored`. Each declares a `resolve:` (what state the node lands in) and an optional `invalidate:` (success-side message emit, symmetric to `error_types[X].policy.invalidate`). Defaults preserve today's hardcoded supervisor behavior; templates override per-handler.
- **`last_outcome` flavor** on the existing `fresh` state. State machine stays at four states; resolution flavor (`fresh_changed | fresh_unchanged | passed | pure_cascade | failed`) lives as a separate column. Dashboard renders the flavor distinctly without proliferating state-machine surface. Used by handlers and by the event/audit log; **not** used as an upstream-check gate (today's cascade already gates on `Changed:` in the right place).
- **Per-emit `frame: in | next`** on every invalidate emit declaration. Lets templates choose between in-frame loops (one frame per drain) and next-frame loops (one frame per iteration) explicitly, with sensible defaults that match today's implicit behavior (operator-originated invalidate goes through `frame.EnqueueOrCoalesce`; cascade `recalculate` post-commit is in-frame and not configurable because it's a scheduler action, not a message).

Plus one related refinement that landed in scope during the brainstorm:

- **`frame_timeout_ms` semantics** redefined from "frame age" to "no progress in window." Soft warning behavior preserved; the underlying metric becomes useful under in-frame loops.

What this spec does not cover (see §13):

- Predicate language for handler conditions ("if attribute X says Y, pass; else error"). Future work.
- Generalized frame-end predicate hooks. Future work.
- Hard frame timeouts (gaining teeth). Out of scope.
- Per-claim `on_unavailable` overrides (different handlers per claim within a node). Out of scope.
- Any change to today's cascade semantics. The cascade stays lazy + `Changed`-gated.
- Any pre-dispatch upstream-outcome check. The brainstorm's earlier sketches considered one; review of `runner_terminal.go` confirmed it would be redundant under today's cascade and was dropped.
- Workflow-control claim producer ("queue producer with no storage"). Discussed in the verantel sketch as future work.
- The Verantel docs-pipeline templates themselves. Downstream consumer.

---

## 1. Architecture overview

```
                          template (yaml)
                                │
                    parse + validate (modeling/template/)
                                │
                                ▼
                rimsky_nodes(state, last_outcome, frame_id)
                                │
                                ▼
        ┌───────────────────────┴───────────────────────┐
        │                                               │
        │   foundation/integration/runner_acquire.go    │
        │   (acquisition tx; existing flow extended for │
        │    handler-driven Unavailable resolution)     │
        │                                               │
        │   1. claim worker_request row (today)         │
        │   2. acquire claims (Open per claim) (today)  │
        │      ├─ all Acquired → running (today)        │
        │      └─ any Unavailable → fire                │
        │           on_acquire_unavailable handler ◄─── NEW
        │                                               │
        └───────────────────────┬───────────────────────┘
                                │
                                ▼
                    executor RPC (today's path)
                                │
                                ▼
        ┌───────────────────────┴───────────────────────┐
        │                                               │
        │   foundation/integration/runner_terminal.go   │
        │   (lifecycle-handler dispatch)                │
        │                                               │
        │   on_executor_complete | _blocked | _errored ◄─── NEW
        │      ├─ resolve: → state transition           │
        │      └─ invalidate: → enqueue per frame:      │
        │                                               │
        │   cascadeChildrenStaleInTx + fanoutRecalculate│
        │   (TODAY'S BEHAVIOR PRESERVED:                │
        │    one level; gated on Changed=true)          │
        │                                               │
        └───────────────────────────────────────────────┘
```

The cascade engine, claim primitives, and atomic acquisition tx (blessed invariant 10) are unchanged. The new logic lives at two boundaries: handler-driven Unavailable resolution inside the acquisition tx, and handler-driven terminal-event resolution inside the terminal-handler tx after the executor returns.

`last_outcome` is a new column on `rimsky_nodes`, written at every transition that lands a terminal-for-this-frame state. It's used by the dashboard, by the lifecycle handlers (specifically the `by_changed` mapping derives from `Changed:` and writes `fresh_changed`/`fresh_unchanged` into the column), and by the event log. It is **not** read by the supervisor as a dispatch gate.

---

## 2. Data model

### 2.1 State machine

Four states; `scheduled` does **not** become a new state value. The supervisor's acquisition flow already runs entirely inside one tx (today's behavior); the run-vs-pass decision happens inside that tx and commits to either `running` (Acquired path) or `fresh` (Unavailable path under `resolve: pass`). There is no persisted intermediate `scheduled` state.

```
fresh | stale | running | failed
```

`shared.NodeState` is unchanged — no new constant added. The transition table extends with new reasons (§2.3); blessed invariant 1 (state machine rejects illegal transitions) continues to hold.

### 2.2 `last_outcome` enum

New enum, persisted as a column on `rimsky_nodes`:

```
fresh_changed       — ran, executor reported Changed=true
fresh_unchanged     — ran, executor reported Changed=false
passed              — did not run; on_acquire_unavailable resolved pass
                       (or other handler resolved pass)
pure_cascade        — pure-cascade inline transition (no executor;
                       scheduler-driven inline stale → fresh)
failed              — error policy exhausted
```

Written at every transition that lands a terminal-for-this-frame state. NULL while the node is `stale` or `running`. Read by the dashboard, written by the supervisor's terminal handler and by the acquisition tx's pass branch.

### 2.3 New `TransitionReason` kinds

Indicative names; finalize during implementation. Added to `foundation/cascade/state.go`:

```
ReasonAcquirePass               — stale → fresh, last_outcome=passed.
                                  on_acquire_unavailable resolved pass.
ReasonHandlerComplete           — running → fresh.
                                  on_executor_complete handler resolved.
                                  Subsumes today's ReasonWorkCompleted; the
                                  old name kept as a deprecated alias for
                                  one cycle to ease the doc / annotation
                                  migration.
ReasonHandlerError              — running → stale or running → failed.
                                  on_executor_blocked / errored handler
                                  routing through error_types policy chain.
                                  Specific transition depends on policy
                                  resolution (retry → stale; give_up → failed;
                                  invalidate → stale).
ReasonHandlerPass               — running → fresh, last_outcome=passed.
                                  on_executor_blocked / errored handler
                                  resolved pass (template explicitly opts to
                                  ignore the terminal).
```

`NextState` extends:

```
case shared.NodeStateStale:
  reason ∈ {dispatch_claimed} → running
  reason ∈ {acquire_pass} → fresh
  reason ∈ {pure_cascade} → fresh
  reason ∈ {dispatch_impossible} → failed

case shared.NodeStateRunning:
  reason ∈ {handler_complete} → fresh           # subsumes work_completed
  reason ∈ {handler_pass} → fresh               # blocked/errored + resolve:pass
  reason ∈ {policy_retry, policy_invalidate, heartbeat_lost,
            infra_reenqueue, handler_error_retry} → stale
  reason ∈ {policy_give_up, handler_error_give_up} → failed
```

All transitions out of stale/running into fresh write `last_outcome` in the same query that writes the new state.

### 2.4 Schema changes

New migration adding to `rimsky_nodes`:

```sql
ALTER TABLE rimsky_nodes
  ADD COLUMN last_outcome TEXT;
```

No new index. Dashboard queries on `last_outcome` are not on the supervisor hot path; they're observability reads against rimsky_nodes already keyed by node_id or instance_id.

Frame-engine changes (§7) add a sibling column to `rimsky_frames`:

```sql
ALTER TABLE rimsky_frames
  ADD COLUMN last_progress_at TIMESTAMPTZ NOT NULL DEFAULT now();
```

Initialized on frame open; updated on every node state-transition write that carries the frame's id.

Pre-v1 migration policy per `.claude/rules/rules.md`: drop+recreate is the natural shape, but these additions are simple ALTERs that preserve existing rows. New columns get safe defaults. No backfill, no compat shim. Existing scenarios keep working under their default (no-handler) configurations.

---

## 3. Template surface — lifecycle handlers

### 3.1 Handler slots

Each node spec accepts up to four lifecycle-handler blocks. All are optional; absent handlers preserve today's hardcoded supervisor behavior.

```yaml
nodes:
  - type: <node-type>
    executor: <executor-name>
    stores: [...]
    locks:  [...]
    inherits: [...]
    attributes: {...}
    error_types: {...}        # unchanged from today

    # NEW — lifecycle handlers (all optional)
    on_acquire_unavailable:
      resolve: pass | retry | error
      error_class: <name>     # required when resolve: error
      invalidate:             # optional; orthogonal to resolve
        targets: [<node-type>...]
        frame: in | next      # optional; default: next

    on_executor_complete:
      resolve: by_changed | always_propagate | never_propagate
      invalidate:
        targets: [<node-type>...]
        frame: in | next

    on_executor_blocked:
      resolve: error | pass
      error_class: <name>     # required when resolve: error;
                              #   default class: executor_blocked
      invalidate:
        targets: [<node-type>...]
        frame: in | next

    on_executor_errored:
      resolve: error | pass
      error_class: <name>     # required when resolve: error;
                              #   default class: <executor-supplied class>
      invalidate:
        targets: [<node-type>...]
        frame: in | next
```

### 3.2 Per-handler `resolve:` vocabulary

Validated at template-deploy. Out-of-vocabulary combinations (e.g. `by_changed` on `on_acquire_unavailable`) are rejected with a clear error.

| Handler | Valid `resolve:` values |
|---|---|
| `on_acquire_unavailable` | `pass`, `retry`, `error` |
| `on_executor_complete` | `by_changed`, `always_propagate`, `never_propagate` |
| `on_executor_blocked` | `error`, `pass` |
| `on_executor_errored` | `error`, `pass` |

`error_class:` is required when `resolve: error`. Template validator rejects missing-class declarations.

### 3.3 Handler defaults (when not declared)

Preserve today's hardcoded behavior:

| Handler | Default behavior |
|---|---|
| `on_acquire_unavailable` | Silent retry on next scheduler tick. Equivalent to `resolve: retry`. Today's behavior. |
| `on_executor_complete` | `resolve: by_changed`, no invalidate. Equivalent to today's commit path. |
| `on_executor_blocked` | `resolve: error, error_class: executor_blocked`. Today's hardcoded routing. |
| `on_executor_errored` | `resolve: error, error_class: <executor-supplied class>`. Today's hardcoded routing. |

Existing scenarios, the smoke fixture, and any deployed templates work unchanged under these defaults. The handlers are an opt-in surface.

### 3.4 `resolve:` semantics

| Resolve value | Effect |
|---|---|
| `pass` (on_acquire_unavailable) | After today's acquisition tx rolls back: call `Abandon` on each claim that returned Available before the Unavailable one (producer-side cleanup; matches `handleOrphanedClaim` semantics). Then in a second tx: transition `stale → fresh`, `last_outcome = passed`, `reason = acquire_pass`. No executor invocation; `applyTerminalComplete` is never called, so cascade-on-commit (`cascadeChildrenStaleInTx` + `fanoutRecalculate`) is never invoked. |
| `pass` (on_executor_blocked, on_executor_errored) | Transition `running → fresh`, `last_outcome = passed`, `reason = handler_pass`. The Blocked / Errored terminal is treated as a noop; `applyTerminalComplete` is not called for these terminals (today's `applyTerminalAppError` path is what runs, and it routes to either `error` or `pass` per the handler). Cascade-on-commit is never invoked. |
| `retry` (on_acquire_unavailable only) | After today's acquisition tx rolls back: nothing further. Today's silent-retry behavior — scheduler re-attempts on next tick. Producer-side state for any Available-then-Unavailable partial acquisition is handled by producer TTL per today's spec §7.8 obligations (no rimsky-side Abandon — same as today). |
| `error` (on_acquire_unavailable, on_executor_blocked, on_executor_errored) | After acquisition tx rolls back (acquire_unavailable case) or after the executor terminal returns (blocked/errored cases): call `Abandon` on producer-side state for any opened claims (matches `handleOrphanedClaim`). Then route through `error_types[<class>].policy` per today's mechanism. The named class must exist in the template's `error_types`; validator rejects undeclared classes. State transitions follow the policy chain (retry → stale; invalidate → stale + emit; give_up → failed). |
| `by_changed` (on_executor_complete) | `last_outcome = fresh_changed` if `Complete.changed = true`; `fresh_unchanged` otherwise. Today's cascade-on-commit fires only when `last_outcome = fresh_changed` — preserving today's gate. |
| `always_propagate` (on_executor_complete) | Force `last_outcome = fresh_changed` regardless of `Complete.changed`. Cascade-on-commit fires. Useful for nodes that always want to wake dependents. |
| `never_propagate` (on_executor_complete) | Force `last_outcome = fresh_unchanged` regardless of `Complete.changed`. Cascade-on-commit does NOT fire. Useful for nodes that should never wake dependents directly. |

Note on cascade-firing rule: today's `runner_terminal.go::applyTerminalComplete` calls `cascadeChildrenStaleInTx` and `fanoutRecalculate` only when `t.Changed`. Under this spec, the gate becomes `last_outcome == fresh_changed` (which equals `t.Changed` under the default `by_changed` handler — preserved). Under `always_propagate`, the gate fires on every Complete; under `never_propagate`, the gate never fires. The `pass` and `error` resolutions never reach `applyTerminalComplete` at all (they take the on_executor_blocked / on_executor_errored / on_acquire_unavailable code paths, which are separate from the Complete-terminal path), so the cascade gate is never evaluated for those resolutions.

Note on producer-side cleanup: `pass` and `error` resolutions call `Abandon` on already-Open'd claims to match the `handleOrphanedClaim` (`runner_acquire.go:579-604`) semantic — supervisor knows it crashed/bailed mid-acquisition and owns the cleanup. `retry` does not call Abandon — same as today's silent-retry path; producer TTL handles it.

### 3.5 `invalidate:` semantics

The handler's `invalidate:` slot is **orthogonal** to `resolve:`. When the handler runs, `invalidate:` fires unconditionally if declared, regardless of which `resolve:` outcome was taken.

```yaml
on_executor_complete:
  resolve: by_changed
  invalidate: { targets: [self], frame: next }
```

Fires `invalidate` to `[self]` whether `Complete.changed` was `true` or `false`. The two propagations carry independent semantics:

- Cascade-on-commit (today's `cascadeChildrenStaleInTx` + `fanoutRecalculate`) is governed by the propagation rule on `last_outcome` (§3.4 table).
- `invalidate` to declared targets is governed by the handler's `invalidate:` declaration.

Templates that want conditional invalidation omit the `invalidate:` block (no message emit) or use `error_types[X].policy.invalidate` (which is conditional on the error class firing).

The reserved target `self` resolves to the declaring node's type at template-deploy. Other target names must reference declared node types in the same template.

### 3.6 Validation rules

Template validator enforces:

- Per-handler `resolve:` vocabulary (table in §3.2).
- `error_class:` required when `resolve: error`; the named class must exist in the same node's `error_types` (or be a built-in like `executor_blocked` for the on_executor_blocked default).
- `invalidate.targets[*]` must reference declared node types in the same template (or `self`).
- `invalidate.frame` ∈ `{in, next}`; defaults to `next` if absent.
- Declaring a handler with neither `resolve:` nor `invalidate:` is rejected (an empty handler has no effect; templates should omit it instead).

---

## 4. Supervisor behavior

### 4.1 Acquisition tx — Unavailable handler integration

`foundation/integration/runner_acquire.go` extends to route claim-Unavailable through the `on_acquire_unavailable` handler. Today's flow is preserved as the **default** path; the handler only changes behavior when the template declares non-default resolutions.

**Today's flow (preserved as `resolve: retry` default).** Per `runner_acquire.go::tryAcquireWithTx` and `tryAcquire`:

1. The acquisition tx opens.
2. Per-claim sequence: take advisory locks → claim `rimsky_worker_request` row → for each claim spec in sort order, run `acquireOneLock` → `acquireClaim`, which INSERTs a `rimsky_claim_handle` row and calls `Open` over the wire.
3. If `Open` returns `outcome.Available == false`: `acquireClaim` returns `(_, false, nil)`. The whole tx rolls back via the `errTryAcquireRollback` sentinel. Caller's outer loop continues to the next candidate; the worker_request row remains unclaimed; the next scheduler tick re-attempts.
4. If a claim earlier in the sort order returned `Available=true` and a later one returned `Unavailable`: rimsky-side `rimsky_claim_handle` rows are unwound via tx rollback. The producer-side state for the Available-claim's `Open` is handled by the producer's own TTL/sweep per spec §7.8 obligations (no rimsky-side Abandon in this path today).

This default path matches `resolve: retry` semantics under the new design — preserved bit-for-bit when the handler is absent.

**New paths under `resolve: pass`.** After step 3's rollback (today's mechanism), the supervisor takes the pass path:

1. **Capture Open results before rollback.** `acquireClaim` already constructs an `AcquiredLock` containing `Store`, `LockHolderID`, `ClaimResult` for each successful Open. The caller (`tryAcquire` / `tryAcquireWithTx`) needs to retain these for already-Available claims even when a later claim's Open returned Unavailable. This requires propagating the partial-acquired-list out of the rollback path, not just the bool. Mechanism: add a return value to `acquireClaim` / `acquireOneLock` that distinguishes "Unavailable" from "other-bail" so the caller can collect partial results. (Implementer's choice on signature shape; the existing `acquireOneLock` returns `(AcquiredLock, bool, error)` — extending to `(AcquiredLock, openResult, error)` where `openResult ∈ {acquired, unavailable, other_bail}` is one straightforward shape.)
2. **Call `Abandon` on already-Available claims.** Iterate the partial-acquired list and call `lk.Store.Abandon(ctx, claimID, scope, address)` for each — matching `handleOrphanedClaim` (`runner_acquire.go:579-604`) semantics. Producer-side state cleaned up.
3. **Apply the resolution in a second tx.** Open a new tx; UPDATE `rimsky_nodes` SET `state = 'fresh'`, `last_outcome = 'passed'`; emit `state_transition` event with `reason = acquire_pass`; emit any handler-declared `invalidate:` per the `frame:` field via `frame.EnqueueOrCoalesce`.
4. **Cascade-on-commit does NOT fire.** `applyTerminalComplete` is not called (no Complete terminal occurred); `cascadeChildrenStaleInTx` and `fanoutRecalculate` are never invoked.

**New paths under `resolve: error`.** Same Abandon cleanup as `pass` (step 2 above), then in a second tx: route through `error_types[handler.error_class].policy`. The policy chain's outcome (retry / invalidate / give_up) drives the state transition per today's mechanism. Optional `invalidate:` emit still fires.

**Per-claim Abandon semantics.** `Abandon` matches today's `handleOrphanedClaim` invocation: `lk.Store.Abandon(ctx, claimID, scope, address)`. The Unavailable-claim itself is NOT Abandoned (the producer signaled Unavailable, meaning it has no state to abandon — that's the contract). Only Available claims that got rolled back due to a later Unavailable get Abandoned.

The acquisition tx itself remains atomic per blessed invariant 10. The new pass/error paths run in a SECOND tx after the acquisition tx rolls back; the two-tx structure mirrors today's `handleOrphanedClaim` + `tryAcquireWithTx` separation. The Abandon calls happen between the two txs (over the wire to the producer; not inside any rimsky tx).

### 4.2 Terminal handler — lifecycle-handler integration

`runner_terminal.go::applyTerminalComplete` extends to apply `on_executor_complete` (with default = `by_changed`) and to fire any declared `invalidate:`. The cascade-on-commit gate (`if t.Changed { cascade + fanout }`) is preserved but driven by the resolved `last_outcome` instead of `t.Changed` directly.

```
BEGIN TRANSACTION
  release locks (today)
  upsert attributes (today)
  resolve handler outcome:
    handler := node.on_executor_complete                      # or default: by_changed
    SWITCH handler.resolve:
      CASE by_changed:
        last_outcome := if t.Changed then fresh_changed else fresh_unchanged
      CASE always_propagate:
        last_outcome := fresh_changed
      CASE never_propagate:
        last_outcome := fresh_unchanged
  UPDATE rimsky_nodes SET state = 'fresh', last_outcome = ?, reason = handler_complete
  IF last_outcome == fresh_changed:
    cascadeChildrenStaleInTx (today's mechanism)              # mark direct dependents stale
COMMIT

# Outside tx (today's pattern):
IF last_outcome == fresh_changed:
  fanoutRecalculate (today's mechanism)                       # send recalculate events for dependents
IF handler.invalidate declared:
  enqueue invalidate per handler.invalidate.frame             # via frame.EnqueueOrCoalesce
```

Symmetric extensions for `on_executor_blocked` and `on_executor_errored`. Note: today's Blocked / Errored terminals route through `runner_terminal.go::applyTerminalAppError`, not `applyTerminalComplete` — they're separate terminal paths. The handler integration replaces today's hardcoded routing with declarative resolution:

```
SWITCH handler.resolve:
  CASE error:
    BEFORE policy routing: call Abandon on each acquired claim
       # matches handleOrphanedClaim semantics; producer-side cleanup
       # before the policy chain decides retry vs invalidate vs give_up.
       # (For executor-Blocked / Errored, claims are already-Open per a
       # successful acquisition; cleanup is symmetric to the on_acquire
       # error path.)
    route through error_types[handler.error_class].policy
    state transition per policy outcome (today's applyTerminalAppError)
  CASE pass:
    call Abandon on each acquired claim                       # producer-side cleanup
    UPDATE rimsky_nodes SET state = 'fresh',
                              last_outcome = 'passed',
                              reason = handler_pass           # in a fresh tx
    # applyTerminalComplete is NOT called; cascadeChildrenStaleInTx and
    # fanoutRecalculate are never invoked. The cascade gate (
    # if last_outcome == fresh_changed) lives inside applyTerminalComplete
    # and is unreachable from the pass path.
IF handler.invalidate declared:
  enqueue invalidate per handler.invalidate.frame             # via frame.EnqueueOrCoalesce
```

Per blessed invariant 10, today's terminal-handler txs run atomically; the new code preserves this — `pass` opens a new tx for the state transition (after Abandon calls over the wire), `error` follows today's existing tx structure inside `applyTerminalAppError`. The fanout/invalidate emit happens after commit per today's pattern (see `runner_terminal.go:144-146` and `:191-215`).

### 4.3 Held-claim cascade (no special-case logic)

No special handling for held claims. Today's lazy cascade naturally handles the holding subgraph:

- Acquirer A is by definition a direct upstream of every inheritor (the `inherits:` declaration creates the dep edge per `docs/concepts/inheritance.md`).
- If A's `on_acquire_unavailable: { resolve: pass }` fires, A transitions `stale → fresh` with `last_outcome=passed`. **No cascade fires**: the `pass` path never reaches `applyTerminalComplete`, so `cascadeChildrenStaleInTx` and `fanoutRecalculate` are never called.
- Inheritors stay fresh from their previous frame (or whatever their state was). They are not woken by A's pass.
- The held-subgraph auto-terminal mechanism (`foundation/integration/auto_terminal.go`) doesn't fire because the acquisition tx rolled back any claim handles that were inserted (today's rollback mechanism), and the pass path's Abandon calls (§4.1) clean up producer-side state for any already-Available claims. Nothing to clean up post-pass.

For mixed-graph topologies where an inheritor B has another upstream C that DID propagate (Changed=true): C's commit cascades to B (today's mechanism); B is marked stale; B's dispatch attempts substitution into `{{claim.<alias>.address}}`; substitution fails because no handle was ever created; `template_resolution_failed` routes through B's `error_types`. Templates that want graceful queue-drain behavior in this unusual shape should declare `error_types[template_resolution_failed].policy: [{action: give_up}]` or similar.

This case is rare. The vast majority of held-claim templates have inheritors that depend ONLY on the acquirer (or a chain rooted at the acquirer). For those, A's pass simply means "no cascade; inheritors stay fresh." Clean.

### 4.4 `run_attempt` semantics

`run_attempt` only advances when the executor was actually invoked (the `running` state was reached). Specifically:

- `pass` resolutions on `on_acquire_unavailable`: no advance. Executor never ran.
- `retry` resolutions (silent retry on next scheduler tick): no advance. Executor never ran.
- `pass` resolutions on `on_executor_blocked` / `on_executor_errored`: advance per today's behavior — the executor DID run; the handler is just choosing to ignore the terminal.
- Executor invocation that returns Complete / Blocked / Errored: advance per today's behavior.
- Heartbeat-loss reenqueue (`infra_reenqueue`): advance per today's behavior.

### 4.5 Operator-originated invalidate behavior

Unchanged. `POST /nodes/{id}/invalidate` continues to mark only the target node stale (per today's `cascade_invalidate.go::InvalidateNode`). Cascade happens lazily as the target's commit propagates per the existing `cascadeChildrenStaleInTx` mechanism.

The only operator-side addition is the optional `frame: in | next` field on the invalidate request body (§5.4); default `next` matches today's behavior of going through `frame.EnqueueOrCoalesce`.

### 4.6 `failed` upstream behavior

Unchanged. A node with a `failed` upstream stays in whatever state it's in (typically `stale` if it was waiting for the upstream). Operator must `POST /nodes/{id}/reset` or `POST /nodes/{id}/invalidate` to unstick. Today's "failures freeze downstream" semantic preserved.

---

## 5. Template surface — per-emit `frame:`

### 5.1 Scope

`frame: in | next` is configurable on every invalidate emit declaration:

| Emit site | `frame:` configurable | Default |
|---|---|---|
| Operator API (`POST /nodes/{id}/invalidate`) | Yes (request body field) | `next` |
| `error_types[X].policy.invalidate` (existing) | Yes (new field on the policy entry) | `next` |
| Lifecycle-handler `invalidate:` (new) | Yes (field on the invalidate block) | `next` |
| Cascade `recalculate` post-commit (scheduler action) | **No** — hardcoded in-frame | (n/a) |

The cascade recalculate is supervisor-internal — a scheduler action, not a peer message (per `docs/concepts/invalidate.md`'s "Common mistakes" entry — recalculation is what the scheduler does to a stale node, not a message that travels). Configurability is on emitted invalidate messages only.

### 5.2 `frame: in` semantics

The invalidate joins the current cascade. The frame stays open until the cascade quiesces (no nodes in `stale` or `running`). Used for tight loops where each iteration would otherwise create a frame's worth of bookkeeping noise.

Implementation: the emit path bypasses `frame.EnqueueOrCoalesce` and directly transitions the target node `fresh → stale` within the current frame's `frame_id`. The supervisor's normal stale-node sweep picks it up on the next tick (still inside the same frame).

In-frame self-invalidate is the pattern that motivated the `frame_timeout_ms` refinement (§7). Without that refinement, in-frame loops would trigger spurious frame-age warnings.

### 5.3 `frame: next` semantics

The invalidate buffers through `frame.EnqueueOrCoalesce` per today's mechanism. Current frame closes at quiescence; next frame opens with the target stale. Each iteration is a discrete frame on the dashboard. Default for all invalidate emits.

Under `frame_resolution: coalesce`, multiple pending self-invalidates collapse to one pending frame (correct — no double-execute; a frame-coalesce + self-invalidate scenario test verifies this).

Under `frame_resolution: serial_queue`, each self-invalidate enqueues its own frame; frames execute in order.

### 5.4 API extension for operator-originated invalidate

The control-API endpoint accepts an optional `frame` field:

```
POST /nodes/{id}/invalidate
{
  "reason": "operator-triggered",
  "frame": "next"   // optional; default "next"
}
```

Existing callers without the field get today's behavior. CLI `rimsky-cli admin invalidate` exposes `--frame in|next` (default `next`).

---

## 6. Cascade behavior

### 6.1 Today's cascade preserved

`invalidate(A)` continues to mark only A stale (today's `cascade_invalidate.go::InvalidateNode` only enqueues a frame for the target node; no transitive walk). Cascade happens lazily as commits propagate: `applyTerminalComplete` calls `cascadeChildrenStaleInTx` to mark direct dependents stale and `fanoutRecalculate` to send recalculate events, both gated on `t.Changed`.

This spec does not change any of this. The pre-dispatch upstream check considered in early sketches was confirmed redundant under today's lazy cascade — every stale dependent already either was directly invalidated (operator/error-policy/handler invalidate) or is downstream of a propagating upstream (the `Changed=true` gate ensured this). There's no scenario today where a dependent is stale without a propagating upstream cause.

### 6.2 What `last_outcome` adds

`last_outcome` is **not** read as a dispatch gate. It's a dashboard-visible flavor and a substrate for handler resolutions:

- `on_executor_complete` writes `last_outcome` per its `resolve:` declaration. The cascade-on-commit gate (today's `if t.Changed`) becomes `if last_outcome == fresh_changed` — preserved under the default `by_changed` handler; overridable via `always_propagate` / `never_propagate`.
- `on_acquire_unavailable: { resolve: pass }` writes `last_outcome = passed`.
- `on_executor_blocked` / `on_executor_errored` with `resolve: pass` write `last_outcome = passed`.
- Pure-cascade inline transitions write `last_outcome = pure_cascade`.
- Failures write `last_outcome = failed`.

The dashboard renders the flavor distinctly so operators can see "this node passed because the queue drained" vs. "this node ran and committed" vs. "this node ran and decided not to propagate." All currently look identical (`fresh`) on today's dashboard.

### 6.3 What changes for operator-visible behavior

Templates without lifecycle handlers see no observable behavior change. Today's defaults preserved:

- `on_acquire_unavailable` absent → silent retry (today).
- `on_executor_complete` absent → `by_changed` (today).
- `on_executor_blocked` absent → `error_types[executor_blocked].policy` (today).
- `on_executor_errored` absent → `error_types[<executor-supplied class>].policy` (today).

Templates that DO declare handlers see new behavior per the §3.4 table. The `last_outcome` column gets populated for all nodes (including handler-absent ones, since the default `by_changed` resolution writes it); this is observable on the dashboard but doesn't change runtime behavior.

---

## 7. Frame timeout refinement

### 7.1 Motivation

Today's `frame_timeout_ms` ("this frame has been open longer than X") becomes uninformative under in-frame self-invalidate loops — the frame stays open for the entire drain, which could be hours for a long queue. The metric's intent is "the frame seems stuck," but frame age alone can't distinguish "stuck" from "doing useful work for a long time."

### 7.2 New metric: last-progress-at

Replace "frame age" with "no progress in window."

**Progress = any state transition on any node in the frame.** Specifically, every write to `rimsky_nodes.state` updates the frame's `last_progress_at` to `now()`. The `frame_timeout_ms` check becomes:

```sql
SELECT id FROM rimsky_frames
 WHERE state = 'running'
   AND now() - last_progress_at > make_interval(milliseconds := frame_timeout_ms)
```

A long-running but progressing frame doesn't trigger; an actually-stuck frame (no node has transitioned in `frame_timeout_ms`) does.

### 7.3 What does NOT count as progress

Deliberately excluded to keep the metric narrow:

- **Executor heartbeats.** Heartbeats are per-run liveness signals at a different layer; the executor protocol already handles heartbeat-loss via `infra_reenqueue` (which IS a state transition and DOES count as progress).
- **Incremental attribute writebacks.** The `POST {callback_url}/v1/attributes/{node_id}` path writes to `rimsky_node_attributes` without a state transition. Doesn't count.
- **Quality-rule evaluations.** Internal supervisor work; not visible at the frame level.

The principle: progress is "the cascade graph moved." Liveness signals at other layers have their own metrics.

### 7.4 Per-frame, not per-node

`last_progress_at` lives on `rimsky_frames`, not per-node. A frame with one node making progress and ten others idle is "making progress." This matches the metric's semantic ("the frame is doing something").

### 7.5 Soft warning only

No teeth. Today's behavior preserved: the metric triggers a structured log warning when the threshold is exceeded. No automatic frame-fail, no automatic reaping, no automatic node invalidation.

Hard frame timeouts ("kill this frame after N minutes of no progress") would be a separate change with consumer use cases to think through. Out of scope.

### 7.6 Interaction with executor silence-timeout

None. The executor silence-timeout (`RIMSKY_EXECUTOR_SILENCE_MS` on the claude-agent peer) measures per-run liveness — "the running executor hasn't emitted output in N seconds." That's a per-run health signal at the protocol layer.

The frame `last_progress_at` measures cascade progress — "no node in this frame has transitioned in N seconds." Frame-level health signal.

The two can fire independently and mean different things. Documentation in `docs/concepts/frame.md` and the operator-guide makes the distinction explicit.

### 7.7 Implementation surface

- New column `last_progress_at TIMESTAMPTZ NOT NULL DEFAULT now()` on `rimsky_frames` (§2.4).
- `modeling/frame/engine.go` updates `last_progress_at = now()` in the same tx as every state-transition write that carries the frame's id. The hook is the existing state-transition write path; one additional UPDATE per transition.
- The frame-engine's timeout-evaluation tick consults `last_progress_at` instead of `opened_at`. One predicate change.
- One scenario test: an in-frame self-invalidate loop that's making progress doesn't trigger the warning over a window that exceeds `frame_timeout_ms`; an actually-idle running frame does trigger.

---

## 8. Migration

Pre-v1 per `.claude/rules/rules.md`. The schema additions (`rimsky_nodes.last_outcome`, `rimsky_frames.last_progress_at`) are simple ALTERs that preserve existing rows.

**`rimsky_nodes.last_outcome`:** initialized NULL on existing rows. Dashboard displays NULL as "no outcome recorded yet" (falls back to the bare `state` value). Handler resolutions populate the column on the next transition. No backfill needed.

**`rimsky_frames.last_progress_at`:** initialized to `now()` on existing rows (DEFAULT). The frame-timeout check treats this as "the frame just made progress," which is the safest default (no spurious warnings on legacy frames).

No backfill, no compat shim. Existing scenarios keep working under their default (no-handler) configurations.

CHANGELOG entry documents:

- The new `last_outcome` column and its enum values.
- The new `last_progress_at` column and the `frame_timeout_ms` semantic shift.
- The new template surface (lifecycle handlers, `frame: in | next`).
- That today's runtime behavior is unchanged for handler-absent templates.

---

## 9. Implementation surface

File-by-file (indicative; finalize during plan-writing). Schema files reference the post-Phase-5 layer-crystallization layout (`foundation/persistence/postgres/`, `foundation/persistence/sqlite/`).

### `foundation/cascade/state.go`

- Add `LastOutcome` enum with values `fresh_changed | fresh_unchanged | passed | pure_cascade | failed`.
- Add new `TransitionReason` kinds: `ReasonAcquirePass`, `ReasonHandlerComplete` (subsuming `ReasonWorkCompleted` for the new path; old name kept as deprecated alias for one cycle), `ReasonHandlerError`, `ReasonHandlerPass`.
- Extend `NextState` transition table (§2.3). Reject illegal combinations per blessed invariant 1.
- Update tests in `state_test.go` to cover the new transitions.

### `foundation/persistence/{postgres,sqlite}/migrations/`

- New migration adding `last_outcome TEXT` column to `rimsky_nodes`.
- New migration (or same migration) adding `last_progress_at TIMESTAMPTZ NOT NULL DEFAULT now()` column to `rimsky_frames`.
- Both drivers (Postgres + SQLite per the persistence-pluggable spec).

### `foundation/persistence/{postgres,sqlite}/nodes.go`

- Persist `last_outcome` on every state-transition write that lands `fresh` or `failed`. The existing `UpdateState(ctx, id, state, reason, tx)` signature gains a `lastOutcome` parameter (or a sibling `UpdateStateWithOutcome` helper if signature change is too invasive).

### `foundation/persistence/{postgres,sqlite}/frames.go`

- Update `last_progress_at` on every node state-transition write that carries the frame's id (one additional `UPDATE rimsky_frames SET last_progress_at = now() WHERE id = ?` per transition; runs in the same tx).
- Update the frame-timeout-evaluation query to consult `last_progress_at`.

### `modeling/template/`

- Extend node-spec parsing for the four lifecycle-handler blocks (`on_acquire_unavailable`, `on_executor_complete`, `on_executor_blocked`, `on_executor_errored`).
- Per-handler `resolve:` vocabulary validation (§3.2).
- `error_class:` required when `resolve: error`; class must exist in `error_types` (or be a built-in default).
- `invalidate.targets[*]` validation against declared node types or `self`.
- `invalidate.frame` ∈ `{in, next}`.

### `foundation/integration/runner_acquire.go`

- Implement Unavailable-handler integration (§4.1):
  - Add resolve-switch after Open returns Unavailable.
  - `pass` branch: cleanup claim handles, transition to fresh+passed, optional invalidate emit, COMMIT.
  - `retry` branch: ROLLBACK (today's behavior).
  - `error` branch: route through error_types per the named class.

### `foundation/integration/runner_terminal.go`

- Implement Complete handler integration (§4.2):
  - Read `on_executor_complete` from template; default `by_changed`.
  - Map `resolve:` to `last_outcome`.
  - Cascade gate: `if last_outcome == fresh_changed { cascadeChildrenStaleInTx; fanoutRecalculate }`.
  - Optional invalidate emit per `handler.invalidate.frame`.
- Symmetric Blocked / Errored handler integration:
  - Read handler from template; default `error` with the existing class.
  - `error` branch: route through error_types per today (potentially with template-specified `error_class:` override).
  - `pass` branch: transition to fresh+passed, no cascade.
  - Optional invalidate emit.

### `foundation/integration/cascade_recalculate.go`

- No changes. Today's `RecalculateNode` and `fanoutRecalculate` continue to work. The cascade-firing gate moves from `if t.Changed` to `if last_outcome == fresh_changed` (still inside `applyTerminalComplete`'s tx; just expressed via the new field).

### `foundation/integration/cascade_invalidate.go`

- Optional: extend `InvalidateArgs` to accept a `frame: in | next` value; default `next`. The `frame: in` path bypasses `frame.EnqueueOrCoalesce` and directly transitions the target stale within the current frame.

### `modeling/frame/engine.go`

- Add `last_progress_at` update to the state-transition write path (one additional UPDATE per transition).
- Update timeout-evaluation predicate.

### `modeling/controlapi/`

- Extend `POST /nodes/{id}/invalidate` to accept optional `frame: in | next` field; default `next`. Validation on the field.

### `cmd/rimsky-cli/`

- Extend `rimsky-cli admin invalidate` with `--frame in|next` flag; default `next`.

### `docs/concepts/`

- Extend `node-state.md` with the `last_outcome` enum.
- Extend `node.md` with the lifecycle-handler block.
- Extend `cascade.md` to clarify that `last_outcome` is observability metadata, not a dispatch gate (today's lazy + Changed-gated cascade is preserved).
- Extend `frame.md` with the `frame_timeout_ms` semantic shift.
- Update `invalidate.md` with the per-emit `frame: in | next` field.

### `docs/specs/2026-05-04-foundation-contract.md`

- Note the lifecycle-handler integration as foundation-owned (matches the contract — supervisor terminal handler).

### `docs/specs/2026-05-04-modeling-layer-contract.md`

- Note the lifecycle-handler block as a modeling-layer template-spec extension.

### `CHANGELOG.md`

- Entry under Unreleased capturing all of the above.

### `CLAUDE.md`

- Add `last_outcome` to the vocabulary line if appropriate.
- No new blessed invariants; existing inv 1 covers the new transitions. The cascade behavior is unchanged.

---

## 10. Testing strategy

Scenario tests under `test/scenarios/`:

1. **`reactive_loop_self_invalidate_next_frame_test.go`** — Single-node template with `on_executor_complete: { invalidate: { targets: [self], frame: next } }`. Queue producer (claim-shape) yields N items; each Acquired commit fires self-invalidate; queue eventually drains; on_acquire_unavailable: pass fires; node lands in `fresh+passed`; instance reaches terminal.

2. **`reactive_loop_self_invalidate_in_frame_test.go`** — Same template with `frame: in`. Single long-running frame for the entire drain; `last_progress_at` updates per iteration; `frame_timeout_ms` warning doesn't fire.

3. **`acquire_unavailable_pass_test.go`** — Template with `on_acquire_unavailable: { resolve: pass }`. Producer returns Unavailable on first dispatch; node transitions to `fresh+passed` without invoking executor. No cascade-on-commit fires (no Changed-true outcome).

4. **`acquire_unavailable_retry_default_test.go`** — Template without `on_acquire_unavailable` declared. Producer returns Unavailable; supervisor performs silent retry on next scheduler tick (today's default behavior preserved).

5. **`acquire_unavailable_error_routing_test.go`** — Template with `on_acquire_unavailable: { resolve: error, error_class: my_drained }`. Producer returns Unavailable; `error_types[my_drained]` policy chain fires; node lands per policy.

6. **`held_claim_acquirer_passes_test.go`** — Held-subgraph: A acquires claim @q; B inherits @q. A passes (Unavailable + pass handler). No claim handles inserted (cleanup verified). B is not woken (no cascade fires from a pass). B stays fresh from previous state.

7. **`held_claim_mixed_upstream_test.go`** — A acquires @q (passes); C is an independent upstream of B that commits Changed=true (cascade fires to B). B is marked stale via C's cascade. B's dispatch attempts substitution into `{{claim.@q.address}}`; substitution fails. `template_resolution_failed` routes through B's error_types.

8. **`always_propagate_resolution_test.go`** — Node with `on_executor_complete: { resolve: always_propagate }`. Commits with `changed: false`; resolution forces `last_outcome=fresh_changed`; cascade fires; downstream is marked stale.

9. **`never_propagate_resolution_test.go`** — Node with `on_executor_complete: { resolve: never_propagate }`. Commits with `changed: true`; resolution forces `last_outcome=fresh_unchanged`; cascade does NOT fire; downstream stays fresh.

10. **`pure_cascade_outcome_test.go`** — Pure-cascade scheduled root with N executor-backed dependents. Cron fires; root transitions `stale → fresh, last_outcome=pure_cascade`. Today's cascade fan-out fires (pure_cascade is propagating-equivalent for the cascade firing rule). Dashboard reads `last_outcome=pure_cascade` for the root.

11. **`fresh_unchanged_does_not_cascade_test.go`** — Two-node graph A → B. A commits with `changed: false` → `last_outcome=fresh_unchanged`. Cascade gate (`if last_outcome == fresh_changed`) does NOT fire. B stays fresh from previous state. (This is today's behavior under by_changed default; explicit test that it's preserved.)

12. **`operator_invalidate_target_only_test.go`** — Operator-originated invalidate marks only the target stale (today's behavior preserved). Cascade happens lazily as the target's commit propagates. No transitive walk at invalidate time.

13. **`failed_upstream_freezes_downstream_test.go`** — Upstream A → B. A fails (policy give_up). B stays in its previous state. Today's behavior preserved.

14. **`executor_blocked_pass_resolution_test.go`** — Node with `on_executor_blocked: { resolve: pass }`. Executor returns Blocked; node lands in `fresh+passed` instead of routing through error_types.

15. **`executor_errored_pass_resolution_test.go`** — Node with `on_executor_errored: { resolve: pass }`. Executor returns Errored; node lands in `fresh+passed` instead of routing through error_types.

16. **`frame_coalesce_self_invalidate_test.go`** — Node with `on_executor_complete: { invalidate: { targets: [self], frame: next } }` under `frame_resolution: coalesce`. Rapid sequence of commits; coalesce path collapses pending self-invalidates to one pending frame; no double-execute.

17. **`frame_timeout_progressing_loop_test.go`** — In-frame self-invalidate loop running for longer than `frame_timeout_ms`. `last_progress_at` updates per iteration; timeout warning doesn't fire.

18. **`frame_timeout_stuck_frame_test.go`** — Running frame with no node transition for `frame_timeout_ms`; warning fires.

19. **`handler_invalidate_orthogonal_to_changed_test.go`** — `on_executor_complete: { resolve: by_changed, invalidate: { targets: [monitor], frame: next } }`. Commit with `changed: false` → `last_outcome=fresh_unchanged` → cascade does NOT fire to dependents → BUT invalidate fires to `[monitor]` regardless.

20. **`acquire_pass_invalidate_emit_test.go`** — `on_acquire_unavailable: { resolve: pass, invalidate: { targets: [monitor], frame: next } }`. Producer returns Unavailable; node passes; invalidate fires to `[monitor]` even though no executor ran.

Test infrastructure: scenario harness already supports declarative templates and stub executors/producers. Each scenario boots its own Postgres testcontainer.

Race tests (-race -count=3) for paths in `runner_acquire.go` and `runner_terminal.go` per `.claude/rules/rules.md`:

- Frame-coalesce + self-invalidate (#16 above) gets `-race -count=5` to flake-hunt the coalesce-vs-emit race.

---

## 11. Documentation updates

Per `.claude/rules/rules.md` "After Code Changes":

- **`docs/concepts/`** — extensions to `node-state.md`, `node.md`, `cascade.md`, `frame.md`, `invalidate.md` per §9.
- **`docs/specs/2026-05-04-foundation-contract.md`** — note lifecycle-handler integration (foundation-owned).
- **`docs/specs/2026-05-04-modeling-layer-contract.md`** — note lifecycle-handler block as modeling-layer template extension.
- **`CHANGELOG.md`** — comprehensive entry per §8.
- **`CLAUDE.md`** — vocabulary line update if needed.
- **Cold-read annotations** updated where modified (`@blessed-invariant 1`, `@source` references on the cascade/recalculate paths).

---

## 12. Related cleanups

### 12.1 `CLAUDE.md` vocabulary cleanup (LANDED)

`CLAUDE.md` previously claimed two message types (`invalidate`, `recalculate`) and pointed at the pre-layer-crystallization design docs. Already corrected in the same session that produced this spec; CHANGELOG entry recorded.

### 12.2 Stale `docs/internal/` references in `CLAUDE.md` (NOT LANDED)

`CLAUDE.md` lines 176-181 still reference `docs/internal/node-graph-design.md`, `docs/internal/architecture.md`, etc. — paths that don't exist after the layer-crystallization restructure (the files were archived to `.ok-planner/archive/internal/`). Separate cleanup; out of scope for this spec but flagged.

### 12.3 Predicate language for handler conditions

A future extension would let handlers conditionally resolve based on attributes or other predicates ("if attribute X says Y, pass; else error"). Out of scope for this spec; the resolve-kind enum is finite and validated. When a use case emerges, design the predicate language separately.

### 12.4 Hard frame timeouts

`frame_timeout_ms` gaining teeth (automatic frame-fail, automatic node invalidation) is a separate design. Operator use cases drive that decision. Out of scope.

### 12.5 Workflow-control claim producer

A claim-producer kind with no storage semantics (purely a workflow-control payload source). Discussed in the verantel docs-pipeline sketch. Cleaner architectural factoring for future bounded-work patterns; out of scope here.

---

## 13. Open questions (deferred to implementation)

Listed for the implementer's reference.

- **Final names for new `TransitionReason` kinds.** Indicative names in §2.3; finalize in `state.go`.
- **`ReasonWorkCompleted` deprecation handling.** Subsumed by `ReasonHandlerComplete`. Either rename in place (cleaner) or keep as alias for one cycle (safer for any docs referencing the old name). Implementer decides.
- **`UpdateState` signature change vs. helper method.** Adding `lastOutcome` to the existing `UpdateState` signature touches every caller. A sibling `UpdateStateWithOutcome` keeps the old signature for callers that don't need to set the field. Implementer decides based on diff size.
- **Handler `error_class:` validation interaction with `error_types`.** When a handler declares `resolve: error, error_class: foo`, validator checks `error_types[foo]` exists. What if `error_types[foo]` references the handler in its policy chain (`policy: [{action: invalidate, targets: [self]}]`)? Cycle is fine semantically (it's a message-graph cycle, allowed) but worth a scenario test.

---

## 14. Risks and unknowns

- **`last_outcome` write site coverage.** Every code path that writes `state = 'fresh'` or `state = 'failed'` must also write `last_outcome` (or accept that `last_outcome` stays NULL, which is fine for the dashboard but means the `if last_outcome == fresh_changed` cascade gate behaves like `false` for that node — which would be incorrect for today's `Changed=true` paths). The implementer must enumerate the writes during implementation: every call site of `UpdateState(_, _, NodeStateFresh, _, _)` and `UpdateState(_, _, NodeStateFailed, _, _)`. Plan a comprehensive grep + audit during implementation.
- **Cascade gate change from `t.Changed` to `last_outcome == fresh_changed`.** Today's `if t.Changed` reads from the executor's terminal event directly; the new `if last_outcome == fresh_changed` reads from a column written by the same code path. Functionally equivalent under the default `by_changed` handler. The risk is in the order of operations: `last_outcome` must be persisted in the same tx as the cascade decision, BEFORE `cascadeChildrenStaleInTx` runs. The implementation must thread `last_outcome` into the tx scope at the right point.
- **Test surface size.** 20 scenario tests is a lot but each is small and targets a specific invariant. Test infrastructure already supports the pattern. Worth running `-count=3 -race` on the new tests during landing to flake-hunt.
- **Frame coalesce + self-invalidate race.** Tight self-invalidate cycles under coalesce mode are a new pattern. Scenario test #16 + race-mode runs target this; race conditions in `frame.EnqueueOrCoalesce` would surface there.
- **Migration hazard: `last_outcome=NULL` on legacy fresh nodes.** Pre-migration `fresh` nodes have NULL `last_outcome`. Dashboard handles NULL gracefully (renders as "no flavor recorded"). The cascade gate would treat NULL as `false` (non-propagating) — but legacy fresh nodes are NOT in any active frame's cascade decision, so this is moot. Document as expected behavior; flag in CHANGELOG.
- **Backwards compat for in-flight test fixtures.** Existing scenario tests rely on default (no-handler) template shapes. They keep working by §3.3's preservation of today's defaults. Worth running the full scenario suite during implementation to verify nothing trips.

---

## 15. What this is not

- **Not a generalized frame-end predicate hooks design.** Template-level frame-end predicate evaluation, predicate language, new scheduling phase. Right shape for a future class of patterns; this spec scopes to specific resolve-kind and invalidate primitives.
- **Not a replacement for cron-driven nodes.** Scheduled nodes still exist; "run every Tuesday at 3am" is still right for them. This spec adds the "loop until done" primitive next to cron, not replacing it.
- **Not a redesign of the error model.** `error_types` and the policy chain are unchanged. Lifecycle handlers route into error_types via `resolve: error`, not around it.
- **Not a backwards-compat-breaking change at the template level for templates without lifecycle-handler blocks.** Templates without handlers preserve today's behavior under §3.3 defaults. Only templates that declare handlers see new behavior.
- **Not a generalized state-machine extension.** No new state values; `last_outcome` is a sibling field, not a state. The proliferation stops there.
- **Not a cascade-semantics change.** Today's lazy + Changed-gated cascade is preserved end-to-end. The pre-dispatch upstream check considered in early sketches was confirmed redundant under today's cascade and dropped.
- **Not a BFS-frontier scheduler rewrite.** Same reason.
- **Not an eager-invalidate change.** Today's `cascade_invalidate.go::InvalidateNode` only marks the target stale; cascade happens lazily on commits. Preserved.
- **Not hard frame timeouts.** `frame_timeout_ms` stays soft-warning-only. Hard timeouts are a separate design.
- **Not a workflow-control claim producer design.** That's a sibling architectural conversation; this spec stays focused on Rimsky-side primitives.
