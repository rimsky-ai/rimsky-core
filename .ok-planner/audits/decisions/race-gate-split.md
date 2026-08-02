---
audit: race-gate-split
artifact: decision:race-gate-split
determination: supported
commit: b767a27d
audited: 2026-08-02T09:36:46Z
---

# Everyday single-iteration race slice vs. release-gate repeated race detection

Supported. `Makefile`'s `test-root` and `test-foundation` targets (composed into `test-all`, the everyday gate) each carry a thin `-race -count=1` slice — `./lib/runtime/... ./lib/graph/scheduler/...` and `./persistence/postgres/... ./persistence/sqlite/...` respectively — covering the runtime layer, the scheduler, and the persistence drivers on every run. The dedicated `test-race` target repeats only the runtime-and-scheduler slice at `-race -count=3`, explicitly omitting persistence per its own comment ("their race surface is mostly contention against the underlying driver, not Go data races"), matching the decision's stated exclusion. The `release` target's chain (`lint core-images service-images test-all test-race scan push-images`) requires `test-race`, so the full repeated-race gate is mandatory before a release ships, while the everyday `test-all` gate never pays the `-count=3` cost.
