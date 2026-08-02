---
decision: parallel-cap-removal
---

# Test parallelism is uncapped except where a named contention bounds it

## Choice

No module carries a test-parallelism cap as scheduling insurance: tests synchronize on observable state (the services tests wait on observable subscription state, see `decision:subscription-mounting-state`), never on throttled parallelism. The one admitted cap bounds a real shared resource — the docker-stack e2e suites (services, examples) carry a docker-daemon-saturation cap on concurrent stack boots — and any such cap must be accompanied by a comment naming the contention it guards.

## Rationale

A parallelism cap that stands in for synchronization masks an ordering assumption and makes the verdict load-dependent; a cap that bounds a genuinely finite shared resource, and says so, is a harness fact rather than a hidden race.

## Alternatives

- A blanket test-parallelism cap across the modules — rejected: throttling substitutes for synchronization, hiding ordering assumptions until a differently-loaded machine exposes them.
- No caps at all, including the docker-stack suites — rejected: concurrent stack boots genuinely saturate the docker daemon, failing tests for a resource reason no synchronization can remove.
