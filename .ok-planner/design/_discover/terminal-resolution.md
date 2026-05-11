---
topic: terminal-resolution
kind: discipline
---

# End-to-end terminal-event flow: executor wire → lifecycle handler → error policy → claim-handle resolution

## Description

Every executor terminal event (`Complete`, `Blocked`, `Errored`, `AsyncAccepted` → callback, `ParkRequested`, or an infra-class failure like `stream_closed_without_terminal`) converges on a single spine of code that decides:

1. What `last_outcome` to stamp on the node (and therefore whether cascade fires).
2. What to do with the dispatch row (delete vs retry-enqueue vs invalidate-targets).
3. What producer verb (`Commit` / `Abandon` / nothing) to fire on every acquired claim.
4. When to delete the `rimsky_claim_handle` rows claimant-guarded.

Four `_discover` entries each describe a piece of this spine (`executor`, `error-policy-retry-loop-cap`, `reactive-loops-and-lifecycle-handlers`, `auto-terminal-aggregate-resolution`). The spine itself is the load-bearing concept: a reader who knows each piece in isolation may still not see how an `Errored` event from `executors/claude-agent/` ends up calling `Abandon` on a postgres claim-producer five files later. This entry threads the flow.

### Stage 1 — Wire to internal terminal event

`foundation/integration/runner_dispatch.go::readExecutorStream` (lines 235-326) consumes the gRPC `ExecuteEvent` stream. Each terminal proto variant maps to a `terminalEvent{Kind, ...}` struct:

- `Complete{changed, attributes_delta}` → `terminalKindComplete` (lines 271-281).
- `Blocked{reason, context}` → `terminalKindBlocked` with `error_class="executor_blocked"` (lines 282-292).
- `Errored{error_class, payload}` → `terminalKindErrored` with the executor-supplied class verbatim (lines 293-303).
- `ParkRequested{...}` → `terminalKindPark` (lines 304-315).
- `AsyncAccepted{async_ack_id}` → `terminalKindAsyncAccepted`; the supervisor records the ack-id and waits for the HTTP callback (lines 316-323).
- Stream `io.EOF` or `Recv` error → `terminalKindInfra` with `error_class` `stream_closed_without_terminal` or `stream_error` (lines 244-258).

Named events emitted before the terminal are accumulated into `terminalEvent.NamedEvents` (lines 266-270) and persisted later (`runner_terminal.go::applyTerminal:52-54`) before the terminal verdict is applied.

Per `protocols/proto/v1/executor.proto:131-141`, exactly one terminal event closes the stream; the wire grammar is enforced at the proto level.

### Stage 2 — Dispatch on terminal kind

`runner_terminal.go::applyTerminal` (lines 43-70) is the omnibus dispatch. Five kinds, five branches:

| Kind | Handler | Stage-3 entry |
|------|---------|---------------|
| `terminalKindComplete` | `applyTerminalComplete` | `runQualityRules` → `on_executor_complete` → `releaseLocksInTx(success=true)` |
| `terminalKindBlocked` | `applyTerminalBlockedOrErrored("blocked")` | `OnExecutorBlocked` handler → `applyTerminalPass` / `applyTerminalAppError` |
| `terminalKindErrored` | `applyTerminalBlockedOrErrored("errored")` | `OnExecutorErrored` handler → `applyTerminalPass` / `applyTerminalAppError` |
| `terminalKindInfra` | `applyTerminalInfraError` | infra-reenqueue path (no error policy, no retry-counter bump) |
| `terminalKindPark` | `applyTerminalPark` | park-state transition; claim handles retained |

Before dispatch, `applyTerminal` increments the `rimsky_terminal_verdicts_total{class, error_class}` metric (line 56) and persists any pre-terminal named events (lines 52-54). Both happen even on failure paths.

### Stage 3 — Lifecycle handler

`runner_terminal_handlers.go::applyTerminalBlockedOrErrored` (lines 33-73) looks up the declared `OnExecutorBlocked` or `OnExecutorErrored` on `acq.NodeDef`. Three branches:

