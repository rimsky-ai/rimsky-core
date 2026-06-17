---
concept: parked-state
status: as-is
aliases:
  - park
  - parked node
---

# Parked state

## What it is

`parked` is the fifth legal node state, entered from `running` when the executor emits a park outcome. While parked, the node is not running and not failed; it carries `parked_reason`, optional `parked_reason_label`, optional `parked_reason_note`, and optional `resume_at`. The corresponding node-run phase is `'parked'`.

### Park-flavored signals

Park terminals emit canonical signals per `concept:signal`: `terminal/park/snooze` and `terminal/park/await_callback` (the two park-reason values). The freeform `parked_reason_label` is a payload field on both signals, not a separate column-form distinction at the subscriber boundary. The await-async-callback outcome is NOT a park (the node stays `running` during the callback wait); it emits `transient/await_async` and is covered under `concept:signal`'s transient subtree.

### Resume context

Parked nodes carry no dedicated resume context. On re-dispatch (whether time-wake via the parked sweep or cascade-invalidate via an upstream event), the executor receives the dispatch with the standard `ExecuteRequest.attributes` populated by attribute carry-forward. Executors that need state across a park-and-resume cycle write it to `attributes_delta` on the Park terminal and read it from incoming attributes on re-dispatch. The two exit paths (time-wake when `resume_at` has passed; external invalidate via admin endpoint or in-graph subscription match) still emit `terminal/park/<reason>` signals as before, but no per-row payload or session token is threaded through to the re-dispatch.

(The third exit path, watchdog timeout, does not re-dispatch — it forces `failed{error_class: "park_timeout"}`.)

## Purpose

Some workloads (human review, scheduled wake, external event wait) cannot finish in a bounded window. `parked` gives them a first-class hold state with explicit resume semantics, instead of forcing them through `failed`+retry (which loses session context) or keeping a gRPC stream open indefinitely.

## Boundaries

Owns: the hold-state schema (the park fields on the node-run row), the three exit paths (time-wake, external invalidate, watchdog timeout). Does NOT own: held-claim resolution (that's `auto-terminal`); orphan reaping (parked rows are explicitly skipped). Adjacent: `node-run`, `auto-terminal`, `claim-handle` (including its held variant).

## Invariants

- Parked nodes emit `terminal/park/*` signals; subscribers decide whether to react (propagation is determined by subscriber matches against the emitted signal, not by sender color).
- The orphan-claim reaper skips `phase='parked'` rows because parked rows are settled with respect to liveness (no quiet-period or RPC connection state to observe).
- Time-wake and external-invalidate both transition `parked → stale` (never directly to `running`); the next supervisor tick re-dispatches. Watchdog timeout is the one destructive exit (`parked → failed` with `error_class: "park_timeout"`).
- Held-claim auto-terminal continues to fire correctly across park because the claim-holder's state stays `'active'` while the node is parked.

## Common pitfalls

- **Indefinite human-review park inside an in-flight frame.** A common pattern is "produce a tentative output, then park indefinitely (no `resume_at`) waiting for an operator to invalidate." Authoring this with `parked_reason: OTHER` and `parked_reason_label: "human_review"` is supported and correct — but parking a frame on review serializes parallel work in the same frame and creates long-lived held frames. The recommended idiom is **post-frame review**: the producing frame runs to completion; review happens externally; a follow-on graph or instance kicks off the post-review work. Frame-blocking review should be reserved for cases where downstream genuinely cannot proceed safely without approval (e.g. cross-system commit where the alternative is to reverse-cascade after the fact).
- Treating an operator-supplied reason string as a first-class park-reason. The park-reason surface is the two enum values (snooze and await-callback) plus a freeform reason-label payload field. Operator-supplied reasons like a human-review label belong in the freeform reason-label slot under the await-callback enum value; they are not first-class enum values.
