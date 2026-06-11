---
decision: depguard-pgx-isolation
status: as-is
---

# Confine `pgx` imports

## Choice

Only the foundation module's postgres persistence driver, the services module, the cmd group, the test-support package, and the scenario harness in the test group.

## Rationale

Keep postgres specifics out of the graph, runtime, and control layers.
