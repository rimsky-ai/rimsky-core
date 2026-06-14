---
decision: parallel-cap-removal
status: as-is
---

# Test-all runs without a test-parallelism cap

## Choice

The test-all gate runs each module's tests without a test-parallelism cap; the services tests wait on observable subscription state rather than relying on throttled parallelism.

## Rationale

With `mounting` observable (see `decision:subscription-mounting-state`), the services tests have no synchronous publisher-Subscribe budget to throttle around. A test-parallelism cap is admitted only when accompanied by a comment naming the contention it guards.
