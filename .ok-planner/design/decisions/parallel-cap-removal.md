---
decision: parallel-cap-removal
status: as-is
---

# Test-parallelism caps lifted

## Choice

The test-all gate runs each module's tests without a test-parallelism cap; the services tests wait on observable subscription state rather than relying on throttled parallelism.

## Rationale

With `mounting` observable (see `decision:subscription-mounting-state`), the services tests have no synchronous publisher-Subscribe budget to throttle around, and an uncommented cap is decay risk.
