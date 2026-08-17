---
concept: parked-state
aliases:
  - park
  - parked node
---

# Parked state

## What it is

Parked is one of the seven node-run states (see `concept:node-run`), entered from `running` when the executor emits a park outcome. While parked, the node is not running and not failed; it carries a required resume-at timestamp, executor-opaque scratch bytes, and tags — the single annotation channel for operator- or executor-supplied context. `parked` is in the in-flight set (`{pending, stale, running, held, parked}`). A cascade event from a subscribed upstream reaching a parked receiver both wakes the parked row (parked → stale in the walk's transaction, through the same single wake path the time-wake sweep uses) and queues the cascade round as a NEW cascade-driven pending row per `concept:cascade`; the parked row's bag and scratch are never mutated by the walk.

### Park-flavored signals

Park emits a canonical signal per `concept:signal`, under the audit-only `transient/park` family. Park is dispatch-internal (the dispatch ends but the run continues), so park signals never fire cascade and never carry `attributes_delta`; template registration rejects any subscription whose `type:` targets the `transient/park` family, so a park outcome never propagates to downstream subscribers directly. Tags on the Park outcome ride the signal payload as the single annotation channel, not a separate column-form distinction at the subscriber boundary. The park audit row also records the scratch payload's size in bytes and whether it was blob-spilled — never the scratch bytes themselves, keeping the audit ledger forensically useful without inflating it with executor-opaque content. The await-async-callback outcome is NOT a park (the node stays running during the callback wait); it emits a distinct transient await-async signal and is covered under `concept:signal`'s transient subtree.

### Resume context

Parked nodes carry no dedicated resume context. On time-wake re-dispatch, the executor receives the same persisted attribute bag it saw at the original dispatch (loaded by run-id from the run's attribute store per `concept:attribute`; see "Time-wake resume preserves the dispatch-time snapshot" below). Executors that need to thread executor-managed state across a park-and-resume cycle use scratch: the parker writes opaque bytes to the Park outcome's scratch field, which the supervisor persists on the parked row's scratch slot. The same row re-dispatches at time-wake (no row-copy step — the parked row IS the resume row), so the resumed executor reads its scratch off the same dispatch's scratch slot via its ExecuteRequest. Attribute writeback is not available on Park because Park is dispatch-internal and writes nothing to the per-run attribute row (per `decision:uniform-attributes-delta`). No per-row payload or session token is threaded through to the re-dispatch.

A cascade event from a subscribed upstream targeting a parked receiver wakes the parked row early (parked → stale, resume-at cleared) and creates a fresh cascade-driven pending row carrying the round. The pending row dispatches in dispatcher serialization order after the woken row settles, so cascade information arriving during a park survives and produces a later dispatch.

### Time-wake resume preserves the dispatch-time snapshot

Both wake paths — time-wake at resume-at and cascade-wake from a subscribed upstream — transition the node from `parked` to `stale` (the standard re-eligibility transition) through one shared wake path that clears resume-at and appends the wake event with its reason. The dispatcher claims the row by its existing run-id and loads the persisted attribute bag — initialized at the original dispatch — re-invoking the executor with that bag verbatim. No substitution rebuild happens at any dispatch in the seven-state model; every row's bag is loaded from its own attribute record (per `concept:attribute`). The "executor's view of its upstreams is fixed for the lifetime of the node-run" rule is general, not a special case for parked-resume.

(Under `concept:cascade`'s in-flight-sealed invariant, the walk never rewrites the parked row's bag or scratch; the wake is a state-only transition. The cascade round rides the NEW cascade-driven pending row, which dispatches in dispatcher serialization order after the woken row settles.)

## Purpose

Some workloads (human review, scheduled wake, external event wait) cannot finish in a bounded window. `parked` gives them a first-class hold state with explicit resume semantics, instead of forcing them through `failed`+retry (which loses session context) or keeping a gRPC stream open indefinitely.

## Boundaries

Owns: the hold-state schema (the park fields on the node-run row), the exit paths (time-wake and cascade-wake). Does NOT own: held-claim resolution (that's `auto-terminal`); orphan reaping (parked rows are explicitly skipped). Adjacent: `node-run`, `auto-terminal`, `claim-handle` (including its held variant), `cascade`, `signal`, `attribute`.

## Invariants

- Parked nodes emit park-family signals under the audit-only `transient/park` family (see `concept:signal`); template registration rejects any subscription targeting that family, so a park outcome never propagates to downstream subscribers directly — only the parked run's eventual terminal settlement does.
- The park audit signal's payload carries the scratch payload's byte size and a spilled-to-blob flag, never the scratch bytes; a zero-length scratch payload records size zero and spilled false.
- The orphan-claim reaper skips parked rows because parked rows are settled with respect to liveness (no quiet-period or dispatch-channel connection state to observe).
- Both wake paths transition the parked row directly to `stale`; the next dispatcher claim re-invokes the executor with the persisted dispatch-time attribute bag (loaded by run-id). The cascade wake fires only for a subscribed upstream's settling cascade and mutates state only — the walker never rewrites the parked row's bag or scratch, and the cascade round lands on a new pending row per `concept:cascade`. The run-tree cancellations that force-fail any non-terminal run force-fail a parked row too; they are cross-cutting terminations, not park-specific machinery. `concept:node-run` owns the state machine and enumerates the reasons. None of the resume paths transition directly to `running`.
- The persisted attribute bag is the executor's input for every invocation of the node-run, including both resume paths. Upstream activity during the park does not rewrite the parked row's bag.
- Held-claim auto-terminal continues to fire correctly across park because the claim-holder's state stays active while the node is parked.

## Common pitfalls

- **Long-running human-review park inside an in-flight frame.** A common pattern is "produce a tentative output, then park with a periodic re-check waiting for an operator decision." Every park requires a resume-at, so an indefinite no-deadline wait is not available; a review park re-checks periodically rather than waiting forever. Parking a frame on review still serializes parallel work in the same frame and creates long-lived held frames regardless of how the resume-at is chosen. The recommended idiom is **post-frame review**: the producing frame runs to completion; review happens externally; a follow-on graph or instance kicks off the post-review work. Frame-blocking review should be reserved for cases where downstream genuinely cannot proceed safely without approval (e.g. cross-system commit where the alternative is to reverse-cascade after the fact).
