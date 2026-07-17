---
decision: parallel-cap-removal
status: as-is
---

# Test-all runs without a test-parallelism cap

## Choice

The services and examples test-all targets carry a docker-daemon-saturation cap (bounding concurrent stack boots); the services tests wait on observable subscription state rather than relying on throttled parallelism as a substitute for that. No module carries a test-parallelism cap for the retired synchronous publisher-Subscribe budget.

## Rationale

With `mounting` observable (see `decision:subscription-mounting-state`), the services tests have no synchronous publisher-Subscribe budget to throttle around, so that cap is gone. A test-parallelism cap is admitted only when accompanied by a comment naming the contention it guards — the docker-daemon-saturation cap on the docker-stack e2e suites (services, examples) is such a case and remains.
