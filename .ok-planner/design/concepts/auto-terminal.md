---
concept: auto-terminal
status: as-is
aliases:
  - held-claim resolution
---

# Auto-terminal

## What it is

The mechanism that fires the producer's Commit or Abandon verb exactly once at the end of a held claim's holding-subgraph. It delegates to the unified claim-handle terminal-resolution engine. Runs after every node terminal in a held subgraph: locks the claim-handle row, checks whether all of that handle's holder rows are non-active, computes aggregate outcome, fires the verb, **promotes** the handle to a committed or abandoned state claimant-guarded against the supervisor that held it. Carve-out paths (the shared abandon helper used in pre-dispatch and verify-before-run bail) continue to delete the row directly because those rows never went through Promote.

## Purpose

A held claim outlives its acquirer; somebody has to decide when to release it. The auto-terminal mechanism extracts that decision into one place driven by a deterministic predicate (the aggregate of the holder rows' states).

## Boundaries

Owns: the aggregate-outcome computation, the producer-verb dispatch, the post-fire delete of the handle row. Does NOT own: how each holder reaches its terminal (see `error-policy` for retry/pass/give_up and the successful-executor-terminal handler for clean completions), the verb's producer-side effect (see `claim-producer`), the active-terminal (non-held) branch that also routes through the same unified resolution engine (see `terminal-resolution` for the unified spine). Adjacent: `claim-handle` (including its `### Held variant` subsection — the dropped held-claim concept's content lives there), `claim-producer`, `parked-state` (continues to fire across park), `terminal-resolution`, `error-policy`.

## Invariants

- Exactly one resolution per held claim — enforced by a row-locking select plus the row-state check (`@blessed-invariant 13`).
- Aggregate-outcome rule: any-failed → Abandon; all-completed → Commit.
- The producer verb fires before the surrounding rimsky tx commits — the verb-then-tx-fail leak path is mitigated by requiring terminal verbs to be idempotent in the claim id.
- State transition of the claim-handle row uses **two guard shapes** (`@blessed-invariant 4`):
  - Active-row mutations (Promote, heartbeat-extend, carve-out delete in the abandon helper) are claimant-guarded by matching the holder-supervisor id against the acting supervisor.
  - Non-active-row deletions (retention sweep, asset Release path) are absence-guarded: the row's holder-supervisor id is null by construction (Promote nulled it); the row-discovery query filter substitutes for the per-row claimant check.
- The unified resolution engine is the audited post-dispatch entry point for error-policy `pass`/`error` resolutions on already-opened claims. Two carve-outs route through the shared abandon helper instead of the unified engine: (a) the pre-dispatch acquire-unavailable `pass`/`error` path, where the claim-handle rows are already gone (rolled back by the acquisition tx); and (b) the post-commit verify-before-run race-detection bail path, where the cleanup is a per-acquired-claim Abandon plus its own claimant-guarded delete. Those rows never went through Promote, so they take the delete-direct path; the unified engine's Promote path is the standard.
