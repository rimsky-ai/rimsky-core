---
audit: testcontainers-go
artifact: decision:testcontainers-go
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:36:46Z
---

# Persistence integration tests boot real containers via testcontainers-go

Supported. `github.com/testcontainers/testcontainers-go` (plus its `modules/postgres` submodule) is a direct dependency of all four modules that run persistence or stack integration tests (root, `lib/foundation`, `lib/services`, `examples`). The shared Postgres-container helper (`lib/foundation/pgpool`) boots a real Postgres container from inside the Go test process — via `testcontainers.WithCmdArgs`/`WithWaitStrategy` and the `postgres` module's container runner — and clones per-test databases from a migrated template; it is consumed transitively by every Postgres-backed test package through `test/support/testpg` and `test/support/pgdbtest`, used across roughly 40 packages spanning `lib/control/controlapi`, `lib/graph/frame`, `lib/runtime` (and its scheduler subpackage), and `test/scenarios`. The services module's own container-boot helper (`lib/services/test/harness/postgres.go`) and the docker-stack harness (`lib/services/test/harness/rimsky*.go`) likewise drive testcontainers-go directly rather than mocking persistence or requiring an externally provisioned database. No mocked-persistence or in-memory-engine test fixture stands in for the real backends anywhere in the packages checked.
