---
decision: frame-isolation
status: as-is
aliases: []
---

# Frames run in perfect isolation; no state crosses a frame boundary

## Choice

A frame is a self-contained unit of cascade resolution. No node-run, RunScope, attribute bag, or message ever crosses a frame boundary. A message can only ever land in the frame it itself triggered; nothing in an already-running frame is observable to or mutable by a different frame. Cross-frame coupling, when needed, is expressed by a message-emitter node whose dispatch lands a message in the ledger, with the next frame opening on the standard delivery path per `decision:single-frame-creation-path`.

## Rationale

Cascade resolution per `concept:cascade` is a graph-traversal operation that holds during one frame; modeling it as a flowing state stream that spans multiple frames introduces a class of races the design cannot reason about cleanly. Frame isolation makes the cascade walker's "within a frame" invariant a hard structural property rather than a convention: every quantity the walker reads (the wait-set, in-flight states, the run-scope tree) is bounded by frame identity, and no concurrent frame's mid-flight state can pollute the read.

Frame isolation also makes the per-instance serial-frame execution rule of `concept:frame` complete: with one running frame at a time and no state crossing the boundary, the runtime's behavior for any frame is fully determined by that frame's triggering message and the persisted state at frame start. The next frame starts with a clean slate; whatever the prior frame committed lives in the database as the frame's terminal effect, not as transient state the next frame inherits.

The session-resume carry-forward defect that surfaced in mid-2026 traced directly to the absence of this property: under the retired "frames and RunScopes are orthogonal" framing (see `decision:run-scope-is-per-frame`), carry-forward bounded by RunScope leaked across frames whenever a RunScope outlived a frame, producing nondeterministic state on the next frame's dispatch. Promoting frame isolation to a hard invariant eliminates that defect class structurally rather than via per-call frame filters.

## Alternatives

Shared-state-across-frames (the retired model) — rejected. Earlier rimsky variants kept RunScopes long-lived (one main RunScope per instance) and let cascade-relevant state — carry-forward, sequence numbering, the wait-set tail — survive across frames. Three consequences ruled this out: (1) every persistence query for "state in this scope" had to be re-qualified with a frame filter to avoid cross-frame leakage, multiplying the surface area for missing-filter bugs; (2) the cascade walker's in-flight-sealed invariant of `concept:cascade` could be observably violated when one frame's mid-flight run was visible to another frame's gate evaluator; (3) the meaning of "the latest run for this node in this scope" became ambiguous across frames and required clock-based tiebreakers. Replacing shared state with frame isolation closes all three at the structural layer.

Implicit isolation by convention (no formal property, just discipline) — rejected. Discipline across many code paths and several agent generations does not hold; the retired model was implicit-isolation-by-convention in practice, and the convention was repeatedly violated by the cascade walker, the message delivery path, and the carry-forward query. Making isolation a hard invariant is the only durable form.
