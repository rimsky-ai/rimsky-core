---
audit: sqlite-multiproc-safety
artifact: decision:sqlite-multiproc-safety
determination: supported
commit: b767a27d
audited: 2026-08-02T09:39:53Z
---

# The SQLite driver is safe for processes sharing one local database file

Supported, on both claimed halves. (1) `lib/foundation/persistence/sqlite/database.go` opens the database with `_txlock=immediate` in the DSN, and the package's single transaction-creation path (`tablesImpl`'s `db.BeginTx` in `backend.go`, reached via `persistence.Tables.Transaction`) is the only `BeginTx` call site outside migration bootstrap — so every `persistence.Tx` a caller opens is an OS-level immediate-mode SQLite transaction; the per-name/per-scope in-tx lock methods (`TakeNamedLock`, `TakeClaimScopeLock`, `TakeLifecycleScopeLock`) are deliberate no-ops that rely on exactly this. (2) `advisoryLockerImpl` (`advisory_locker.go`, tagged `@decision: sqlite-multiproc-safety`) backs `TrySchedulerTick` and `AcquireMigrationLock` with OS `flock()` on lock files beside the database path (`advisory_flock_unix.go`/`advisory_flock_windows.go`); two dedicated tests (`TestTrySchedulerTick_ExcludesAcrossLockerInstances`, `TestAcquireMigrationLock_BlocksAcrossLockerInstances`) exercise cross-instance exclusion on the same file path, and `TrySchedulerTick` is called by the scheduler's real tick loop (`lib/runtime/scheduler/scheduler.go`). Both the sqlite and postgres advisory lockers are further exercised by one shared conformance suite (`lib/foundation/persistence/conformance/advisory_locker.go`).
