---
decision: testcontainers-go
status: as-is
---

# Integration-test container management

## Choice

Integration tests boot real container instances of the persistence backends from inside the Go test process, via testcontainers-go.

## Rationale

Real database containers in tests, not mocks — the persistence layer is exercised against the engines it ships with, and the test process itself owns container lifecycle and isolation.

## Alternatives

- Mocked or faked persistence — rejected: the storage tests' subject is the real engines' behavior.
- Externally provisioned databases (compose file, CI service) — rejected: the test process no longer owns lifecycle or isolation, and local runs need out-of-band setup.
- An in-memory engine standing in for the server-backed one — rejected: divergent SQL semantics make the tests prove the wrong engine.
