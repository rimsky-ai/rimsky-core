---
decision: test-harness-invalidate-node-retired
status: as-is
aliases: []
---

# Test harness has no invalidate-node helper

## Choice

The scenario test harness carries no invalidate-node helper and no other test-only ad-hoc node-invalidation surface. Tests set up second firings through legitimate triggers only — typed messages for non-parked targets, a real executor and callback for parked-state targets — so the test-to-production path is identical end-to-end.

## Rationale

A harness invalidate-node is the ad-hoc-invalidation principle violation relocated to the test layer — a test-only surface letting test code do precisely what the principle rules out for user surfaces. A debug-channel form is structurally incoherent: the debug-override surface requires a running frame, which a quiescent instance does not have, and pausing first blocks the frame engine from creating one. Any test-only state-injection helper preserves an internal mechanism production no longer supports, creating maintenance debt nobody owns.

## Alternatives

- Debug-channel migration of the helper — rejected: the preconditions for debug-override admission and for having a running frame to act on exclude each other on a quiescent instance.
- A test-only state-injection helper — rejected: preserves the ad-hoc-invalidation mechanism in test code, an internal path nobody owns in production.
- Reshape the helper and keep the calling tests unchanged — rejected: the helper itself is the principle violation, so reshaping it cannot make its callers principle-aligned.
