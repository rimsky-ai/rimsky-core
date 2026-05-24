---
concept: terminal-resolution
status: as-is
aliases:
  - executor-terminal-spine
references:
  - _discover/terminal-resolution.md
  - _discover/2026-05-10-auto-terminal-aggregate-resolution.md
  - _discover/error-policy-retry-loop-cap.md
  - _discover/reactive-loops-and-lifecycle-handlers.md
  - _discover/2026-05-10-executor-streamed-execute.md
---

# Terminal resolution

## What it is

The end-to-end spine that takes a single executor stream-close event off the wire and converges it onto exactly four decisions: (1) what canonical signal type-path to emit (and therefore what `Resolution.Color` to stamp on the run row, which carries the `last_outcome` projection through Pass 5), (2) what to do with the node-run row (delete vs retry-enqueue), (3) what producer verb (`Commit` / `Abandon` / nothing) to fire on every acquired claim, (4) when to delete the `rimsky_claim_handles` rows claimant-guarded. Four stages stitched across `runtime/`. The same four-stage spine handles executor `Error` and runtime acquisition failure uniformly — acquisition-failure routes through the operator's `error_types:` chain via synthetic-class `acquire/*` (see `concept:error-policy`).

> **Vocabulary note (post-`spec:2026-05-12-nomenclature-resolution` Group E.2):** "Terminal" is no longer a wire-protocol term. The proto layer carries a single `StreamClose` event with an outcome `oneof` (`Success | Error | Park | AwaitAsyncCallback`); the executor closes the stream immediately after `StreamClose`. The word "terminal" persists in two narrower senses: (a) the state-machine sense — `node-state` terminal states (`fresh`, `failed`) and the `code:runtime/terminal_decision.go::ResolveClaimHandleTerminal` decision-engine entry point; and (b) this concept's name as the convergence-spine umbrella. The internal classification kind `terminalKind` is a supervisor-internal categorization, not a wire shape.

1. **Wire to internal terminal kind** — `runner_dispatch.go::readExecutorStream` maps each `StreamClose` outcome variant to an internal `terminalEvent{Kind, ...}` struct: `Complete | Errored | Park | Infra`. Named events emitted before stream-close accumulate into `terminalEvent.NamedEvents` and persist before the verdict is applied.
2. **Dispatch on terminal kind** — `runner_terminal.go::applyTerminal` routes the four kinds (`Complete`, `Errored`, `Infra`, `Park`) to their per-kind handlers and increments `rimsky_terminal_verdicts_total{class, error_class}`. Acquisition failure (pre-dispatch) routes through `runner_lifecycle.go::handleAcquireUnavailable` into the same Stage-3 entry point via synthetic class `acquire/unavailable`.
3. **Resolution** — produces the canonical `spec.Resolution{Signal, DispatchDisposition, Color, ...}` 3-tuple per `concept:error-policy`. Runs the operator's `error_types:` chain when the terminal kind is `Errored` or when the synthetic `acquire/*` class is in play (`runner_error_policy.go::applyErrorPolicy` → `node.Evaluate` → `buildResolution`). For `Complete` / `Park` / `AwaitAsyncCallback` / `Infra` the resolution is fixed by the terminal kind — no operator-configurable policy chain.
4. **Claim-handle resolution** — `runner_terminal_release.go::releaseLocksInTx` walks `acq.Locks`. NamedLockSpec → claimant-guarded `ClaimHandles.Delete` only. Non-held `ClaimSpec` → `ResolveClaimHandleTerminal` directly with `Source: ActiveTerminal`. Held `ClaimSpec` → mark `rimsky_claim_holders` row + `CheckAndFireResolution`; if the holding subgraph is complete, that engine computes aggregate outcome (any failed → Abandon; else Commit) and calls `ResolveClaimHandleTerminal` with `Source: HeldTerminal`. The unified `terminal_decision.go::ResolveClaimHandleTerminal` fires the producer verb and deletes the `rimsky_claim_handles` row claimant-guarded — the single audited site for both call paths.

Two upstream siblings sit outside the unified engine but share the same `abandonOpenedClaim` helper (`runtime/abandon_claim.go`):

- `handleAcquireUnavailable` (`runner_lifecycle.go`) runs *before* dispatch when `tryAcquire` returns the `errAcquireUnavailable` sentinel. Post-2026-05-23 it Abandons already-Open'd partial claims via the helper and routes through `OnError` with synthetic class `acquire/unavailable` for state-machine + queue mutation. The carve-out exists because the acquisition tx has already rolled back — the `rimsky_claim_handles` rows are gone, so there is no claimant-guarded delete to fold into the unified engine.
- The verify-before-run bail path (`runner_acquire.go::handleOrphanedClaim`) runs *after* the acquisition tx committed but before the executor was dispatched. Its per-claim Abandon (via the helper) is followed by a claimant-guarded `ClaimHandles.Delete` owned by the caller, outside the unified engine's verb-then-delete tx sequence.

