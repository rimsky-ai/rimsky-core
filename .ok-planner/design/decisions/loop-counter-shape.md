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
- Two declared named events: a step-iteration event and a done event.
- On every dispatch: read the count attribute from incoming attributes (carry-forward yields the prior value, or zero on the first dispatch in scope); increment the count; emit the step-iteration event if the new count is below the maximum, otherwise emit the done event; close the stream with the success outcome carrying an attribute delta that persists the new count for next-dispatch carry-forward.

## Rationale

Minimum surface for "count up to N, emit on each step, emit a different event on the terminal step." Both events are observable from downstream subscriptions; the count attribute is visible to other nodes (and to the loop-counter itself across dispatches via carry-forward). Scope-bounded carry-forward makes the new-RunScope-resets-count behavior fall out naturally — no separate reset mechanism needed.
