---
decision: node-state-retired-from-operator-api
---

# Operator surfaces do not expose a synthesized node-level state field

## Choice

The operator-facing node response does not carry a single `state` field. The node row owns no lifecycle state; per-run lifecycle lives on `concept:node-run`. Operators read run lifecycle through one of two surfaces: the categorical per-state run summary (counts per state, attached to the node response), or the per-run endpoint for a specific run's state.

## Rationale

A synthesized "node state" derived from a latest-run probe silently picks one run when multiple runs exist for the same node — fan-out partitions, multi-scope nodes, recently-invalidated nodes whose prior run has not yet terminated. The picked value is presented as authoritative; operators act on it as if it were the node's state. When the picked run does not match the operator's mental model — a fresh new run still in flight while a prior failed run sits at the top of the latest-run order, for example — the operator makes incorrect decisions.

With no synthesized field, operators consume run lifecycle through surfaces that are unambiguous by construction. The categorical summary answers "what is the distribution of run states for this node" — the question operators almost always actually have. The per-run endpoint answers "what state is run X in" — the question operators ask when they have a specific run in mind. Neither surface conceals run-level detail behind a synthesized aggregate, and neither requires the API to make a "which run is the canonical one" choice the API cannot make correctly.

The categorical summary is informationally richer than a single synthesized state in every case: an operator who wants "is this node done" can look at whether any in-flight states are nonzero; an operator who wants "has this node ever failed" can look at the failed count; an operator who wants "the most recent state" looks up the most recent run by id and reads its state directly.

## Alternatives

Keep the field, documented as "the latest run's state" — rejected. The ambiguity is in "latest" — without per-run scope and frame in the response, operators cannot reason about which run the value describes, and the value is wrong for fan-out and multi-scope nodes in ways operators cannot detect.

Synthesize a coarser `is_idle` / `is_in_flight` boolean instead — rejected. The categorical summary supplies the same information at the per-state granularity an operator needs; collapsing it to a boolean removes information without reducing the dual-surface problem.

Move the latest-run lookup behind a query parameter (opt-in, with a warning) — rejected. The presence of the field on the response invites consumption; the underlying ambiguity is unfixable in the API shape. A field that is sometimes correct is a worse contract than no field.
