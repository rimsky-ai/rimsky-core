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

The end-to-end spine that takes a single executor stream-close event off the wire and converges it onto exactly four decisions: (1) what `last_outcome` to stamp on the node and therefore whether cascade fires, (2) what to do with the node-run row (delete vs retry-enqueue vs invalidate-targets), (3) what producer verb (`Commit` / `Abandon` / nothing) to fire on every acquired claim, (4) when to delete the `rimsky_claim_handles` rows claimant-guarded. Five stages stitched across `runtime/`:

> **Vocabulary note (post-`spec:2026-05-12-nomenclature-resolution` Group E.2):** "Terminal" is no longer a wire-protocol term. The proto layer carries a single `StreamClose` event with an outcome `oneof` (`Success | Error | Snooze | AwaitAsyncCallback`); the executor closes the stream immediately after `StreamClose`. The word "terminal" persists in two narrower senses: (a) the state-machine sense — `node-state` terminal states (`fresh`, `failed`) and the `code:runtime/terminal_decision.go::ResolveClaimHandleTerminal` decision-engine entry point; and (b) this concept's name as the convergence-spine umbrella. The internal classification kind `terminalKind` is a supervisor-internal categorization, not a wire shape.

1. **Wire to internal terminal kind** — `runner_dispatch.go::readExecutorStream` maps each `StreamClose` outcome variant to an internal `terminalEvent{Kind, ...}` struct: `Complete | Errored | Park | Infra`. Named events emitted before stream-close accumulate into `terminalEvent.NamedEvents` and persist before the verdict is applied.
2. **Dispatch on terminal kind** — `runner_terminal.go::applyTerminal` routes the four kinds (`Complete`, `Errored`, `Infra`, `Park`) to their per-kind handlers and increments `rimsky_terminal_verdicts_total{class, error_class}`.
3. **Lifecycle handler** — `runner_terminal_handlers.go::applyTerminalError` checks `acq.NodeDef.OnExecutorErrored`; resolves to `pass` (→ `applyTerminalPass`, Abandon + clear + running→fresh), `error` (→ `applyErrorPolicy` with handler-declared error_class), or fall-through to `applyErrorPolicy` with the executor-supplied class. (Post-E.10, the `on_executor_blocked` slot is retired; every error variant — including the `executor_blocked` error_class synthesized from the legacy "Blocked" pattern — routes through `on_executor_errored`.)
4. **Error policy chain** — `runner_error_policy.go::applyErrorPolicy`. Guards with the retry-loop cap, looks up `acq.NodeDef.ErrorTypes[errorClass]`, calls `node.Evaluate(policy, state, errorClass, nil)` → `ResolvedAction{Kind, Targets, Frame, DelayMs, NewState}`, then `applyResolvedAction` maps Kind to state + queue mutation.
5. **Claim-handle resolution** — `runner_terminal_release.go::releaseLocksInTx` walks `acq.Locks`. NamedLockSpec → claimant-guarded `ClaimHandles.Delete` only. Non-held `ClaimSpec` → `ResolveClaimHandleTerminal` directly with `Source: ActiveTerminal`. Held `ClaimSpec` → mark `rimsky_claim_holders` row + `CheckAndFireResolution`; if the holding subgraph is complete, that engine computes aggregate outcome (any failed → Abandon; else Commit) and calls `ResolveClaimHandleTerminal` with `Source: HeldTerminal`. The unified `terminal_decision.go::ResolveClaimHandleTerminal` fires the producer verb and deletes the `rimsky_claim_handles` row claimant-guarded — the single audited site for both call paths.

Two upstream siblings sit outside the unified engine but share the same `abandonOpenedClaim` helper (`runtime/abandon_claim.go`):

- `OnAcquireUnavailable` (`runner_lifecycle.go::handleAcquireUnavailable`) runs *before* dispatch when `tryAcquire` returns the `errAcquireUnavailable` sentinel, and on `pass`/`error` it Abandons already-Open'd partial claims via the helper. The carve-out exists because the acquisition tx has already rolled back — the `rimsky_claim_handles` rows are gone, so there is no claimant-guarded delete to fold into the unified engine.
- The verify-before-run bail path (`runner_acquire.go::handleOrphanedClaim`) runs *after* the acquisition tx committed but before the executor was dispatched. Its per-claim Abandon (via the helper) is followed by a claimant-guarded `ClaimHandles.Delete` owned by the caller, outside the unified engine's verb-then-delete tx sequence.

