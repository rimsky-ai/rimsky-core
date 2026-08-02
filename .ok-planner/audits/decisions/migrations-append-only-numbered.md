---
audit: migrations-append-only-numbered
artifact: decision:migrations-append-only-numbered
determination: supported
commit: b767a27d
audited: 2026-08-02T09:39:53Z
---

# Migrations are numerically ordered and append-only, maintained per backend

Supported. The shared `persistence.Migrator.Run` (`lib/foundation/persistence/migrations.go`, guarded at the check with `@decision: migrations-append-only-numbered`) lexicographically sorts embedded migration filenames and hard-fails any unapplied file that sorts before the highest already-applied filename, rather than silently reordering or skipping — exercised by `TestMigratorRun_OutOfOrderFileIsRejected` plus five other tests covering the runner's other behaviors. Checked both backends' migration directories as the population: 39 files under `lib/foundation/persistence/postgres/migrations` and 40 under `lib/foundation/persistence/sqlite/migrations` (one extra, `036-normalize-timestamp-column-dialect.sql`, sqlite-only), every filename numerically prefixed, monotonically increasing, and no filename reused between the two directories' shared numbers.
