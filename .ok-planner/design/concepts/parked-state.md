---
concept: parked-state
status: as-is
aliases:
  - park
  - parked node
---

# Parked state

## What it is

Parked is the fifth legal node state, entered from running when the executor emits a park outcome. While parked, the node is not running and not failed; it carries a park-reason discriminator, an optional reason-label, an optional reason-note, and an optional resume-at timestamp. The corresponding node-run is in the parked lifecycle phase.

### Park-flavored signals

Park terminals emit canonical signals per `concept:signal` — one per park-reason value. The freeform reason-label is a payload field on those signals, not a separate column-form distinction at the subscriber boundary. The await-async-callback outcome is NOT a park (the node stays running during the callback wait); it emits a transient await-async signal and is covered under `concept:signal`'s transient subtree.

### Resume context

Parked nodes carry no dedicated resume context. On re-dispatch (whether time-wake via the parked sweep or cascade-invalidate via an upstream event), the executor receives the dispatch with attributes populated by attribute carry-forward. Executors that need state across a park-and-resume cycle write it via the attribute-delta of the Park terminal and read it from incoming attributes on re-dispatch. The two exit paths (time-wake when the resume-at has passed; external invalidate via admin endpoint or in-graph subscription match) still emit the original park-reason terminal signals as before, but no per-row payload or session token is threaded through to the re-dispatch.

(The third exit path, watchdog timeout, does not re-dispatch — it forces a failed terminal with a park-timeout error class.)

### Time-wake resume preserves the dispatch-time snapshot

The time-wake exit path transitions the node from `parked` to a distinct `resuming` state. The dispatcher reloads the node-run's persisted attribute bag — initialized at the original dispatch from the substitution context built against the node's subscribed upstreams — and re-invokes the executor with that bag verbatim, skipping the substitution rebuild. This holds the rule that a parked node-run is a mid-resolve state, not a fresh dispatch: the executor's view of its upstreams is fixed for the lifetime of the node-run, regardless of upstream activity during the park.

(The in-graph subscription-match exit path continues to transition from `parked` to `stale` and rebuild substitution at the next dispatch. That path delivers cascade information the parked subscriber declared interest in, and so re-evaluates against current upstream state by design.)

## Purpose

Some workloads (human review, scheduled wake, external event wait) cannot finish in a bounded window. `parked` gives them a first-class hold state with explicit resume semantics, instead of forcing them through `failed`+retry (which loses session context) or keeping a gRPC stream open indefinitely.

## Boundaries

Owns: the hold-state schema (the park fields on the node-run row), the three exit paths (time-wake, external invalidate, watchdog timeout). Does NOT own: held-claim resolution (that's `auto-terminal`); orphan reaping (parked rows are explicitly skipped). Adjacent: `node-run`, `auto-terminal`, `claim-handle` (including its held variant).

## Invariants

- Parked nodes emit park-family terminal signals (see `concept:signal`); subscribers decide whether to react (propagation is determined by subscriber matches against the emitted signal, not by sender color).
- The orphan-claim reaper skips parked rows because parked rows are settled with respect to liveness (no quiet-period or dispatch-channel connection state to observe).
- Time-wake transitions the node from parked to a `resuming` state; the next supervisor tick re-dispatches with the persisted dispatch-time attribute bag. External invalidate via in-graph subscription match transitions the node from parked to stale; the next supervisor tick re-dispatches with a freshly built substitution context. Watchdog timeout is the one destructive exit (parked to failed, with a park-timeout error class). None of the resume paths transition directly to running.
- The substitution-resolved attribute bag persisted at original dispatch is the executor's input for every invocation of the node-run, including the time-wake resume. Upstream activity during the park does not rewrite that bag.
- Held-claim auto-terminal continues to fire correctly across park because the claim-holder's state stays active while the node is parked.

## Common pitfalls

- **Indefinite human-review park inside an in-flight frame.** A common pattern is "produce a tentative output, then park indefinitely (no resume-at) waiting for an operator to invalidate." Authoring this with the await-callback reason discriminator and a human-review reason-label is supported and correct — but parking a frame on review serializes parallel work in the same frame and creates long-lived held frames. The recommended idiom is **post-frame review**: the producing frame runs to completion; review happens externally; a follow-on graph or instance kicks off the post-review work. Frame-blocking review should be reserved for cases where downstream genuinely cannot proceed safely without approval (e.g. cross-system commit where the alternative is to reverse-cascade after the fact).
- Treating an operator-supplied reason string as a first-class park-reason. The park-reason surface is the two enum values (snooze and await-callback) plus a freeform reason-label payload field. Operator-supplied reasons like a human-review label belong in the freeform reason-label slot under the await-callback enum value; they are not first-class enum values.
