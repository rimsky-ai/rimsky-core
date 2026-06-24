---
concept: parked-state
status: as-is
aliases:
  - park
  - parked node
---

# Parked state

## What it is

Parked is one of the seven node-run states (see `concept:node-run`), entered from `running` when the executor emits a park outcome. While parked, the node is not running and not failed; it carries a park-reason discriminator, an optional reason-label, an optional reason-note, and an optional resume-at timestamp. `parked` is in the in-flight set (`{pending, stale, running, held, parked}`) — cascade events targeting a parked node-run create a NEW cascade-driven pending row per `concept:cascade`; they never mutate the parked row's state or bag.

### Park-flavored signals

Park emits canonical signals per `concept:signal` — one per park-reason value, under the audit-only `transient/park/*` family. Park is dispatch-internal (the dispatch ends but the run continues), so park signals never fire cascade and never carry `attributes_delta`; template registration rejects subscriptions whose `type:` explicitly targets `transient/park/*`. The freeform reason-label is a payload field on those signals, not a separate column-form distinction at the subscriber boundary. The await-async-callback outcome is NOT a park (the node stays running during the callback wait); it emits a transient await-async signal and is covered under `concept:signal`'s transient subtree.

### Resume context

Parked nodes carry no dedicated resume context. On time-wake re-dispatch, the executor receives the same persisted attribute bag it saw at the original dispatch (loaded by run-id from the run's attribute store per `concept:attribute`; see "Time-wake resume preserves the dispatch-time snapshot" below). Executors that need to thread executor-managed state across a park-and-resume cycle use scratch: the parker writes opaque bytes to the Park outcome's scratch field, which the supervisor persists on the parked row's scratch slot. The same row re-dispatches at time-wake (no row-copy step — the parked row IS the resume row), so the resumed executor reads its scratch off the same dispatch's scratch slot via its ExecuteRequest. Attribute writeback is not available on Park because Park is dispatch-internal and writes nothing to the per-run attribute row (per `decision:uniform-attributes-delta`). No per-row payload or session token is threaded through to the re-dispatch.

A cascade event that would have "woken" a parked subscriber under the old model now creates a fresh cascade-driven pending row for the receiver, leaving the parked row's lifecycle intact. The pending row dispatches in dispatcher serialization order after the parked row exits (time-wake or watchdog).

(The watchdog timeout exit path does not re-dispatch — it forces a failed terminal with a park-timeout error class.)

### Time-wake resume preserves the dispatch-time snapshot

The time-wake exit path transitions the node from `parked` to `stale` (the standard re-eligibility transition). The dispatcher claims the row by its existing run-id and loads the persisted attribute bag — initialized at the original dispatch — re-invoking the executor with that bag verbatim. No substitution rebuild happens at any dispatch in the seven-state model; every row's bag is loaded from its own attribute record (per `concept:attribute`). The "executor's view of its upstreams is fixed for the lifetime of the node-run" rule is general, not a special case for parked-resume.

(The in-graph subscription-match exit path is no longer a direct transition on the parked row at all. Under `concept:cascade`'s in-flight-sealed invariant, a cascade walk targeting a parked node-run creates a NEW cascade-driven pending row; the parked row continues its own lifecycle independently. When the parked row eventually exits via time-wake or watchdog, the cascade-driven pending row dispatches in turn, in dispatcher serialization order.)

## Purpose

Some workloads (human review, scheduled wake, external event wait) cannot finish in a bounded window. `parked` gives them a first-class hold state with explicit resume semantics, instead of forcing them through `failed`+retry (which loses session context) or keeping a gRPC stream open indefinitely.

## Boundaries

Owns: the hold-state schema (the park fields on the node-run row), the three exit paths (time-wake, external invalidate, watchdog timeout). Does NOT own: held-claim resolution (that's `auto-terminal`); orphan reaping (parked rows are explicitly skipped). Adjacent: `node-run`, `auto-terminal`, `claim-handle` (including its held variant).

## Invariants

- Parked nodes emit park-family terminal signals (see `concept:signal`); subscribers decide whether to react (propagation is determined by subscriber matches against the emitted signal, not by sender color).
- The orphan-claim reaper skips parked rows because parked rows are settled with respect to liveness (no quiet-period or dispatch-channel connection state to observe).
- Time-wake transitions the parked row directly to `stale`; the next dispatcher claim re-invokes the executor with the persisted dispatch-time attribute bag (loaded by run-id). Cascade-driven re-invocation does NOT mutate the parked row — per `concept:cascade`, the cascade walker creates a new cascade-driven pending for the receiver and the parked row is left untouched. Watchdog timeout is the one destructive exit (parked to failed, with a park-timeout error class). None of the resume paths transition directly to running.
- The persisted attribute bag is the executor's input for every invocation of the node-run, including the time-wake resume. Upstream activity during the park does not rewrite the parked row's bag.
- Held-claim auto-terminal continues to fire correctly across park because the claim-holder's state stays active while the node is parked.

## Common pitfalls

- **Indefinite human-review park inside an in-flight frame.** A common pattern is "produce a tentative output, then park indefinitely (no resume-at) waiting for an operator to invalidate." Authoring this with the await-callback reason discriminator and a human-review reason-label is supported and correct — but parking a frame on review serializes parallel work in the same frame and creates long-lived held frames. The recommended idiom is **post-frame review**: the producing frame runs to completion; review happens externally; a follow-on graph or instance kicks off the post-review work. Frame-blocking review should be reserved for cases where downstream genuinely cannot proceed safely without approval (e.g. cross-system commit where the alternative is to reverse-cascade after the fact).
- Treating an operator-supplied reason string as a first-class park-reason. The park-reason surface is the two enum values (snooze and await-callback) plus a freeform reason-label payload field. Operator-supplied reasons like a human-review label belong in the freeform reason-label slot under the await-callback enum value; they are not first-class enum values.
