---
audit: claim-producer-postgres
artifact: story:claim-producer-postgres
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:35:26Z
---

# Operator acquires row-locking, staged-async, verifier-backed claims via the postgres claim producer

Supported. `postgres/store/pick_policy.go` implements configurable pick policies (`ItemsTable`, `OnCommit`, `OnGiveUp`, `VisibilityTimeout`) that acquire via `SELECT ... FOR UPDATE SKIP LOCKED`, real row-locking rather than an in-memory queue; `postgres/store/staging.go` implements opt-in atomic staging as a real Postgres schema swap at commit (`CREATE SCHEMA` reservation on open, `DROP`+`RENAME SCHEMA` under an advisory lock on commit), exercised by `atomic_staging_test.go`; the shared `sqlchecks` package (`lib/services/claim_producers/shared/sqlchecks/compile.go`) compiles a `row_count_ratio` verifier check to a `SELECT count(*) FROM ...` query and enforces (via a `SELECT`-only regex) that every check kind, including `row_count_ratio`, only ever emits aggregate SQL; and the postgres server declares and the postgres executor emits exactly the claimed error-class family — `pg/claim_unavailable`, `pg/swap_failed` (store capabilities, `postgres/server/server.go`) and a `pg/verifier_check_failed/<kind>` class per failing check (`postgres/server/executor.go`), each exercised by `executor_test.go` (`TestExecutor_RowCountRatio`) and `atomic_staging_test.go`. Checked all four claimed surfaces (pick policy, atomic staging, row-count-ratio verifier, declared error classes) against their store/server code and unit tests.
