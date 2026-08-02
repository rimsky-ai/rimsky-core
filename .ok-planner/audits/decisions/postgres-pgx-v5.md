---
audit: postgres-pgx-v5
artifact: decision:postgres-pgx-v5
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:39:53Z
---

# Postgres access uses jackc/pgx/v5 through its native interface

Supported. Both `go.mod` and `lib/foundation/go.mod` pin `github.com/jackc/pgx/v5 v5.9.2`. The postgres persistence package (`lib/foundation/persistence/postgres`) is built on `pgxpool.Pool` directly rather than through `database/sql`/`pgx/v5/stdlib` — checked every `.go` file under the package importing `pgx/v5`; all import the native `pgx`/`pgxpool`/`pgconn` packages, none import `pgx/v5/stdlib`. Structured Postgres error detail via `pgconn.PgError` is used at four call sites (`api_keys.go`, two in `instances.go`, `blob_largeobject.go`). `github.com/lib/pq` appears only as an indirect entry in `go.sum` (a transitive dependency, e.g. of testcontainers) with zero direct imports anywhere in the tree, and `.golangci.yml`'s `pgx-isolation` depguard rule confines `jackc/pgx/v5` imports to the postgres persistence package and a fixed list of test/tooling directories.
