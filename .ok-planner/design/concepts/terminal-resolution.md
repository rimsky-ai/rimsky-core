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

The end-to-end spine that takes a single executor terminal event off the wire and converges it onto exactly four decisions: (1) what `last_outcome` to stamp on the node and therefore whether cascade fires, (2) what to do with the dispatch row (delete vs retry-enqueue vs invalidate-targets), (3) what producer verb (`Commit` / `Abandon` / nothing) to fire on every acquired claim, (4) when to delete the `rimsky_claim_handle` rows claimant-guarded. Five stages stitched across `foundation/integration/`:

1. **Wire to internal terminal event** — `runner_dispatch.go::readExecutorStream` (lines 235-326) maps each proto-level `ExecuteEvent` variant to a `terminalEvent{Kind, ...}` struct: `Complete | Blocked | Errored | ParkRequested | AsyncAccepted | Infra`. Named events emitted before the terminal accumulate into `terminalEvent.NamedEvents` and persist before the terminal verdict is applied.
2. **Dispatch on terminal kind** — `runner_terminal.go::applyTerminal` (lines 43-70) routes the five kinds (`Complete`, `Blocked`, `Errored`, `Infra`, `Park`) to their per-kind handlers and increments `rimsky_terminal_verdicts_total{class, error_class}`.
3. **Lifecycle handler** — `runner_terminal_handlers.go::applyTerminalBlockedOrErrored` (lines 33-73) checks `acq.NodeDef.OnExecutorBlocked` / `OnExecutorErrored`; resolves to `pass` (→ `applyTerminalPass`, Abandon + clear + running→fresh), `error` (→ `applyTerminalAppError` with handler-declared error_class), or fall-through to `applyTerminalAppError` with the executor-supplied class.
4. **Error policy chain** — `runner_terminal_errors.go::applyTerminalAppError` (lines 40-144). Guards with the retry-loop cap, looks up `acq.NodeDef.ErrorTypes[errorClass]`, calls `node.Evaluate(policy, state, errorClass, nil)` → `ResolvedAction{Kind, Targets, Frame, DelayMs, NewState}`, then `applyResolvedAction` maps Kind to state + queue mutation.
5. **Claim-handle resolution** — `runner_terminal_release.go::releaseLocksInTx` walks `acq.Locks`. NamedLockSpec → claimant-guarded `ClaimHandles.Delete` only. Non-held `ClaimSpec` → `ResolveClaimHandleTerminal` directly with `Source: ActiveTerminal`. Held `ClaimSpec` → mark `rimsky_claim_holders` row + `CheckAndFireResolution`; if the holding subgraph is complete, that engine computes aggregate outcome (any failed → Abandon; else Commit) and calls `ResolveClaimHandleTerminal` with `Source: HeldTerminal`. The unified `terminal_decision.go::ResolveClaimHandleTerminal` (lines 110-135) fires the producer verb and deletes the `rimsky_claim_handle` row claimant-guarded — the single audited site for both call paths.

The `OnAcquireUnavailable` handler is the upstream sibling (`runner_lifecycle.go::handleAcquireUnavailable`): it runs *before* dispatch when `tryAcquire` returns the `errAcquireUnavailable` sentinel, and on `pass`/`error` it Abandons already-Open'd claims by direct producer call rather than routing through `releaseLocksInTx`.

### Terminal kind → producer verb

| Terminal kind | Lifecycle handler | success → release | Active-claim verb | Held-claim aggregate |
|---|---|---|---|---|
| `Complete` | `OnExecutorComplete` | true | `Commit` | `Commit` if all completed |
| `Blocked` | nil / `error` | false | `Abandon` | `Abandon` if any failed |
| `Blocked` | `pass` | false | `Abandon` (via `applyTerminalPass`) | mark + check |
| `Errored` | nil / `error` | false | `Abandon` | `Abandon` if any failed |
| `Errored` | `pass` | false | `Abandon` (via `applyTerminalPass`) | mark + check |
| `Infra` | n/a | false | `Abandon` | mark failed + check |
| `Park` | n/a | n/a | none — claims retained | none — claims retained |
| `OnAcquireUnavailable` pass/error | n/a | n/a | `Abandon` (direct producer call) | n/a |

## Purpose

The four constituent concepts each describe one stage; none on its own makes visible how an `Errored` event from `executors/claude-agent/` ends up calling `Abandon` on a postgres claim-producer five files later. This concept threads the spine so a reader can trace a single terminal event from the wire through to the producer verb and the claim-handle row deletion.

## Boundaries

Owns: the five-stage flow as one coherent narrative, the kind→verb table, the convergence-point story (two convergence points: `releaseLocksInTx` per-acquired-lock fan-out, `ResolveClaimHandleTerminal` per-claim-handle producer-verb site). Does NOT own: any stage's internals (those are the constituent concepts). Adjacent: `executor`, `lifecycle-handler`, `error-policy`, `auto-terminal`, `claim-handle`, `last-outcome`, `parked-state`.

## Invariants

- Exactly one terminal event closes the executor stream (`protocols/proto/v1/executor.proto:131-141`).
- Every kind except `Park` and `AsyncAccepted` flows through `applyTerminal` and ends in `releaseLocksInTx` for the dispatch's acquired locks.
- `ResolveClaimHandleTerminal` is the single audited site that fires `Producer.Commit` / `Abandon` *and* deletes the `rimsky_claim_handle` row claimant-guarded (`@blessed-invariant 4`). Both the active-terminal and held-terminal paths converge here.
- The retry-loop cap (`shouldForceRetryLoopGiveUp`) at Stage 4 short-circuits before policy lookup; `resolve: pass` at Stage 3 bypasses Stage 4 entirely, so a `pass` handler is not subject to the retry-loop cap by design.
- `AsyncAccepted` re-enters the spine through `foundation/integration/callback.go`; the final `terminalEvent` produced there feeds back into `applyTerminal`.

## Aliases and historical names

The "auto-terminal" name applies specifically to the held-claim branch of Stage 5 (`auto-terminal-aggregate-resolution`). The spine as a whole has no canonical name in the source; this concept introduces "terminal resolution" as the umbrella.

## Open within this concept

- The producer-Abandon-on-pass-or-error path is implemented twice — once in `runner_lifecycle.go::handleAcquireUnavailable` for pre-dispatch unavailability, once in `applyTerminalPass` for the post-executor case. Neither routes through `ResolveClaimHandleTerminal`. See `tensions/abandon-on-pass-duplicated-path.md`.
- `Blocked` and `Errored` differ only cosmetically in this spine — both flow through `applyTerminalBlockedOrErrored` and both Abandon and both increment the retry counter — see `tensions/blocked-vs-errored-routing.md`.

