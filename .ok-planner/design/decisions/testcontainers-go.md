---
decision: testcontainers-go
status: as-is
---

# Integration-test container management

## Choice

An integration-test container helper that boots real container instances of the persistence backends from inside the Go test process.

## Rationale

Real database containers in tests, not mocks.
