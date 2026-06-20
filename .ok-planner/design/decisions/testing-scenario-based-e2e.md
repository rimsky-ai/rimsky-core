---
decision: testing-scenario-based-e2e
status: as-is
---

# Testing discipline

## Choice

End-to-end via the test group's scenarios directory + the services module's scenarios test directory driving the assembled product; persistence tests use an integration-test container helper to boot real backends.

## Rationale

Real-stack integration tests against the load-bearing safety properties documented in the concept catalog.
