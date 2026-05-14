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

The mechanism that fires `ClaimProducer.Commit` or `Abandon` exactly once at the end of a held claim's holding-subgraph. Implementation lives in `runtime/auto_terminal.go::CheckAndFireResolution` and delegates to the unified `ResolveClaimHandleTerminal` engine in `terminal_decision.go`. Runs after every node terminal in a held subgraph: locks the `rimsky_claim_handles` row, checks whether all `rimsky_claim_holders` rows are non-active, computes aggregate outcome, fires the verb, deletes the handle claimant-guarded.

## Purpose

A held claim outlives its acquirer; somebody has to decide when to release it. The auto-terminal mechanism extracts that decision into one place driven by a deterministic predicate (aggregate of `rimsky_claim_holders.state`).

## Boundaries

Owns: the aggregate-outcome computation, the producer-verb dispatch, the post-fire delete of the handle row. Does NOT own: how each holder reaches its terminal (see `lifecycle-handler`), the verb's producer-side effect (see `claim-producer`), the active-terminal (non-held) branch that also routes through `ResolveClaimHandleTerminal` (see `terminal-resolution` for the unified spine). Adjacent: `claim-handle` (including its `### Held variant` subsection — the dropped `held-claim` concept's content lives there), `claim-producer`, `parked-state` (continues to fire across park), `terminal-resolution`.

## Invariants

- Exactly one resolution per held claim — enforced by `SELECT … FOR UPDATE` plus the row-existence check (`@blessed-invariant 13`).
- Aggregate-outcome rule: any-failed → `Abandon`; all-completed → `Commit`.
- The producer verb fires before the surrounding rimsky tx commits — verb-then-tx-fail leak path is mitigated by requiring terminal verbs to be idempotent in `claim_id`.
- Delete of the `rimsky_claim_handles` row is claimant-guarded (`AND holder_supervisor_id = supervisor_id`).
- Unified `ResolveClaimHandleTerminal` is the audited post-dispatch entry point for error-policy `pass`/`error` resolutions on already-Open'd claims. Two carve-outs route through the shared `abandonOpenedClaim` helper (`runtime/abandon_claim.go`) instead of the unified engine: (a) the pre-dispatch `OnAcquireUnavailable` `pass`/`error` path (`runner_lifecycle.go::abandonPartialLocks`), where the `rimsky_claim_handles` rows are already gone (rolled back by the acquisition tx) so the unified engine's delete step has nothing to do; and (b) the post-commit verify-before-run race-detection bail path (`runner_acquire.go::handleOrphanedClaim`), where the cleanup is per-acquired-claim Abandon + its own claimant-guarded `ClaimHandles.Delete` outside the unified engine's verb-then-delete tx sequence. All three sites share the helper, so any future audit / telemetry hook there fires uniformly.

## Aliases and historical names

The `lock_holder_id` FK column on the holders table is renamed to `claim_handle_id`. Pre-Phase-5 the same algorithm ran against `rimsky_lock_holders`; post-baseline-rebase the table is `rimsky_claim_handles` (plural). The `Delete` is on `table:rimsky_claim_handles`.

## Open within this concept

(no live tensions distinct from `claim-handle`)

