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

The end-to-end spine that takes a single executor stream-close event off the wire and converges it onto exactly four decisions: (1) what canonical signal type-path to emit (and therefore what resolution color to stamp on the run row, which carries the last-outcome projection through Pass 5), (2) what to do with the node-run row (delete vs retry-enqueue), (3) what producer verb (`Commit` / `Abandon` / nothing) to fire on every acquired claim, (4) when to delete the persisted claim-handle rows claimant-guarded. Four stages stitched across the runtime. The same four-stage spine handles the executor error outcome and runtime acquisition failure uniformly — acquisition-failure routes through the operator's `error_types:` chain via synthetic-class `acquire/*` (see `concept:error-policy`).

> **Vocabulary note (post-`spec:2026-05-12-nomenclature-resolution` Group E.2):** "Terminal" is no longer a wire-protocol term. The wire layer carries a single stream-close event with one of four outcome variants (success, error, park, await-async-callback); the executor closes the stream immediately after that event. The word "terminal" persists in two narrower senses: (a) the state-machine sense — `concept:node-run` terminal states (`fresh`, `failed`) and the unified claim-handle resolution decision-engine entry point; and (b) this concept's name as the convergence-spine umbrella. The internal terminal-kind classification is a supervisor-internal categorization, not a wire shape.

1. **Wire to internal terminal kind** — the executor-stream reader maps each stream-close outcome variant to an internal terminal-event of kind `Complete | Errored | Park | Infra`. Named events emitted before stream-close accumulate onto the terminal event and persist before the verdict is applied.
2. **Dispatch on terminal kind** — the terminal-application step routes the four kinds (`Complete`, `Errored`, `Infra`, `Park`) to their per-kind handlers and increments a per-class terminal-verdict counter. Acquisition failure (pre-dispatch) routes through the acquire-unavailable handler into the same Stage-3 entry point via synthetic class `acquire/unavailable`.
3. **Resolution** — produces the canonical `(signal, dispatch_disposition, color)` resolution tuple per `concept:error-policy`. Runs the operator's `error_types:` chain when the terminal kind is `Errored` or when the synthetic `acquire/*` class is in play. For `Complete` / `Park` / await-async-callback / `Infra` the resolution is fixed by the terminal kind — no operator-configurable policy chain.
4. **Claim-handle resolution** — the lock-release step walks the dispatch's acquired locks. A named-lock acquisition → claimant-guarded handle delete only. A non-held claim → the unified claim-handle resolution directly, with an active-terminal source. A held claim → mark the persisted claim-holder row + check-and-fire; if the holding subgraph is complete, that engine computes the aggregate outcome (any failed → Abandon; else Commit) and calls the unified claim-handle resolution with a held-terminal source. The unified claim-handle resolution fires the producer verb and deletes the persisted claim-handle row claimant-guarded — the single audited site for both call paths.

Two upstream siblings sit outside the unified engine but share the same abandon-opened-claim helper:

- The acquire-unavailable handler runs *before* dispatch when the acquisition attempt returns the unavailable sentinel. Post-2026-05-23 it Abandons already-Open'd partial claims via the helper and routes through the error path with synthetic class `acquire/unavailable` for state-machine + queue mutation. The carve-out exists because the acquisition tx has already rolled back — the persisted claim-handle rows are gone, so there is no claimant-guarded delete to fold into the unified engine.
- The verify-before-run bail path runs *after* the acquisition tx committed but before the executor was dispatched. Its per-claim Abandon (via the helper) is followed by a claimant-guarded handle delete owned by the caller, outside the unified engine's verb-then-delete tx sequence.

### Terminal kind → emitted signal → producer verb

| Terminal kind | Emitted signal type-path | Active-claim verb | Held-claim aggregate |
|---|---|---|---|
| `Complete` | `terminal/success` | `Commit` | `Commit` if all completed |
| `Errored` | `terminal/error/<class>` (give_up / pass) or `transient/retry/<n>/<class>` (retry) | `Abandon` on give_up; preserved on retry | `Abandon` if any failed |
| `Infra` | `terminal/infra/<reason>` | `Abandon` | mark failed + check |
| `Park` | `terminal/park/snooze` or `terminal/park/await_callback` | none — claims retained | none — claims retained |
| await-async-callback (transient) | `transient/await_async` | none — no settling verb on first pass | none — callback's eventual terminal drives verb emission |
| Acquisition failure (pre-dispatch) | `terminal/error/acquire/unavailable` | `Abandon` partial-acquired (via helper) | n/a |
| Verify-before-run race (orphaned-claim bail) | (no signal — admin path) | `Abandon` (via helper) | n/a |

