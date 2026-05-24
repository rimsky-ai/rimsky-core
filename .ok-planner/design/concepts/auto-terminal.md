---
concept: auto-terminal
status: as-is
aliases:
  - held-claim resolution
references:
  - _discover/2026-05-10-auto-terminal-aggregate-resolution.md
  - _discover/2026-05-10-claimant-guarded-release.md
---

# Auto-terminal

## What it is

The mechanism that fires `ClaimProducer.Commit` or `Abandon` exactly once at the end of a held claim's holding-subgraph. Implementation lives in `runtime/auto_terminal.go::CheckAndFireResolution` and delegates to the unified `ResolveClaimHandleTerminal` engine in `terminal_decision.go`. Runs after every node terminal in a held subgraph: locks the `rimsky_claim_handles` row, checks whether all `rimsky_claim_holders` rows are non-active, computes aggregate outcome, fires the verb, **promotes** the handle to `state = 'committed'` or `'abandoned'` claimant-guarded against the supervisor that held it. Carve-out paths (`abandonOpenedClaim` in pre-dispatch / verify-before-run bail) continue to `Delete` directly because those rows never went through `Promote`.

## Purpose

A held claim outlives its acquirer; somebody has to decide when to release it. The auto-terminal mechanism extracts that decision into one place driven by a deterministic predicate (aggregate of `rimsky_claim_holders.state`).

## Boundaries

Owns: the aggregate-outcome computation, the producer-verb dispatch, the post-fire delete of the handle row. Does NOT own: how each holder reaches its terminal (see `error-policy` for retry/pass/give_up and `runner_terminal.go::applyTerminalComplete` for successful executor terminals), the verb's producer-side effect (see `claim-producer`), the active-terminal (non-held) branch that also routes through `ResolveClaimHandleTerminal` (see `terminal-resolution` for the unified spine). Adjacent: `claim-handle` (including its `### Held variant` subsection — the dropped `held-claim` concept's content lives there), `claim-producer`, `parked-state` (continues to fire across park), `terminal-resolution`, `error-policy`.

## Invariants

- Exactly one resolution per held claim — enforced by `SELECT … FOR UPDATE` plus the row-state check (`@blessed-invariant 13`).
- Aggregate-outcome rule: any-failed → `Abandon`; all-completed → `Commit`.
- The producer verb fires before the surrounding rimsky tx commits — verb-then-tx-fail leak path is mitigated by requiring terminal verbs to be idempotent in `claim_id`.
- State transition of the `rimsky_claim_handles` row uses **two guard shapes** (`@blessed-invariant 4` post-2026-05-17 refactor):
  - Active-row mutations (Promote, ExtendHeartbeat, carve-out Delete in `abandonOpenedClaim`) are claimant-guarded with `AND holder_supervisor_id = supervisor_id`.
  - Non-active-row deletions (retention sweep, asset Release path) are absence-guarded: the row has `holder_supervisor_id IS NULL` by construction (post-Promote nulled it); the row-discovery query filter substitutes for the per-row claimant check.
- Unified `ResolveClaimHandleTerminal` is the audited post-dispatch entry point for error-policy `pass`/`error` resolutions on already-Open'd claims. Two carve-outs route through the shared `abandonOpenedClaim` helper (`runtime/abandon_claim.go`) instead of the unified engine: (a) the pre-dispatch `OnAcquireUnavailable` `pass`/`error` path (`runner_lifecycle.go::abandonPartialLocks`), where the `rimsky_claim_handles` rows are already gone (rolled back by the acquisition tx); and (b) the post-commit verify-before-run race-detection bail path (`runner_acquire.go::handleOrphanedClaim`), where the cleanup is per-acquired-claim Abandon + its own claimant-guarded `ClaimHandles.Delete`. Those rows never went through Promote, so they take the Delete-direct path; the unified engine's Promote path is the standard.

## Aliases and historical names

The `lock_holder_id` FK column on the holders table is renamed to `claim_handle_id`. Pre-Phase-5 the same algorithm ran against `rimsky_lock_holders`; post-baseline-rebase the table is `rimsky_claim_handles` (plural). Pre-2026-05-17 the main path was `Delete` (active-row claimant-guarded); post-refactor it's `Promote` (active → committed/abandoned) followed by retention-sweep or Release-path absence-guarded deletion later.

## Open within this concept

(no live tensions distinct from `claim-handle`)

## Notes

State-column refactor per `spec:2026-05-17-post-data-platform-cleanup`: the main auto-terminal path moved from `Delete` to `Promote`; the row is preserved past terminal for forensics. Carve-out paths (`abandonOpenedClaim`) still `Delete` directly because those rows never went through `Promote`.