### Terminal kind → emitted signal → producer verb

| Terminal kind | Emitted signal type-path | Active-claim verb | Held-claim aggregate |
|---|---|---|---|
| `Complete` | `terminal/success` | `Commit` | `Commit` if all completed |
| `Errored` | `terminal/error/<class>` (give_up / pass) or `transient/retry/<n>/<class>` (retry) | `Abandon` on give_up; preserved on retry | `Abandon` if any failed |
| `Infra` | `terminal/infra/<reason>` | `Abandon` | mark failed + check |
| `Park` | `terminal/park/snooze` or `terminal/park/await_callback` | none — claims retained | none — claims retained |
| `AwaitAsyncCallback` (transient) | `transient/await_async` | none — no settling verb on first pass | none — callback's eventual terminal drives verb emission |
| Acquisition failure (pre-dispatch) | `terminal/error/acquire/unavailable` | `Abandon` partial-acquired (via helper) | n/a |
| `handleOrphanedClaim` (verify-before-run race) | (no signal — admin path) | `Abandon` (via helper) | n/a |

## Purpose

The four constituent concepts each describe one stage; none on its own makes visible how an `Errored` event from `executors/claude-agent/` ends up calling `Abandon` on a postgres claim-producer five files later. This concept threads the spine so a reader can trace a single terminal event from the wire through to the producer verb and the claim-handle row deletion.

## Boundaries

Owns: the four-stage flow as one coherent narrative, the kind→signal-type-path→verb table, the convergence-point story (two convergence points: `releaseLocksInTx` per-acquired-lock fan-out, `ResolveClaimHandleTerminal` per-claim-handle producer-verb site). Does NOT own: any stage's internals (those are the constituent concepts). Adjacent: `executor`, `signal`, `error-policy`, `auto-terminal`, `claim-handle`, `last-outcome`, `parked-state`.

## Invariants

- Exactly one `StreamClose` event closes the executor stream (`proto:executor.proto::Execute`); the executor MUST close the stream immediately after.
- Every kind except `Park` and `AwaitAsyncCallback` flows through `applyTerminal` and ends in `releaseLocksInTx` for the dispatch's acquired locks.
- `ResolveClaimHandleTerminal` is the single audited site that fires `Producer.Commit` / `Abandon` *and* deletes the `rimsky_claim_handles` row claimant-guarded (`@blessed-invariant 4`). Both the active-terminal and held-terminal paths converge here.
- The retry-loop cap (`shouldForceRetryLoopGiveUp`) at Stage 3 short-circuits before policy lookup. A per-class `pass` action in `error_types:` settles the run as fresh and ends the dispatch without retry — bypassing the cap by design.
- `AwaitAsyncCallback` re-enters the spine through `runtime/callback.go`; the final `terminalEvent` produced there feeds back into `applyTerminal`.

## Aliases and historical names

The "auto-terminal" name applies specifically to the held-claim branch of Stage 4 (`auto-terminal-aggregate-resolution`). The spine as a whole has no canonical name in the source; this concept introduces "terminal resolution" as the umbrella. Pre-2026-05-12 the wire proto had separate `Complete`, `Blocked`, `Errored`, `ParkRequested`, `AsyncAccepted` per-terminal messages; post-E.2 the wire shape is `StreamClose{outcome: Success | Error | Park | AwaitAsyncCallback}` (with `Park.reason ∈ {AWAIT_CALLBACK, SNOOZE}`) and the supervisor's internal `terminalKind` synthesizes the legacy `executor_blocked` error_class from the new `Error.error_class` field.

## Open within this concept

(none live; the `Blocked`-vs-`Errored` routing tension was resolved by `spec:2026-05-12-nomenclature-resolution` Group E.2 / E.9 / E.10.)

## Notes

- Wire-event vocabulary updated for the post-`spec:2026-05-12-nomenclature-resolution` Group E.2 proto restructure (`StreamClose` + outcome oneof; `applyTerminalError` replaces `applyTerminalBlockedOrErrored`; `applyErrorPolicy` replaces `applyTerminalAppError`). The five-stage spine narrative survives unchanged at the supervisor-internal level; only the wire shape and the error-handler function names move.
- 2026-05-23 — Reshape per spec `2026-05-23-signal-taxonomy-and-policy-decoupling-design`. Resolution shape becomes `(signal, dispatch_disposition, color)` per `concept:error-policy`. Acquisition failure folds into the same spine via synthetic `acquire/*` error class. `concept:lifecycle-handler` retires; `on_executor_complete` / `on_executor_errored` / `on_acquire_unavailable` slots delete. Five-stage flow collapses to four (the lifecycle-handler stage absorbed into resolution). Kind→verb table refreshed to include emitted signal type-paths per `concept:signal`. Snooze→Park drift in the wire-shape note corrected (`StreamClose.outcome` is `Success | Error | Park | AwaitAsyncCallback`, with `Park.reason ∈ {AWAIT_CALLBACK, SNOOZE}`).
