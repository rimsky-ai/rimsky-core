---
concept: terminal-resolution
status: as-is
aliases:
  - executor-terminal-spine
---

# Terminal resolution

## What it is

The end-to-end spine that takes a single executor Outcome off the wire and converges it onto exactly four decisions: (1) what canonical signal type-path to emit (and the verdict's `tags` set as the discriminator that subscribers CEL-filter against), (2) whether the node-run row settles — state-transitioned and its dispatch entry removed from the queue — or the dispatch retries in place with the node-run row and its queue entry both preserved (see `decision:in-place-retry`), (3) what producer verb (`Commit` / `Abandon` / nothing) to fire on every acquired claim, (4) when to delete the persisted claim-handle rows claimant-guarded. Four stages stitched across the runtime. The same four-stage spine handles the executor error outcome and runtime acquisition failure uniformly — acquisition-failure routes through the operator's `error_types:` chain via the producer-declared class else the synthetic acquisition class (see `concept:error-policy`).

> **Vocabulary note.** "Terminal" is not a wire-protocol term. The wire layer carries a single Outcome with one of four variants (success, error, park, await-async-callback); the unary RPC returns the Outcome directly. The word "terminal" is reserved for two narrower senses: (a) the state-machine sense — the `concept:node-run` terminal states and the unified claim-handle resolution decision-engine entry point; and (b) this concept's name as the convergence-spine umbrella. The internal terminal-kind classification is a supervisor-internal categorization, not a wire shape.

1. **Wire to internal terminal kind** — the executor-outcome reader maps each of the wire's four Outcome variants to its own internal terminal-kind: success to completion, error to error, park to park, await-async-callback to async-accepted. A nil or otherwise unrecognized Outcome synthesizes a fifth internal terminal-kind, infra, so every dispatch path resolves to a terminal-kind even when the wire contract is violated. The settling verdict's tag set rides into the cascade-walk's CEL filter on the tag set (see `concept:terminal-tag`).
2. **Dispatch on terminal kind** — the terminal-application step routes the four kinds (completion, error, infra, park) to their per-kind handlers and increments a per-class terminal-verdict counter. Acquisition failure (pre-dispatch) routes through the acquire-unavailable handler into the same Stage-3 entry point via the producer-declared class else the synthetic acquisition class.
3. **Resolution** — produces the canonical resolution tuple of signal, dispatch disposition, and color per `concept:error-policy`. Runs the operator's per-class error-policy chain when the terminal kind is an error or when an acquisition-failure class (the producer-declared class else the synthetic acquisition class) is in play. For completion, park, await-async-callback, and infra the resolution is fixed by the terminal kind — no operator-configurable policy chain.
4. **Claim-handle resolution** — the lock-release step walks the dispatch's acquired locks. A named-lock acquisition → claimant-guarded handle delete only. A non-held claim → the unified claim-handle resolution directly, with an active-terminal source. A held claim → mark the persisted claim-holder row + check-and-fire; if the holding subgraph is complete, that engine computes the aggregate outcome (any failed → Abandon; else Commit) and calls the unified claim-handle resolution with a held-terminal source. The verify-before-run bail (the supervisor discovers post-commit that another supervisor stole the dispatch and unwinds the acquisition it just opened) also calls the unified claim-handle resolution, with an ownership-bail source — under that source the engine fires Abandon and deletes the handle row claimant-guarded, emitting no signal (admin path). The unified claim-handle resolution fires the producer verb and resolves the persisted claim-handle row claimant-guarded — the single audited verb-then-delete site for all three sources.

One carve-out sits outside the unified engine but shares the same abandon-opened-claim helper: the acquire-unavailable handler. It runs *before* dispatch, when the acquisition attempt returns the unavailable sentinel. It Abandons already-Open'd partial claims via the helper and routes through the error path with the producer-declared class else the synthetic acquisition class for state-machine + queue mutation. The carve-out exists because the acquisition tx has already rolled back — the persisted claim-handle rows are gone, so there is no claimant-guarded delete to fold into the unified engine, and folding it anyway would force the engine to grow a no-rows mode that dilutes its single audited verb-then-delete promise.

### Terminal kind → emitted signal → producer verb

| Terminal kind | Emitted signal | Active-claim verb | Held-claim aggregate |
|---|---|---|---|
| Completion | Success terminal | Commit | Commit if all completed |
| Error | Per-class error terminal (give-up or pass paths) or per-class transient retry signal | Abandon on give-up; preserved on retry | Abandon if any failed |
| Infra | Per-reason infrastructure terminal | Abandon | mark failed + check |
| Park | Park terminal (time-wake at resume-at) | none — claims retained | none — claims retained |
| Await-async-callback (transient) | Await-async transient signal | none — no settling verb on first pass | none — callback's eventual terminal drives verb emission |
| Acquisition failure (pre-dispatch) | Per-class error terminal (producer-declared class else the synthetic acquisition class) | Abandon partial-acquired (via helper — the single carve-out outside the unified engine) | n/a |
| Verify-before-run race (orphaned-claim bail) | (no signal — admin path) | Abandon (via the unified engine, ownership-bail source: verb then claimant-guarded delete) | n/a |

## Purpose

The four constituent concepts each describe one stage; none on its own makes visible how an `Errored` event from an executor ends up calling `Abandon` on a claim-producer several steps later. This concept threads the spine so a reader can trace a single terminal event from the wire through to the producer verb and the claim-handle row deletion.

## Boundaries

Owns: the four-stage flow as one coherent narrative, the kind→signal-type-path→verb table, the convergence-point story (two convergence points: the per-acquired-lock fan-out at lock release, and the per-claim-handle producer-verb site at the unified claim-handle resolution). Does NOT own: any stage's internals (those are the constituent concepts). Adjacent: `concept:executor`, `concept:signal`, `concept:terminal-tag`, `concept:error-policy`, `concept:auto-terminal`, `concept:claim-handle`, `concept:parked-state`.

## Invariants

- The unary `Execute` RPC returns exactly one Outcome carrying one of four variants — Success / Error / Park / AwaitAsyncCallback.
- Every kind except `Park` and await-async-callback flows through the terminal-application step; a `retry` disposition loops in place on the dispatch's already-acquired locks without releasing them, while `give_up`, `pass`, and `release_and_requeue` dispositions all end in the lock-release step for the dispatch's acquired locks (see `concept:error-policy`).
- The unified claim-handle resolution is the single audited site that fires the producer `Commit` / `Abandon` verb *and* resolves the persisted claim-handle row claimant-guarded (invariant 4). Its source kinds are active-terminal, held-terminal, and ownership-bail — all three converge here. The ownership-bail source deletes the row (the acquisition is unwound, not resolved) and emits no signal; the verb always fires before the row transition.
- The acquire-unavailable handler is the single carve-out outside the unified claim-handle resolution: its acquisition transaction has already rolled back, so no claim-handle rows exist and only the shared abandon-opened-claim helper fires against the producer's partial opens.
- The retry-loop cap at Stage 3 short-circuits before policy lookup. A per-class pass action in the operator's error-policy chain settles the run as cleanly-resolved and ends the dispatch without retry — bypassing the cap by design.
- The await-async-callback outcome re-enters the spine through the callback path; the final terminal event produced there feeds back into the terminal-application step.
