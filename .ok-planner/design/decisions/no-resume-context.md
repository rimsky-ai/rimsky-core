---
decision: no-resume-context
status: as-is
aliases: []
---

# No resume-context channel

## Choice

The executor wire carries no dedicated resume-context channel — no park payload, no session token, no resume-context field on the dispatch request. Executor-managed state that crosses a park-and-resume rides scratch: the parker writes opaque bytes to the Park outcome's scratch field, the supervisor persists them on the parked row's scratch slot, and the same row re-dispatches at time-wake (no row-copy step — the parked row IS the resume row), so the resumed executor reads them back from the dispatch's scratch field. Attribute writeback is not available on Park — Park is dispatch-internal and writes no attributes (per `decision:uniform-attributes-delta`).

## Rationale

A dedicated resume-context channel duplicated what scratch (the per-dispatch executor-attached opaque bytes channel) already provides. Two parallel mechanisms for "executor-managed state that crosses the park boundary" violate the "one idiom per job" principle of the project's coding style. Scratch is the right channel because it is purpose-built for opaque executor state, it survives the park-resume cycle via the supervisor's scratch carry-forward on re-enqueue, and it does not conflate executor-managed transient state with the per-run attribute row's typed schema.

## Alternatives

Keep resume context as the primary channel for Park-specific state — rejected because it is redundant with scratch and adds a Park-only wire field for a job scratch already covers uniformly across success / error / park.

Use `attributes_delta` to thread state across park — rejected because Park does not carry `attributes_delta` (per `decision:uniform-attributes-delta`); attribute writeback is a feature of run-terminating verdicts, not of the dispatch-internal park transition.
