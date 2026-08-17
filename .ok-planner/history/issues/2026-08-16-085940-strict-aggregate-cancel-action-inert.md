---
issue: strict-aggregate-cancel-action-inert
kind: audit
category: conflicting
artifacts:
  - concept:fan-out
status: promoted
opened: 2026-08-16T08:59:40Z
sprint: 2026-08-17-accepted-intake-drain.md
---

# Strict fan-out aggregation asks to cancel siblings, and nothing listens

A fan-out node clones itself over partitions and aggregates the results; the fan-out concept says the strict and first policies both force-cancel every remaining in-flight clone at the run level, and the cancel-siblings concept independently points at fan-out as the owner of that mechanism. In the code, strict's aggregate returns a cancel-siblings action; the one consumer of aggregate actions handles only first's cancel-non-winners and explicitly skips everything else. What remains under strict is a claim-handle-level force-abandon that leaves sibling run rows in flight — the dispatches run to natural conclusion. The only strict scenario asserts the returned action value, so the gap sits behind a passing test. The ruling wires the action.

## Options

- Extend the cancel-action executor to handle cancel-siblings by recursing into each remaining in-flight sibling, as it already does for first, and add a scenario asserting a sibling run row reaches failed; cost: none beyond reusing the working path.
- Amend the invariant; cost: also requires walking back the cancel-siblings concept, which treats the mechanism as real.

The ruling makes strict do what two concepts already say it does.

## Ruling

> Generated ruling (/verify-issues): Make the run-level cancellation happen under strict — the aggregate's cancel-siblings action force-fails every remaining in-flight clone through the same run-tree walk first's non-winners already use — and pin it with a scenario that watches a sibling's run row reach failed. Forced by two independently authored concepts that state the mechanism as design; the code has an unhandled action. Verified against the tree as it stands; nothing was applied.

<!-- Owner: this is a generated ruling, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
