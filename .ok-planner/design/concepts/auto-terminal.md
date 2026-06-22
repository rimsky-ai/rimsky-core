---
concept: auto-terminal
status: as-is
aliases:
  - held-claim resolution
---

# Auto-terminal

## What it is

The mechanism that fires the producer's Commit or Abandon verb exactly once at the end of a held claim's holding-subgraph. It delegates to the unified claim-handle terminal-resolution engine. Runs after every node terminal in a held subgraph: locks the claim-handle row, checks whether all of that handle's holder rows are non-active, computes aggregate outcome, fires the verb, **promotes** the handle to a committed or abandoned state claimant-guarded against the supervisor that held it.

**Auto-terminal is the cascade-fire moment for held node-runs.** Per `decision:held-as-state-not-phase`, a held node-run's `running → held` transition does NOT fire the cascade walker. The walker is invoked here instead, at the same atomic moment the handle row is promoted:

- Commit: the held holder node-runs transition `held → fresh`, and the cascade walker fires `terminal/success` (or the run's emitted terminal-type if explicit) downstream for each.
- Abandon: the held holder node-runs transition `held → failed`, and the cascade walker fires `terminal/error/abandoned` downstream for each — a new uniform error-class signal (see `decision:terminal-error-abandoned-as-error-class`). Downstream subscribers see `terminal/error/abandoned` (or match the wildcard `terminal/error/*`) and react to the rollback.

The verify-before-run ownership bail routes through the same engine with its own source kind, under which the engine deletes (rather than promotes) the row claimant-guarded — a bailed acquisition is unwound, not resolved, so its row never reaches a resolved state. The single carve-out outside the engine is the pre-dispatch acquire-unavailable path, whose rows were already removed by the acquisition transaction's rollback; only the shared abandon helper fires there.

## Purpose

A held claim outlives its acquirer; somebody has to decide when to release it. The auto-terminal mechanism extracts that decision into one place driven by a deterministic predicate (the aggregate of the holder rows' states).

## Boundaries

Owns: the aggregate-outcome computation, the producer-verb dispatch, the post-fire delete of the handle row. Does NOT own: how each holder reaches its terminal (see `error-policy` for retry/pass/give_up and the successful-executor-terminal handler for clean completions), the verb's producer-side effect (see `claim-producer`), the active-terminal (non-held) branch that also routes through the same unified resolution engine (see `terminal-resolution` for the unified spine). Adjacent: `claim-handle` (including its `### Held variant` subsection), `claim-producer`, `parked-state` (continues to fire across park), `terminal-resolution`, `error-policy`.

## Invariants

- Exactly one resolution per held claim — enforced by a row-locking select plus the row-state check.
- Aggregate-outcome rule: any-failed → Abandon; all-completed → Commit.
- The producer verb fires before the surrounding rimsky tx commits — the verb-then-tx-fail leak path is mitigated by requiring terminal verbs to be idempotent in the claim id.
- Auto-terminal Commit fires the cascade walker with `terminal/success` for each held node-run in the holding subgraph, transitioning each `held → fresh` in the same tx. Auto-terminal Abandon fires the cascade walker with `terminal/error/abandoned` for each, transitioning each `held → failed`. Downstream subscribers never observe a held node-run's terminal before this moment.
- State transition of the claim-handle row uses **two guard shapes** (invariant 4):
  - Active-row mutations (Promote, liveness-extend, the ownership-bail delete inside the unified engine) are claimant-guarded by matching the holder-supervisor id against the acting supervisor.
  - Non-active-row deletions (retention sweep, asset Release path) are absence-guarded: the row's holder-supervisor id is null by construction (Promote nulled it); the row-discovery query filter substitutes for the per-row claimant check.
- The unified resolution engine is the audited entry point for resolving already-opened claims; its source kinds are active-terminal, held-terminal, and ownership-bail (the post-commit verify-before-run race-detection bail, under which the engine fires the per-acquired-claim Abandon and then deletes the row claimant-guarded instead of promoting it). One carve-out routes through the shared abandon helper instead of the unified engine: the pre-dispatch acquire-unavailable path, where the claim-handle rows are already gone (rolled back by the acquisition tx), so there is no row transition to fold.
- **Poison rule (forward propagation).** Aggregate-outcome resolves to Abandon the moment any holder reaches `failed`, not when the last holder settles. At the resolution moment, every still-active holder's co-holder row is marked `failed` in the same transaction as the handle promotion. Holders whose own execution is still in flight at this moment will, when they eventually settle, route to node-run state `failed` with `terminal/error/abandoned` regardless of what their executor returned — once poisoned, a holder cannot revert the aggregate, and its outputs are treated as part of an abandoned coordinated unit (see `concept:claim-handle`'s Held-variant invariants).
- **Member-vs-non-member cascade routing.** The cascade walks fired by Commit and Abandon target only subscribers that are NOT themselves members of the holding subgraph for this claim. Holding-subgraph members are coordinated internally through the held machinery and through this handler's own per-member state transition (`held → fresh` for Commit, `held → failed` for Abandon); they do not also receive a downstream cascade from a sibling member's terminal — firing one would double-fire. Non-members are the audience for the deferred resolution signal, and they observe it as a single event at this atomic moment.