Post-dispatch terminal paths (`OnExecutorErrored` `pass`) route through `releaseLocksInTx` → `ResolveClaimHandleTerminal`, which calls the same helper for its Abandon branch (and adds the claimant-guarded `rimsky_claim_handles` delete after the verb).

### Terminal kind → producer verb

| Terminal kind | Lifecycle handler | success → release | Active-claim verb | Held-claim aggregate |
|---|---|---|---|---|
| `Complete` | `OnExecutorComplete` | true | `Commit` | `Commit` if all completed |
| `Errored` | nil / `error` | false | `Abandon` | `Abandon` if any failed |
| `Errored` | `pass` | false | `Abandon` (via `applyTerminalPass`) | mark + check |
| `Infra` | n/a | false | `Abandon` | mark failed + check |
| `Park` | n/a | n/a | none — claims retained | none — claims retained |
| `OnAcquireUnavailable` pass/error | n/a | n/a | `Abandon` (via `abandonOpenedClaim` helper) | n/a |
| `handleOrphanedClaim` (verify-before-run race) | n/a | n/a | `Abandon` (via `abandonOpenedClaim` helper) | n/a |

## Purpose

The four constituent concepts each describe one stage; none on its own makes visible how an `Errored` event from `executors/claude-agent/` ends up calling `Abandon` on a postgres claim-producer five files later. This concept threads the spine so a reader can trace a single terminal event from the wire through to the producer verb and the claim-handle row deletion.

## Boundaries

Owns: the five-stage flow as one coherent narrative, the kind→verb table, the convergence-point story (two convergence points: `releaseLocksInTx` per-acquired-lock fan-out, `ResolveClaimHandleTerminal` per-claim-handle producer-verb site). Does NOT own: any stage's internals (those are the constituent concepts). Adjacent: `executor`, `lifecycle-handler`, `error-policy`, `auto-terminal`, `claim-handle`, `last-outcome`, `parked-state`.

## Invariants

- Exactly one `StreamClose` event closes the executor stream (`proto:executor.proto::Execute`); the executor MUST close the stream immediately after.
- Every kind except `Park` and `AwaitAsyncCallback` flows through `applyTerminal` and ends in `releaseLocksInTx` for the dispatch's acquired locks.
- `ResolveClaimHandleTerminal` is the single audited site that fires `Producer.Commit` / `Abandon` *and* deletes the `rimsky_claim_handles` row claimant-guarded (`@blessed-invariant 4`). Both the active-terminal and held-terminal paths converge here.
- The retry-loop cap (`shouldForceRetryLoopGiveUp`) at Stage 4 short-circuits before policy lookup; `resolve: pass` at Stage 3 bypasses Stage 4 entirely, so a `pass` handler is not subject to the retry-loop cap by design.
- `AwaitAsyncCallback` re-enters the spine through `runtime/callback.go`; the final `terminalEvent` produced there feeds back into `applyTerminal`.

## Aliases and historical names

The "auto-terminal" name applies specifically to the held-claim branch of Stage 5 (`auto-terminal-aggregate-resolution`). The spine as a whole has no canonical name in the source; this concept introduces "terminal resolution" as the umbrella. Pre-2026-05-12 the wire proto had separate `Complete`, `Blocked`, `Errored`, `ParkRequested`, `AsyncAccepted` per-terminal messages; post-E.2 the wire shape is `StreamClose{outcome: Success | Error | Snooze | AwaitAsyncCallback}` and the supervisor's internal `terminalKind` synthesizes the legacy `executor_blocked` error_class from the new `Error.error_class` field.

## Open within this concept

(none live; the `Blocked`-vs-`Errored` routing tension was resolved by `spec:2026-05-12-nomenclature-resolution` Group E.2 / E.9 / E.10.)

## Notes

- Wire-event vocabulary updated for the post-`spec:2026-05-12-nomenclature-resolution` Group E.2 proto restructure (`StreamClose` + outcome oneof; `applyTerminalError` replaces `applyTerminalBlockedOrErrored`; `applyErrorPolicy` replaces `applyTerminalAppError`). The five-stage spine narrative survives unchanged at the supervisor-internal level; only the wire shape and the error-handler function names move.

