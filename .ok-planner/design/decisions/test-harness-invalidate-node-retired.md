---
decision: test-harness-invalidate-node-retired
status: as-is
aliases: []
---

# Test-harness invalidate-node retired

## Choice

The scenario test harness's invalidate-node helper retires entirely (both branches). Two tests whose subject was the retired operator-invalidate surface retire alongside. The remaining scaffolding tests are reinstrumented to drive their second-firing setup through legitimate triggers — typed messages for non-parked targets, real executor and callback for parked-state targets. Story-proof tests reinstrument the same way; their stories' `Acceptance` and `Proof` fields are unchanged because the user-observable outcome is unchanged.

## Rationale

The harness's invalidate-node was itself the principle violation, just relocated to the test layer — a test-only ad-hoc-node-invalidation surface that lets test code do precisely what the principle rules out for user surfaces. Keeping the helper via a debug-channel migration was structurally incoherent (the debug-override surface requires a running frame, which a quiescent instance does not have; pausing first blocks the frame engine from creating one). Keeping a test-only state-injection helper preserved an internal mechanism that production no longer supports, creating maintenance debt that nobody owns. Reinstrumenting tests with the legitimate trigger keeps the test-to-production path identical end-to-end.

## Alternatives considered

Debug-channel migration of the helper — rejected: the preconditions for debug-override admission and for having a running frame to act on exclude each other on a quiescent instance. Test-only state-injection helper — rejected: preserves the ad-hoc-invalidation mechanism in test code, creating an internal path nobody owns in production. Per-test retain-and-migrate (reshape the helper, keep tests unchanged) — rejected: the helper itself is the principle violation, so reshaping it cannot make the tests calling it principle-aligned.