- **Handler nil or `resolve` empty** → fall through to `applyTerminalAppError(ctx, args, acq, errorClass, payload)` with the executor-supplied class. This is today's default behavior for templates that don't declare lifecycle handlers.
- **`resolve: pass`** → `applyTerminalPass` (lines 79-121): inside one tx, `releaseLocksInTx(success=false)` (Abandon on already-Open'd claims, like `handleOrphanedClaim`), `UpdateError({})` to clear evaluator state, `UpdateState(running→fresh, ReasonHandlerPass, LastOutcomePassed)`, and dequeue. Skips error_types routing entirely. Emits a `state_transition` event with `terminal_kind` and the original `error_class` in the payload for the audit trail. Optional `handler.Invalidate` fires unconditionally afterward.
- **`resolve: error`** → `applyTerminalAppError(ctx, args, acq, handler.ErrorClass-or-fallback, payload)` — overrides the executor-supplied class with a handler-declared class (line 58-61), then routes through Stage 4. Optional `handler.Invalidate` fires after `applyTerminalAppError` returns.

The `OnAcquireUnavailable` handler is parallel but lives upstream: `foundation/integration/runner_lifecycle.go::handleAcquireUnavailable` (lines 32-87+) runs *before* the dispatch path takes place at all — it's invoked when `tryAcquire` returns the `errAcquireUnavailable` sentinel (`runner_acquire.go:131,152-153`), and on `resolve=pass`/`error` it Abandons any already-Open'd claims by direct producer call (`runner_lifecycle.go:76`). The OnAcquireUnavailable path skips Stage 4 entirely because no executor call was made and no `error_class` from an executor exists.

### Stage 4 — Error policy chain (retry/invalidate/give_up)

`runner_terminal_errors.go::applyTerminalAppError` (lines 40-144) is the error policy dispatcher:

1. **Retry-loop guard** (lines 48-64). Before any policy lookup, `shouldForceRetryLoopGiveUp` (lines 352-375) checks `consecutive_retries_no_progress` against the effective cap (per-dispatch override → per-template `MaxRetriesWithoutProgress` → deployment default → built-in 100). If the cap is exceeded, the error class is rewritten to `retry_loop_no_progress`, and `give_up` is the only resolution possible. This short-circuit lives *here*, not in the handler chain, so any error class on any node is capped.
2. **Policy lookup**: `lookupPolicyForNode` (lines 261-273) reads `acq.NodeDef.ErrorTypes[errorClass]`. Nil = no policy → the default `Evaluate` falls back to give_up.
3. **`node.Evaluate(policy, state, errorClass, nil)`** (called at line 83) produces a `ResolvedAction{Kind, Targets, Frame, DelayMs, NewState}`. The function lives in `modeling/node/` (referenced as `node.Evaluate`).
4. **Counter housekeeping** (lines 90-103): the per-dispatch `consecutive_retries_no_progress` counter is read, bumped on `retry|discard_then_retry|resume_then_retry`, reset on `invalidate|give_up`. The carry-forward write happens *after* `releaseLocksInTx` and the queue mutation so the new dispatch row carries the count.
5. **In-tx body** (lines 105-122): `Nodes().UpdateError` writes the resolved evaluator state, `releaseLocksInTx(success=false)` fires the per-claim release (Stage 5), `applyResolvedAction` (lines 149-189) handles the per-Kind side effects.
6. **`applyResolvedAction`**: maps `Kind` to state + queue mutation. `retry`/`discard_then_retry`/`resume_then_retry` → `running→stale` + dequeue + re-enqueue with a delay; `invalidate` → `running→stale` + dequeue (the actual invalidate fan-out happens out-of-tx via `invalidateTargets`, lines 140-142); `give_up` → `running→failed` with `LastOutcomeFailed` + dequeue.

The application-error path always emits an `error` event with `error_class, action_taken, delay_ms` (lines 127-139).

### Stage 5 — Claim-handle resolution (the unified spine)

`runner_terminal_release.go::releaseLocksInTx` (lines 49-58) walks `acq.Locks`. For each lock, `releaseAcquiredLock` (lines 66-81) dispatches by spec:

- **`NamedLockSpec`** → claimant-guarded `ClaimHandles.Delete` (line 73). No producer verb because named locks have no out-of-process state.
- **`ClaimSpec` (held)** → `releaseClaim` (lines 91-149) takes the held branch (line 96): `markClaimHolderForNode` updates this node's `rimsky_claim_holders` row to `completed` or `failed`; on `!success` it calls `FailAllActiveByClaimHandle` to fail every still-active inheritor in the held subgraph (lines 110-114); then `CheckAndFireResolution` (auto-terminal entry point) decides whether the subgraph is complete and dispatches to the unified engine.
- **`ClaimSpec` (non-held)** → loads scope and address from the `rimsky_claim_handle` row (lines 120-131), computes `outcome = success ? AggregateCommit : AggregateAbandon`, and calls `ResolveClaimHandleTerminal` (lines 137-147) directly with `Source: ActiveTerminal`.

`releaseInheritedClaimsInTx` (lines 156-172) runs in the same tx and handles the case where this node is a non-acquirer member of a holding subgraph — it marks the inheritor's row and calls `CheckAndFireResolution` once per subgraph.

`foundation/integration/auto_terminal.go::CheckAndFireResolution` (the held branch) does the three-step held-resolution: `SELECT … FOR UPDATE` on the claim_handle row, list claim_holders to determine aggregate outcome (`any failed → Abandon; else Commit`), and call `ResolveClaimHandleTerminal` with `Source: HeldTerminal`.

`foundation/integration/terminal_decision.go::ResolveClaimHandleTerminal` (lines 110-135) is the bottom of the pipe — the single audited site that:

1. Calls `Producer.Commit(claimID, scope, address)` for `AggregateCommit` or `Producer.Abandon(claimID, scope, address)` for `AggregateAbandon` (lines 119-126).
2. Calls `args.ClaimHandles.Delete(claimHandleID, supervisorID, tx)` claimant-guarded (line 131; `@blessed-invariant 4`).

Both call paths (active-terminal and held-terminal) end here. Per spec §7.3, the producer verb runs in its own producer-side transaction; rimsky's bookkeeping tx commits the claim-handle DELETE independently. The verb-then-tx-fail leak is bounded by at-least-once delivery + claim_id idempotency on the producer side (`auto_terminal.go:43-50`; spec §7.8 obligation #3).

### Summary table — terminal kind to producer verb

| Terminal kind | Lifecycle handler | Final `success` to `releaseLocksInTx` | Active-claim verb | Held-claim aggregate |
|---|---|---|---|---|
| `Complete` (any) | `OnExecutorComplete` | `true` | `Commit` | `Commit` if all members completed |
| `Blocked` | nil / `resolve: error` | `false` | `Abandon` | `Abandon` if any member failed |
| `Blocked` | `resolve: pass` | `false` | `Abandon` (via `applyTerminalPass`) | mark + check (success=false fails the holder row) |
| `Errored` | nil / `resolve: error` | `false` | `Abandon` | `Abandon` if any member failed |
| `Errored` | `resolve: pass` | `false` | `Abandon` (via `applyTerminalPass`) | mark + check |
| `Infra` | n/a | `false` | `Abandon` | mark failed + check |
| `Park` | n/a (handled inline) | n/a — claims retained | none | none (claims retained across boundary) |
| `OnAcquireUnavailable` `pass/error` | n/a | n/a — release runs in `runner_lifecycle.go` | `Abandon` (direct producer call) | n/a |

A reader inspecting one of the four constituent entries sees only one row of this table. The spine is the whole table.

## Code surface

- `protocols/proto/v1/executor.proto:131-208` — the terminal-event proto grammar.
- `foundation/integration/runner_dispatch.go:235-326` — `readExecutorStream`: wire → `terminalEvent`.
- `foundation/integration/runner_terminal.go:43-70` — `applyTerminal` dispatch on `terminalKind`.
- `foundation/integration/runner_terminal_handlers.go:33-121` — `applyTerminalBlockedOrErrored` + `applyTerminalPass`.
- `foundation/integration/runner_terminal_errors.go:40-189` — `applyTerminalAppError` + retry guard + `applyResolvedAction`.
- `foundation/integration/runner_terminal_errors.go:191-257` — `applyTerminalInfraError`.
- `foundation/integration/runner_lifecycle.go:32-150` — `handleAcquireUnavailable` (the upstream sibling).
- `foundation/integration/runner_terminal_release.go:49-149` — `releaseLocksInTx` + `releaseClaim` (active vs held branching).
- `foundation/integration/auto_terminal.go:55-123` — `CheckAndFireResolution` (held-aggregate decision).
- `foundation/integration/terminal_decision.go:110-135` — `ResolveClaimHandleTerminal` (the single producer-verb + claim-handle DELETE site).
- `modeling/node/` — `Evaluate` (error-policy resolver), `OnExecutorErrored` / `OnExecutorBlocked` / `OnAcquireUnavailable` handler types.

## Prose surface

- `docs/concepts/handlers.md` — lifecycle-handler concept doc (Stage 3).
- `docs/concepts/error-policy.md` — error-types policy chain (Stage 4).
- `docs/concepts/holding-subgraph.md` — held-claim auto-terminal aggregate-outcome rule (Stage 5 held branch).
- `docs/concepts/claim-handle.md` — claim-handle row lifecycle (Stage 5 sink).
- `CLAUDE.md` "Vocabulary" — 5 node states, 4 lifecycle handlers, 3+ error actions.
- `CLAUDE.md` "Blessed invariants" §4, §13, §20 — the invariants this spine preserves.
- `.ok-planner/specs/2026-05-04-foundation-contract.md` §4.4 — terminal-verb idempotency requirement.
- `.ok-planner/specs/2026-05-05-reactive-loops-and-lifecycle-handlers-design.md` — the design that produced Stage 3.

## Adjacent topics

- `reactive-loops-and-lifecycle-handlers` — Stage 3 in isolation.
- `error-policy-retry-loop-cap` — Stage 4 in isolation.
- `2026-05-10-auto-terminal-aggregate-resolution` — Stage 5 (held branch) in isolation.
- `2026-05-10-state-machine-no-self-loop` — Stage 5 state transitions.
- `2026-05-10-claimant-guarded-release` — Stage 5 delete predicate.
- `2026-05-10-orphan-reaper-no-producer-abandon` — what happens when this spine is skipped because the supervisor crashed mid-flow.
- `2026-05-10-parked-state-and-resume` — the one terminal kind that opts out of Stage 5.

## Observations

- The spine has *two* convergence points, not one. `releaseLocksInTx` is the per-acquired-lock fan-out; `ResolveClaimHandleTerminal` is the per-claim-handle producer-verb call. The non-held active path takes both in sequence inside the same caller tx; the held path bounces through `CheckAndFireResolution` to potentially defer the second-stage call to a later node's terminal. Both ends share the unified engine — but the held path can produce *zero* producer-verb calls during this node's terminal handling (if siblings remain active), while the active path always produces exactly one per acquired claim.
- The `OnAcquireUnavailable` handler is the only branch that *doesn't* flow through `applyTerminal` because the executor was never called. Its `pass`/`error` resolutions still need to Abandon already-Open'd claims, so `runner_lifecycle.go::handleAcquireUnavailable` duplicates the per-claim Abandon logic instead of routing through `releaseLocksInTx`. A future refactor could unify these paths, but the duplication is currently tracked: the comment at `runner_terminal_handlers.go:75-77` explicitly notes that `applyTerminalPass` "mirrors handleOrphanedClaim's Abandon-then-clear".
- The retry-loop guard (`shouldForceRetryLoopGiveUp`) sits at Stage 4, *before* the lifecycle handler's `resolve: pass` short-circuit can fire. But `applyTerminalPass` doesn't call `applyTerminalAppError`, so a `resolve: pass` handler completely bypasses the retry-loop cap. A node configured with `on_executor_errored: { resolve: pass }` will pass forever, with no built-in safety net — by design, since `pass` is explicitly an opt-out from error routing.
- The `executor.proto`-level `AsyncAccepted` terminal does not reach Stage 2 directly. It signals "wait for HTTP callback"; the supervisor enters a wait state until the executor POSTs an `AsyncCallbackBody` to `${callback_url}/v1/callback/{async_ack_id}`. The callback body re-enters the spine through a different code path (`foundation/integration/callback.go`); the final `terminalEvent` produced there feeds back into `applyTerminal`. The spine described here is the only one — the async-callback path joins it at Stage 2.
- **Tension candidate (duplicated Abandon-on-pass-or-error logic):** the producer-verb-fire-on-pass-or-error path is implemented twice. Once in `runner_lifecycle.go::handleAcquireUnavailable` for the pre-dispatch unavailability case, once in `applyTerminalPass` for the post-executor case. The two paths use different release routines but both end up calling `Producer.Abandon` directly (not through `ResolveClaimHandleTerminal`). The unified engine is the bottom of the pipe for the *active* + *held* paths; the *pre-dispatch* and the *pass* paths do not currently route through it. This is the strongest remaining seam in the unification narrative documented at `terminal_decision.go:5-27`.
- **Tension candidate (`Blocked` vs `Errored` cosmetic difference):** Stage 1 wraps Blocked with `error_class="executor_blocked"` regardless of the executor's `reason` field; Errored carries the executor's `error_class` verbatim. Two terminal events end up using the same `applyTerminalBlockedOrErrored` path; the only behavioral difference is the auto-generated class. A reader expecting Blocked to be "softer" than Errored (e.g., not increment the retry counter) finds that both increment counter, both Abandon claims, both route through error_types.
