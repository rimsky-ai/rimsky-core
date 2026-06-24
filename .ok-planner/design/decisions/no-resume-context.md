---
decision: no-resume-context
status: as-is
aliases: []
---

# Resume context channel removed

## Choice

Remove `Park.payload`, `Park.session_token`. Remove the `ExecuteRequest.resume_context` field and the `ResumeContext` message. Executors that need to thread executor-managed state across a park-and-resume use scratch: the parker writes opaque bytes to the Park outcome's scratch field, which the supervisor persists on the parked row's scratch slot. The same row re-dispatches at time-wake (no row-copy step — the parked row IS the resume row), so the resumed executor reads its scratch from `ExecuteRequest.scratch` on the same dispatch. Attribute writeback is not available on Park — Park is dispatch-internal and writes no attributes (per `decision:uniform-attributes-delta`).

## Rationale

A dedicated resume-context channel duplicated what scratch (the per-dispatch executor-attached opaque bytes channel) already provides. Two parallel mechanisms for "executor-managed state that crosses the park boundary" violate the "one idiom per job" principle of the project's coding style. Scratch is the right channel because it is purpose-built for opaque executor state, it survives the park-resume cycle via the supervisor's scratch carry-forward on re-enqueue, and it does not conflate executor-managed transient state with the per-run attribute row's typed schema.

## Alternatives

Keep resume context as the primary channel for Park-specific state — rejected because it is redundant with scratch and adds a Park-only wire field for a job scratch already covers uniformly across success / error / park.

Use `attributes_delta` to thread state across park — rejected because Park does not carry `attributes_delta` (per `decision:uniform-attributes-delta`); attribute writeback is a feature of run-terminating verdicts, not of the dispatch-internal park transition.
