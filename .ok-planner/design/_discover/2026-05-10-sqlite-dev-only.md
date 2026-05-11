---
topic: sqlite-dev-only
kind: choice
---

# SQLite is the dev-only persistence driver; multi-host requires Postgres

## Description

Rimsky ships two `persistence.Driver` implementations: `foundation/persistence/postgres/` and `foundation/persistence/sqlite/`. The SQLite driver implements the same `Driver` / `Queue` / `AdvisoryLocker` / `Store` / `LockHoldersStore` / `ClaimHoldersStore` / `FrameStore` interfaces as Postgres, using `modernc.org/sqlite` (pure-Go, no CGO).

SQLite's cross-process advisory primitives degrade to in-process equivalents:

- `TrySchedulerTick` → `sync.Mutex.TryLock` (`foundation/persistence/sqlite/advisory_locker.go`).
- `AcquireMigrationLock` → `sync.Mutex.Lock`.
- `TakeNamedLockInTx` / `TakeScopeLockInTx` → no-ops (the SQLite writer is single-process; `BEGIN IMMEDIATE` serializes writers globally, subsuming the per-name/per-scope exclusion that Postgres needs).

The driver-level comment at `foundation/persistence/driver.go:46-50` is explicit: SQLite's writer is single-process; the cross-process advisory primitives the Postgres driver uses are replaced with in-process equivalents. That's sound for dev (a single process can hit a single SQLite file fine) but unsound for the three-process topology rimsky targets in deployment.

The expectation that SQLite is dev-only is encoded in two adjacent places:

1. **The memory blob backend reject gate** (`foundation/persistence/blob_config.go:115`) explicitly states "the per-process binaries... cannot share state through an in-process map." The same multi-process argument applies to SQLite's writer-only-on-one-host topology.
2. **The unified Docker image** defaults to `driver: sqlite` (`deploy/rimsky-all.yml`) but the multi-replica path with `replicas > 1` creates independent SQLite databases — broken by construction. CLAUDE.md "Non-obvious gotchas" calls this out: "The unified image (rimsky/all) defaults to driver: sqlite with state at /var/lib/rimsky/state.db. Running with replicas > 1 creates independent SQLite databases — broken. Use the per-process images plus the postgres driver for multi-replica deployments."

The SQLite driver makes scenario tests cheap (no testcontainers — the file lives in `t.TempDir()`) and makes the cross-compile-to-pure-Go path possible. Marking it explicitly as dev-only documents the constraint without removing the value.

The two driver impls share `persistence.Migrator` (`foundation/persistence/migrations.go`) so migrations don't fork. A future second prod-grade driver (CockroachDB, planetscale) plugs in at the same `Driver` interface. The sqlite driver subdir is the reference for "how to implement a Driver" — it shows the no-op cases for advisory locks plus the BEGIN IMMEDIATE writer serialization pattern.

## Code surface

- `foundation/persistence/driver.go:46-50` — driver-level comment.
- `foundation/persistence/sqlite/` — entire subdir.
- `foundation/persistence/sqlite/advisory_locker.go` — in-process degraded primitives.
- `foundation/persistence/blob_config.go:115` — analogous reject gate (memory backend).
- `deploy/rimsky-all.yml` — unified image with `driver: sqlite` default.
- `foundation/persistence/migrations.go` — shared migration runner.

## Prose surface

- `CLAUDE.md` "Non-obvious gotchas" — three lines explicitly call this out.
- `CLAUDE.md` "Reference deployment & local stack" — unified image story.
- `docs/humans/landing.md` — first-run experience uses SQLite.

## Adjacent topics

- `2026-05-10-postgres-only-runtime-state` — three-process topology that breaks SQLite.
- `2026-05-10-blob-spill-pluggable-backends` — memory backend's analogous gate.
- `2026-05-10-advisory-locks-tick-and-migrate` — primitive that SQLite no-ops.

## Observations

- A deployment that picks SQLite + replicas > 1 silently splits state — **no startup gate rejects this configuration today**, parallel to the (enforced) memory-blob-backend rejection at `foundation/persistence/blob_config.go:115`. CLAUDE.md notes this as an open question.
- The SQLite advisory_locker.go file documents the no-op rationale inline. A reader looking only at the file would conclude "advisory locks degrade harmlessly" — true for single-process; false for multi-process. The driver.go comment is the cross-reference.
- `modernc.org/sqlite` is pure-Go and is what makes the cross-compile-to-Docker-images path work without CGO. Switching to a CGO-backed SQLite would compromise the cross-compile story.
- The unified-image's SQLite-default means a new operator who runs `docker run rimsky/all` gets a working stack without a Postgres dependency — useful for first-run, but the gotcha at scale is the silent multi-replica split.