## Purpose

The four constituent concepts each describe one stage; none on its own makes visible how an `Errored` event from an executor ends up calling `Abandon` on a claim-producer several steps later. This concept threads the spine so a reader can trace a single terminal event from the wire through to the producer verb and the claim-handle row deletion.

## Boundaries

Owns: the four-stage flow as one coherent narrative, the kind→signal-type-path→verb table, the convergence-point story (two convergence points: the per-acquired-lock fan-out at lock release, and the per-claim-handle producer-verb site at the unified claim-handle resolution). Does NOT own: any stage's internals (those are the constituent concepts). Adjacent: `concept:executor`, `concept:signal`, `concept:error-policy`, `concept:auto-terminal`, `concept:claim-handle`, `concept:parked-state`.

## Invariants

- Exactly one stream-close event closes the executor stream (carried on the executor protocol's execute method); the executor MUST close the stream immediately after.
- Every kind except `Park` and await-async-callback flows through the terminal-application step and ends in the lock-release step for the dispatch's acquired locks.
- The unified claim-handle resolution is the single audited site that fires the producer `Commit` / `Abandon` verb *and* deletes the persisted claim-handle row claimant-guarded (`@blessed-invariant 4`). Both the active-terminal and held-terminal paths converge here.
- The retry-loop cap at Stage 3 short-circuits before policy lookup. A per-class `pass` action in `error_types:` settles the run as fresh and ends the dispatch without retry — bypassing the cap by design.
- The await-async-callback outcome re-enters the spine through the callback path; the final terminal event produced there feeds back into the terminal-application step.

## Aliases and historical names

The "auto-terminal" name applies specifically to the held-claim branch of Stage 4 (`auto-terminal-aggregate-resolution`). The spine as a whole has no canonical name in the source; this concept introduces "terminal resolution" as the umbrella. Pre-2026-05-12 the wire proto had separate per-terminal messages (complete, blocked, errored, park-requested, async-accepted); post-E.2 the wire shape is a single stream-close event carrying one outcome variant (success, error, park, await-async-callback), with the park outcome's reason drawn from the closed set `AWAIT_CALLBACK | SNOOZE`, and the supervisor's internal terminal-kind classification synthesizes the legacy `executor_blocked` error class from the error outcome's error-class field.

## Open within this concept

(none live; the blocked-vs-errored routing tension was resolved by `spec:2026-05-12-nomenclature-resolution` Group E.2 / E.9 / E.10 — see `tension:_resolved/blocked-vs-errored-routing`.)

## Notes

- Wire-event vocabulary updated for the post-`spec:2026-05-12-nomenclature-resolution` Group E.2 proto restructure (a single stream-close event + an outcome variant; the error-handling step was renamed and the app-error handler folded into the error-policy step). The five-stage spine narrative survives unchanged at the supervisor-internal level; only the wire shape and the internal handler names move.
- 2026-05-23 — Reshape per spec:2026-05-23-signal-taxonomy-and-policy-decoupling. Resolution shape becomes `(signal, dispatch_disposition, color)` per `concept:error-policy`. Acquisition failure folds into the same spine via synthetic `acquire/*` error class. The now-retired lifecycle-handler concept retires; the per-lifecycle-event handler slots (on-executor-complete, on-executor-errored, on-acquire-unavailable) delete. Five-stage flow collapses to four (the lifecycle-handler stage absorbed into resolution). Kind→verb table refreshed to include emitted signal type-paths per `concept:signal`. The snooze→park drift in the wire-shape note corrected (the executor's four outcome variants are success, error, park, await-async-callback, with the park outcome's reason drawn from the closed set `AWAIT_CALLBACK | SNOOZE`).
- 2026-05-25 — Codebase citations removed + cross-refs repaired for self-containment per spec:2026-05-25-concept-doc-self-containment.
