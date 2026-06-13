---
concept: terminal-resolution
status: as-is
aliases:
  - executor-terminal-spine
---

# Terminal resolution

## What it is

The end-to-end spine that takes a single executor stream-close event off the wire and converges it onto exactly four decisions: (1) what canonical signal type-path to emit (and therefore what resolution color to stamp on the run row, which carries the last-outcome projection through Pass 5), (2) what to do with the node-run row (delete vs retry-enqueue), (3) what producer verb (`Commit` / `Abandon` / nothing) to fire on every acquired claim, (4) when to delete the persisted claim-handle rows claimant-guarded. Four stages stitched across the runtime. The same four-stage spine handles the executor error outcome and runtime acquisition failure uniformly — acquisition-failure routes through the operator's `error_types:` chain via the producer-declared class else the synthetic acquisition class (see `concept:error-policy`).

> **Vocabulary note.** "Terminal" is not a wire-protocol term. The wire layer carries a single stream-close event with one of four outcome variants (success, error, park, await-async-callback); the executor closes the stream immediately after that event. The word "terminal" is reserved for two narrower senses: (a) the state-machine sense — `concept:node-run` terminal states (`fresh`, `failed`) and the unified claim-handle resolution decision-engine entry point; and (b) this concept's name as the convergence-spine umbrella. The internal terminal-kind classification is a supervisor-internal categorization, not a wire shape.

1. **Wire to internal terminal kind** — the executor-stream reader maps each stream-close outcome variant to an internal terminal-event of kind `Complete | Errored | Park | Infra`. Named events emitted before stream-close accumulate onto the terminal event and persist before the verdict is applied.
2. **Dispatch on terminal kind** — the terminal-application step routes the four kinds (`Complete`, `Errored`, `Infra`, `Park`) to their per-kind handlers and increments a per-class terminal-verdict counter. Acquisition failure (pre-dispatch) routes through the acquire-unavailable handler into the same Stage-3 entry point via the producer-declared class else the synthetic acquisition class.
3. **Resolution** — produces the canonical `(signal, dispatch_disposition, color)` resolution tuple per `concept:error-policy`. Runs the operator's `error_types:` chain when the terminal kind is `Errored` or when an acquisition-failure class (the producer-declared class else the synthetic acquisition class) is in play. For `Complete` / `Park` / await-async-callback / `Infra` the resolution is fixed by the terminal kind — no operator-configurable policy chain.
4. **Claim-handle resolution** — the lock-release step walks the dispatch's acquired locks. A named-lock acquisition → claimant-guarded handle delete only. A non-held claim → the unified claim-handle resolution directly, with an active-terminal source. A held claim → mark the persisted claim-holder row + check-and-fire; if the holding subgraph is complete, that engine computes the aggregate outcome (any failed → Abandon; else Commit) and calls the unified claim-handle resolution with a held-terminal source. The verify-before-run bail (the supervisor discovers post-commit that another supervisor stole the dispatch and unwinds the acquisition it just opened) also calls the unified claim-handle resolution, with an ownership-bail source — under that source the engine fires Abandon and deletes the handle row claimant-guarded, emitting no signal (admin path). The unified claim-handle resolution fires the producer verb and resolves the persisted claim-handle row claimant-guarded — the single audited verb-then-delete site for all three sources.

One carve-out sits outside the unified engine but shares the same abandon-opened-claim helper: the acquire-unavailable handler. It runs *before* dispatch, when the acquisition attempt returns the unavailable sentinel. It Abandons already-Open'd partial claims via the helper and routes through the error path with the producer-declared class else the synthetic acquisition class for state-machine + queue mutation. The carve-out exists because the acquisition tx has already rolled back — the persisted claim-handle rows are gone, so there is no claimant-guarded delete to fold into the unified engine, and folding it anyway would force the engine to grow a no-rows mode that dilutes its single audited verb-then-delete promise.

### Terminal kind → emitted signal → producer verb

| Terminal kind | Emitted signal type-path | Active-claim verb | Held-claim aggregate |
|---|---|---|---|
| `Complete` | `terminal/success` | `Commit` | `Commit` if all completed |
| `Errored` | `terminal/error/<class>` (give_up / pass) or `transient/retry/<n>/<class>` (retry) | `Abandon` on give_up; preserved on retry | `Abandon` if any failed |
| `Infra` | `terminal/infra/<reason>` | `Abandon` | mark failed + check |
| `Park` | `terminal/park/snooze` or `terminal/park/await_callback` | none — claims retained | none — claims retained |
| await-async-callback (transient) | `transient/await_async` | none — no settling verb on first pass | none — callback's eventual terminal drives verb emission |
| Acquisition failure (pre-dispatch) | `terminal/error/<producer-declared class, else the synthetic acquisition class>` | `Abandon` partial-acquired (via helper — the single carve-out outside the unified engine) | n/a |
| Verify-before-run race (orphaned-claim bail) | (no signal — admin path) | `Abandon` (via the unified engine, ownership-bail source: verb then claimant-guarded delete) | n/a |

## Purpose

The four constituent concepts each describe one stage; none on its own makes visible how an `Errored` event from an executor ends up calling `Abandon` on a claim-producer several steps later. This concept threads the spine so a reader can trace a single terminal event from the wire through to the producer verb and the claim-handle row deletion.

## Boundaries

Owns: the four-stage flow as one coherent narrative, the kind→signal-type-path→verb table, the convergence-point story (two convergence points: the per-acquired-lock fan-out at lock release, and the per-claim-handle producer-verb site at the unified claim-handle resolution). Does NOT own: any stage's internals (those are the constituent concepts). Adjacent: `concept:executor`, `concept:signal`, `concept:error-policy`, `concept:auto-terminal`, `concept:claim-handle`, `concept:parked-state`.

## Invariants

- Exactly one stream-close event closes the executor stream (carried on the executor protocol's execute method); the executor MUST close the stream immediately after.
- Every kind except `Park` and await-async-callback flows through the terminal-application step and ends in the lock-release step for the dispatch's acquired locks.
- The unified claim-handle resolution is the single audited site that fires the producer `Commit` / `Abandon` verb *and* resolves the persisted claim-handle row claimant-guarded (`@blessed-invariant 4`). Its source kinds are active-terminal, held-terminal, and ownership-bail — all three converge here. The ownership-bail source deletes the row (the acquisition is unwound, not resolved) and emits no signal; the verb always fires before the row transition.
- The acquire-unavailable handler is the single carve-out outside the unified claim-handle resolution: its acquisition transaction has already rolled back, so no claim-handle rows exist and only the shared abandon-opened-claim helper fires against the producer's partial opens.
- The retry-loop cap at Stage 3 short-circuits before policy lookup. A per-class `pass` action in `error_types:` settles the run as fresh and ends the dispatch without retry — bypassing the cap by design.
- The await-async-callback outcome re-enters the spine through the callback path; the final terminal event produced there feeds back into the terminal-application step.
