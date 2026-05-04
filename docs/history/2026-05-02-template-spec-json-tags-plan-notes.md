# Implementation notes — 2026-05-02 template-spec JSON tags

## Task 6 — Run controlapi tests

**Deviation:** none — flagging for visibility
**Reason:** First run of `go test ./core/controlapi/... -count=1` failed once with `pgtest: connection string: port "5432/tcp" not found` on `TestTemplateDeploy_UnknownStore_400`. Re-running the test in isolation succeeded; re-running the full suite succeeded. Likely a transient testcontainers / Docker race during parallel container startup, not a regression introduced by this change.
**Surfaced for:** awareness only — the suite passed on the second attempt.

## Task 24 — Final cross-package smoke

**Deviation:** none — flagging for visibility
**Reason:** Same testcontainers flake (`pgtest: port "5432/tcp" not found`) appeared again, this time on `TestTemplateLifecycle_DeployGetListDelete` during the full `go test ./...` sweep. Re-running `go test ./core/controlapi/... -count=1` in isolation passed cleanly. Pattern is consistent with parallel-container-startup contention on the local Docker daemon, not a regression introduced by this change. Earlier full-sweep run during Task 20 had completed without any failures, and the storage/queue/scheduler/supervisor suites (Task 17) all passed prior to this.
**Surfaced for:** awareness — the underlying suites pass; this is a Docker/testcontainers infra flake, not test-code regression. If it recurs, consider running `core/controlapi` with `-p 1` or a longer wait_strategy on the Postgres container.
