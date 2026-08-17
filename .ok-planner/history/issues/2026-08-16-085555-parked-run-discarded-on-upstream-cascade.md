---
issue: parked-run-discarded-on-upstream-cascade
kind: audit
category: conflicting
artifacts:
  - story:resume-preserves-snapshot
  - story:cascade-defers-during-flight
  - concept:parked-state
status: promoted
opened: 2026-08-16T08:55:55Z
sprint: 2026-08-17-accepted-intake-drain.md
---

# A parked run is discarded, not resumed, when its upstream re-runs during the park

A node-run can park (wait for an upstream to be ready again) and later resume against the inputs it was dispatched with — two stories and the parked-state concept all promise that, and that an upstream cascade during a park wakes the parked row early rather than rewriting it. In the code, the cascade wake transitions the parked row straight to stale with no marker, then queues a new round; under the default most-recent cascade mode, when that new round passes its gate the mode's coalescing delete removes prior unclaimed cascade-driven stale rows — which the woken parked row now is. The park is silently lost, the new round runs with re-substituted inputs, and the parked unit of work never executes. The three artifacts agree with each other; only the code disagrees. The ruling exempts woken rows from the coalescing delete.

## Options

- Exclude rows that reached stale by a park-wake from the most-recent mode's coalescing delete, so the woken row dispatches first and the queued round follows, as the concept describes; cost: a small predicate change and a scenario test.
- Rewrite three agreeing artifacts to describe the destructive behaviour; cost: overturns a settled design on the strength of a bug.

The ruling repairs the code to the design.

## Ruling

> Generated ruling (/verify-issues): Make the park-wake path visible to the coalescing delete — a row woken from parked is not a "prior cascade stale" and is never deleted by most-recent mode's sweep; it dispatches first with its dispatch-time inputs, and the newly queued round dispatches after it settles. Forced by the parked-state concept and the two stories, which already agree on this order; the code has an interaction bug between two individually correct paths. Verified against the tree as it stands; nothing was applied.

<!-- Owner: this is a generated ruling, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
