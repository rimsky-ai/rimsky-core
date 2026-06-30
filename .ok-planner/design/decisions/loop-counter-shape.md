---
decision: loop-counter-shape
status: as-is
aliases: []
---

# Loop-counter attribute schema and behavior

## Choice

The in-process handler for the loop-counter utility kind has:

- A required integer-typed maximum-count input attribute with no default, validated as strictly positive at registration via the attribute schema.
- An executor-written carry-forward integer-typed count attribute, defaulting to zero, marked read-only on the schema.
- Two declared tags: a step-iteration tag and a done tag.
- On every dispatch: read the count attribute from incoming attributes (carry-forward yields the prior value, or zero on the first dispatch in scope); increment the count; include the step-iteration tag on the Success outcome if the new count is below the maximum, otherwise include the done tag; the Success outcome carries an `attributes_delta` that persists the new count for next-dispatch carry-forward.

## Rationale

Minimum surface for "count up to N, emit on each step, include a different tag on the terminal step." Both tags are observable from downstream subscriptions via `terminal/success` with a CEL `when:` filter on `payload.tags`; the count attribute is visible to other nodes (and to the loop-counter itself across dispatches via carry-forward). Scope-bounded carry-forward is intra-frame (per `decision:run-scope-is-per-frame`): the count carries across dispatches within one frame's RunScope (the standard cascade-self-edge case) and resets on every new frame, every sub-graph invocation, and every fan-out partition. Cross-frame counting (a sequence of operator-triggered or message-driven iterations) is not within this utility's surface — orchestrators that need it ferry the count through a message body via the standard message-borne cross-frame coupling path (see `concept:message`).
