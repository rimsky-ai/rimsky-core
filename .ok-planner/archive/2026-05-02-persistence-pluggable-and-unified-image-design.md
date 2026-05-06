# Pluggable Persistence + Unified Image — Design

## Status

- Spec, 2026-05-02.
- Outcome of the 2026-05-02 brainstorm covering the pluggable runtime persistence layer (currently Postgres-only) and a unified Docker image that lets `docker run rimsky` boot a complete dev stack.
- Foundational dependencies:
  - `docs/specs/2026-05-01-control-plane-and-store-lifecycle-design.md` §3.1 — the `RIMSKY_CONFIG` / `rimsky.yml` loader posture this spec extends.
  - `docs/specs/2026-04-27-stores-redesign-v3-design.md` and the `2026-04-30` cleanup overlay — the runtime contract the persistence layer must preserve.
  - `CLAUDE.md` — blessed invariants 1–20; the persistence layer is load-bearing for invariants 3, 4, 4b, 6, 7, 8, 10, 13, 14, 15.
- Sibling, not dependency: `docs/specs/2026-05-02-rimsky-cli-and-compose-design.md` — the CLI's `init` scaffold is unchanged by this spec. A follow-up spec may revise `init` to default to the unified image once this work lands.

## Context

Today rimsky's persistence is Postgres-only, and the coupling to pgx is far deeper than "core/queue/ + core/storage/ are the impl, everything above is interface-driven." A spec audit before writing this revision found:

- The `DispatchQueue` interface (`core/queue/interface.go:138`) exposes `pgx.Tx` directly in `SelectCandidates` and `ClaimDispatchRow`. The supervisor's runner owns a `pgx.Tx` and threads it through these helpers.
- The supervisor (~4,150 lines across `core/supervisor/runner.go`, `runner_acquire.go`, `runner_dispatch.go`, `runner_held_claims.go`, `runner_locks.go`, `runner_terminal.go`, `auto_terminal.go`, `callback.go`, `supervisor.go`, `on_error.go`, `terminal_outcome.go`) opens its own `pgx.Tx` via `pool.BeginTx(ctx, pgx.TxOptions{})` and threads it through internal functions.
- `core/store/lockholders.go` (`LockHoldersClient`, ~440 lines) is a pgx-direct lock-holder accessor that exists **parallel to** the proper `LockHoldersStore` interface in `core/storage/`. The supervisor's acquisition tx uses the pgx-direct one; storage's `LockHoldersStore` is used by other call sites. Two impls of overlapping functionality.
- `core/attributes/store.go` (~250 lines) is similarly a pgx-direct attributes accessor parallel to `NodeAttributesStore`.
- The frame engine (`core/frame/engine.go` + `producer.go` + `types.go`, ~660 lines) takes `pgx.Tx` directly and runs raw SQL; its `BeginTx(ctx, pgx.TxOptions{})` shape is hard-coded against pgx.
- `core/scheduler/{scheduler,sweep_locks,invalidate,pure_cascade}.go` hold pgx directly.
- `core/controlapi/{instances,nodes}.go` escape back to pgx via `pgstorage.PgxTxFromStorage(tx)` to call into the frame engine.
- Per-name and per-region advisory xact-locks (`pg_advisory_xact_lock(hashtext(...))`) — load-bearing for blessed invariants 3 (sort-order deadlock prevention) and 4b (single-writer-per-region) — live as Postgres-specific helpers `TakeNamedLockAdvisory` and `TakeRegionAdvisory` in `core/queue/postgres/queue.go`, called from the supervisor's acquisition tx.
- `core/storage/postgres/backend.go::WrapPgxTx` and `::PgxTxFromStorage` are explicit escape hatches that exist precisely because some code holds `pgx.Tx` and needs to call `storage.*` methods inside it. Both directions of the bridge are in active use.

This is fine for the deployment shape rimsky targets today (multi-process, multi-replica, real Postgres). It's incompatible with the deployment shapes the project wants next:

1. **First-touch onboarding.** Today's "run rimsky locally" path requires `docker compose up` against `deploy/docker-compose.yml`. A `docker run rimsky` path that bundles everything into one container with a SQLite file in a volume would be radically simpler for the "what is this thing" demo and the inner dev loop.
2. **Future drivers.** "Postgres or nothing" closes off any other persistence target. Even if SQLite is the only second driver in the foreseeable future, codifying the protocol now makes any later driver additive — but only if the protocol actually carries every persistence operation, not just half of them.

This spec lands a single, cohesive change: **decouple rimsky from pgx end-to-end**, ship Postgres and SQLite as drivers behind that protocol, and build a unified Docker image that defaults to the SQLite driver. The scope is large; the alternatives (a partial protocol with a half-broken SQLite driver; or a multi-spec staging that leaves the codebase pgx-coupled for an extended period) were considered and rejected. The user signed off on "done once and done right."

The architectural backbone:

- **The driver protocol is repository-shaped, not SQL-shaped.** A `Driver` interface aggregates three sub-interfaces (`Queue`, `Store`, `Coordinator`) plus `Migrate(ctx)` and `Close()`. Each driver writes its own SQL inside its own repository implementations; no query builder, no shared dialect translator.
- **`Tx` is first-class in the protocol.** Code that needs a transaction calls `Store.Transaction(ctx, fn)` and gets a `storage.Tx`; the underlying impl uses pgx (or sqlite) — never visible to callers. The `WrapPgxTx` / `PgxTxFromStorage` escape hatches are deleted; no caller outside `core/persistence/postgres/` and `core/persistence/sqlite/` imports a driver-specific library.
- **SQLite is dev-only.** The driver exists for local development and "getting to know rimsky" experimentation, not for production deployments. The startup banner says so loudly on every binary that loads it. Multi-host deployments and multi-replica deployments require Postgres.
- **The three split binaries (`rimsky-scheduler`, `rimsky-supervisor`, `rimsky-control-api`) stay.** No new "all-in-one" Go binary. The unified image runs all three under a tiny in-tree process supervisor (`rimsky-entrypoint`); the per-process images (`rimsky/scheduler`, `rimsky/supervisor`, `rimsky/control-api`) keep being built and published unchanged.
- **No dialect translator, no shared SQL IR, no per-statement adapter.** Postgres-specific features (JSONB, UUID, TIMESTAMPTZ, BIGSERIAL, gen_random_uuid, UUID[] / TEXT[], partial indexes with WHERE clauses) get hand-translated into SQLite equivalents in a separate migration tree. Pre-v1 rule applies: SQLite starts with one consolidated migration capturing the current schema state.

What this spec does not cover (see §12):

- Any change to the `rimsky-cli` / `rimsky-compose` `init` scaffold or its embedded `deploy/docker-compose.yml`.
- Any persistence driver beyond SQLite.
- Web UI, observability extensions, auth, audit logging.
- Helm chart refresh.
- Changes to the existing single-purpose images. They keep working unchanged for ops who deploy split processes against Postgres.

---

## 1. Architecture overview

```
rimsky.yml                                rimsky-{scheduler,supervisor,control-api,migrate}
  persistence:                                |
    driver: postgres | sqlite                 v
    postgres: { dsn: ... }              core/persistence/Driver
    sqlite:   { path: ... }                   |
                                  +-----------+-----------+
                                  v                       v
                       core/persistence/postgres/   core/persistence/sqlite/
                                  |                       |
                                  v                       v
                              pgx/v5                 modernc.org/sqlite
                                  |                       |
                                  v                       v
                           Postgres server          ./state.db (WAL)
```

The runtime binaries pick up the `Driver` from a single `core/persistence/Open(cfg)` constructor and access `.Queue()`, `.Store()`, `.Coordinator()` through it. The interface declarations and the per-driver impls all live under `core/persistence/`. Code outside `core/persistence/{postgres,sqlite}/` and `core/cmd/` does not import driver-specific libraries.

---

## 2. Driver interface shape

### 2.1 The `Driver` umbrella

`core/persistence/driver.go` (new):

```go
package persistence

type Driver interface {
    Queue() Queue
    Store() Store
    Coordinator() Coordinator
    Migrate(ctx context.Context) error
    Close() error
}

type Coordinator interface {
    // TrySchedulerTick attempts to acquire the scheduler-tick exclusion.
    // Returns held=true plus a release fn if acquired; held=false and a nil
    // release fn if another replica holds it. The scheduler skips the tick
    // when held=false.
    TrySchedulerTick(ctx context.Context) (held bool, release func(), err error)

    // AcquireMigrationLock blocks until the migration exclusion is held.
    // The release fn must be safe to call after the parent ctx is cancelled
    // (the runner uses context.Background for the unlock — preserve this
    // pattern across drivers).
    AcquireMigrationLock(ctx context.Context) (release func() error, err error)

    // TakeNamedLockInTx acquires the per-named-lock advisory exclusion
    // inside the supplied tx. Released automatically at tx end (commit or
    // rollback). Load-bearing for blessed invariants 3 (deterministic
    // sort-order acquisition prevents deadlock) and 10 (atomicity of the
    // §7.3 acquisition tx).
    //
    // Postgres impl: pg_advisory_xact_lock(hashtext('rimsky_lock:'+name)).
    // SQLite impl: no-op (the surrounding BEGIN IMMEDIATE writer hold
    // already serializes all writes — strictly stronger than per-name
    // advisory locking).
    //
    // Callers MUST take locks in the deterministic sort order specified in
    // v3 spec §4.10 invariant 3 (named-lock names sorted lexically before
    // region locks sorted by store-name then by region-data bytes). The
    // interface does not enforce ordering; the contract does.
    TakeNamedLockInTx(ctx context.Context, tx storage.Tx, name string) error

    // TakeRegionLockInTx acquires the per-region advisory exclusion inside
    // the supplied tx. Same release semantics as TakeNamedLockInTx.
    // Load-bearing for invariants 3, 4b (single-writer-per-region), and 10.
    //
    // Postgres impl: pg_advisory_xact_lock(hashtext('rimsky_region:'+
    // store_name+':'+region_data_hex)).
    // SQLite impl: no-op (same reason as TakeNamedLockInTx).
    TakeRegionLockInTx(ctx context.Context, tx storage.Tx, storeName string, regionData []byte) error
}
```

The `TakeNamedLockInTx` and `TakeRegionLockInTx` methods live on `Coordinator` rather than `Queue` because they are coordination primitives, not queue operations — the queue happens to be where the Postgres impls live today, but conceptually they're the same family as `TrySchedulerTick` and `AcquireMigrationLock`.

### 2.2 The `Queue` interface

`core/persistence/queue.go` (new — moved from `core/queue/interface.go`, refactored):

```go
package persistence

type Queue interface {
    Enqueue(ctx context.Context, req DispatchRequest) error

    // SelectCandidates returns up to req.Limit dispatch rows the supervisor
    // pool is allowed to consider, filtered by accept-lists and ordered by
    // enqueued_at ascending. Rows are FOR UPDATE SKIP LOCKED inside the
    // caller's tx (Postgres) or held under the writer slot (SQLite); rows
    // the caller does not claim release at tx end. The caller MUST hold an
    // open transaction; passing nil tx returns an error.
    SelectCandidates(ctx context.Context, tx storage.Tx, req SelectCandidatesRequest) ([]Candidate, error)

    // ClaimDispatchRow performs the claimant-guarded UPDATE of
    // rimsky_dispatch.claimed_by from NULL to supervisorID for the given
    // dispatch row, inside the caller's tx. Sets claimed_at and
    // last_heartbeat_at to now(). Returns claimed=true when exactly one
    // row was updated.
    ClaimDispatchRow(ctx context.Context, tx storage.Tx, dispatchID shared.UUID, supervisorID string) (claimed bool, err error)

    // ... remaining methods (Complete, Fail, RemoveForNode, ListOrphanedClaims,
    // ReleaseClaim, GetClaimedBy, RefreshHeartbeat) — same as today's
    // DispatchQueue interface but with pgx.Tx replaced by storage.Tx where applicable.
}
```

The crucial change vs. today: every `pgx.Tx` parameter becomes `storage.Tx`. The supervisor and other callers pass a `storage.Tx` obtained from `Store.Transaction(ctx, fn)`; the Postgres queue impl unwraps it internally to call pgx; the SQLite queue impl unwraps it to call `*sql.Tx`. Neither unwrap is visible outside the driver package.

### 2.3 The `Store` interface

The existing `core/storage/StorageBackend` interface (with all its per-feature sub-interfaces — `TemplateStore`, `InstanceStore`, `NodeStore`, `LockHoldersStore`, etc.) is the right shape. It moves to `core/persistence/store.go` and is renamed `Store` for symmetry with `Queue` and `Coordinator`. The 11 per-feature sub-interfaces move with it:

```go
package persistence

type Store interface {
    Templates() TemplateStore
    TemplateTags() TemplateTagsStore
    Instances() InstanceStore
    StoreLifecycle() StoreLifecycleStore
    Nodes() NodeStore
    LockHolders() LockHoldersStore
    NodeAttributes() NodeAttributesStore
    ClaimHolders() ClaimHoldersStore
    Events() EventStore
    Schedules() ScheduleStore
    Supervisors() SupervisorStore
    Frames() FrameStore                  // NEW; see §3.5
    Transaction(ctx context.Context, fn func(ctx context.Context, tx Tx) error) error
}

// Tx is the transaction handle. Driver-implemented; opaque to callers.
type Tx interface { isTx() }
type TxMarker struct{}
func (TxMarker) isTx() {}
```

The existing `core/storage/interfaces.go` content (the per-feature interface declarations, the `*Row` and `*Input` types, the `LockKind` / `ClaimHolderState` / `TemplateState` / `StoreLifecycleScopeKind` / `StoreLifecycleState` enums, `Tx` and `TxMarker`) all move to `core/persistence/`. Files split per feature per cold-read (`core/persistence/types_templates.go`, `types_instances.go`, etc.) or stay in one `interfaces.go` — implementer's choice (see §11 open question 1).

### 2.4 The `LockHoldersStore` interface — extended

The pgx-direct `LockHoldersClient` in `core/store/lockholders.go` and the proper `LockHoldersStore` in `core/storage/interfaces.go` get merged into one extended `LockHoldersStore` interface under `core/persistence/`. The methods the pgx-direct client adds that aren't in the storage interface today get added (e.g., `ListByStoreRegion`, `CountByNamedLock` per `core/store/lockholders.go:320,340`). After the merge, the `core/store/lockholders.go` file is deleted; every caller goes through the interface.

### 2.5 The `NodeAttributesStore` interface — extended

Same treatment for `core/attributes/store.go`: the pgx-direct accessor merges into `NodeAttributesStore`, the file is deleted. The existing `NodeAttributesStore` already has `Get`, `Upsert`, `MergeDelta` (per `core/storage/interfaces.go:341`). Any methods on the pgx-direct accessor that aren't there get added.

### 2.6 The `FrameStore` interface — new

The frame engine in `core/frame/engine.go` + `producer.go` + `types.go` (~660 lines) is wholesale lifted to use a `FrameStore` interface. The current shape — `func RunTick(ctx, db FrameDB)` taking a `BeginTx(ctx, pgx.TxOptions) (pgx.Tx, error)`-shaped abstraction (`core/frame/engine.go:54`) — gets replaced. The new shape:

- Define `FrameStore` interface in `core/persistence/types_frames.go` declaring every operation the frame engine needs (frame INSERT / SELECT / UPDATE, dispatch INSERT inside frame tx, node state transitions inside frame tx, etc. — to be enumerated during step-1 implementation).
- The frame engine package `core/frame/` is restructured: business logic stays in `core/frame/` and takes a `persistence.Store` (or a `persistence.FrameStore` directly), no pgx import remaining. SQL moves into `core/persistence/postgres/frames.go` and `core/persistence/sqlite/frames.go`.
- `Store.Transaction(ctx, fn)` brackets the frame-tick tx; the frame engine no longer calls `db.BeginTx` directly.

The shape is mechanical (every `pgx.Tx`-taking function becomes a method on the appropriate `*Store`); the trickiest part is enumerating the methods correctly so `FrameStore` is complete on day one. Inv 19 (every claimable candidate has a non-zero `frame_id`, see §3.10) is preserved by retaining the algorithmic logic — the persistence boundary changes only how the row is INSERTed, not the requirement.

### 2.7 Configuration types

`core/persistence/types.go` (new):

```go
package persistence

type Config struct {
    Driver   string           // "postgres" | "sqlite"
    Postgres *PostgresConfig  // required iff Driver == "postgres"
    SQLite   *SQLiteConfig    // required iff Driver == "sqlite"
}

type PostgresConfig struct {
    DSN             string
    MaxOpenConns    int           // 0 → driver default
    MaxIdleConns    int
    ConnMaxLifetime time.Duration
}

type SQLiteConfig struct {
    Path string  // absolute path; relative paths rejected at parse time
}
```

### 2.8 Construction

`core/persistence/open.go` (new): `Open(ctx, cfg) (Driver, error)`. A switch on `cfg.Driver` dispatching to the per-driver constructor. No registry pattern (cold-read: "no DI containers, no abstract factories"); the switch is one of two cases for a long time.

### 2.9 What's deliberately not in the interface

- **No raw SQL surface.** Every method is repository-shaped; each driver writes its own SQL.
- **No `Health()` method.** Existing `/health` endpoint keeps doing its own pool-ping.
- **No connection-pool exposure.** `*pgxpool.Pool` and `*sql.DB` stay encapsulated.
- **No escape hatches.** `WrapPgxTx` and `PgxTxFromStorage` are deleted (see §3.7).

### 2.10 Package import rule update

CLAUDE.md's rules section updates to:

- `core/persistence/` — driver protocol (interfaces, types, Tx) plus per-driver impls. Stdlib + `shared/` + `node/` for the protocol; the per-driver subpackages may import `pgx/v5` (`postgres/`) or `modernc.org/sqlite` (`sqlite/`).
- `core/queue/`, `core/storage/`, `core/migrations/`, `core/store/lockholders.go`, `core/attributes/store.go` — **deleted**. Their contents move into `core/persistence/` (interfaces) and `core/persistence/{postgres,sqlite}/` (impls). Don't reintroduce them.
- `core/frame/` — pure logic, takes a `persistence.Store` parameter; no pgx import.
- `core/scheduler/`, `core/supervisor/`, `core/controlapi/` — unchanged in package boundaries; no `pgx`, `pgxpool`, or `pgconn` imports allowed. The package import rule "they cannot import each other" is unchanged.
- `core/cmd/*` — unchanged; the only packages permitted to import everything needed to wire a binary.

A `golangci-lint` rule (depguard or forbidigo) enforces the no-pgx-outside-persistence rule.

---

## 3. Pgx-decoupling refactor

This section enumerates the specific code changes that make the protocol in §2 actually usable. Each subsection names the files affected and the shape of the change.

### 3.1 Inventory

The non-test files importing `github.com/jackc/pgx/v5` today (36 of them, sampled by `grep -rln "github.com/jackc/pgx" --include='*.go' | grep -v _test.go`) fall into four buckets:

1. **Move into `core/persistence/postgres/` as-is** — files that are already the Postgres impl and just relocate. The 13 files under `core/queue/postgres/` and `core/storage/postgres/`, plus `core/migrations/runner.go` and the migration `*.sql` files.
2. **Move-and-merge into `core/persistence/postgres/` (delete duplicates)** — `core/store/lockholders.go` merges into `lock_holders.go`; `core/attributes/store.go` merges into `node_attributes.go`.
3. **Refactor in place to drop pgx imports** — every file in `core/supervisor/`, `core/scheduler/`, `core/controlapi/`, `core/frame/`. These are business-logic packages that hold `pgx.Tx` and run raw SQL; they're rewritten to take `persistence.Store` and call interface methods, with `Store.Transaction(ctx, fn)` providing tx brackets.
4. **`core/cmd/*` and infra** — `core/cmd/rimsky-{scheduler,supervisor,control-api,migrate}/main.go`, `core/config/scheduler.go`, `core/scenario/harness.go`, `core/internal/pgtest/pgtest.go`, `test/smoke/setup.go`. The first four switch from `pgxpool.New(dsn)` to `persistence.Open(ctx, cfg)`. The pgtest harness keeps its testcontainers Postgres (it's specifically for Postgres-flavored tests); it just exposes a `persistence.Driver` instead of a `*pgxpool.Pool`. The smoke harness similarly.

### 3.2 `DispatchQueue` interface refactor

`core/queue/interface.go` moves to `core/persistence/queue.go`. The interface is renamed `Queue`. The `pgx.Tx` parameters in `SelectCandidates` and `ClaimDispatchRow` become `storage.Tx` (now `persistence.Tx`). The supplemental top-level helpers `TakeNamedLockAdvisory` and `TakeRegionAdvisory` from `core/queue/postgres/queue.go` are deleted; their behavior moves onto `Coordinator.TakeNamedLockInTx` and `TakeRegionLockInTx` (§2.1).

The Postgres impl in `core/queue/postgres/queue.go` moves to `core/persistence/postgres/queue.go`, with internal pgx unwrapping where needed (`tx.(*pgTx).tx`).

### 3.3 Duplicate-accessor merger

`core/store/lockholders.go::LockHoldersClient` and `core/persistence/LockHoldersStore` (the merged interface from §2.4) become one. Method enumeration:

| Today on `LockHoldersClient` | Today on `LockHoldersStore` | After merge |
|---|---|---|
| `Insert(ctx, tx, row)` | `Insert(ctx, in, tx)` | one signature, takes `Tx`, in `LockHoldersStore` |
| `UpdateAddress(ctx, tx, id, supervisorID, address)` | `UpdateAddress(ctx, id, supervisorID, address, tx)` | one signature |
| `Get` (pgx-direct) | `Get(ctx, id, tx)` | merge |
| `DeleteByID(ctx, tx, id, supervisorID)` | `Delete(ctx, id, expectedSupervisorID, tx)` | merge under one name |
| `DeleteIfExpired(ctx, tx, id, supervisorID)` | (absent) | added to interface |
| `CountByNamedLock(ctx, tx, lockName)` | (absent) | added to interface |
| `ListByStoreRegion(ctx, tx, storeName)` | (absent) | added to interface |
| (absent) | `ListByHolderNode`, `ListBySupervisor`, `ExtendHeartbeat`, `ListExpired` | unchanged on interface |
| (heartbeat extension on the pgx-direct one) | `ExtendHeartbeat` on the interface | merge under one name |

`core/store/lockholders.go` is deleted after the merge. Every caller (the supervisor's runner_acquire, runner_terminal, etc.) is rewritten to call methods on `store.LockHolders()`.

### 3.4 `core/attributes/store.go` merger

Same treatment. The pgx-direct `attributes.Store` merges into `NodeAttributesStore`. Method enumeration is straightforward (the existing `NodeAttributesStore` has `Get`, `Upsert`, `MergeDelta`; verify no pgx-direct method is missing during step-1). The `NodeAttributesStore` notably does not currently take a `Tx` parameter (per `core/storage/interfaces.go:341`); after the merge it stays that way for compatibility, with the pgx-direct callers updated to drop their own tx threading where they relied on it. If a caller genuinely needs node-attributes writes inside a multi-call tx, add the Tx-taking variants then.

`core/attributes/store.go` is deleted after the merge. Callers go through `store.NodeAttributes()`.

### 3.5 Frame engine refactor

`core/frame/engine.go` and `core/frame/producer.go` are restructured:

- A new `FrameStore` interface enumerates every operation the frame engine performs against persistence (frame INSERT, frame SELECT/UPDATE, dispatch INSERT inside the frame tx, node state-transition operations the frame engine drives, etc. — exact enumeration during step-1 implementation, since the spec can't accurately list ~660 lines worth of methods up-front without reading the code).
- `core/frame/engine.go` and `producer.go` keep their algorithmic logic but lose all pgx imports. The `FrameDB` abstraction (`core/frame/engine.go:54`, currently `BeginTx(ctx, pgx.TxOptions) (pgx.Tx, error)`) is deleted; the engine functions take `persistence.Store` and use `store.Transaction(ctx, fn)` for tx brackets and `store.Frames().*` (and other sub-stores) for operations.
- The SQL moves to `core/persistence/postgres/frames.go` (Postgres impl of `FrameStore`) and `core/persistence/sqlite/frames.go` (SQLite impl).

This is a substantial refactor in line count but mechanical in shape. The blessed-invariants the frame engine touches (specifically inv 19 — every claimable candidate has a non-zero `frame_id`, per `core/queue/interface.go:113`) are preserved by keeping the algorithmic logic intact and only changing the persistence boundary.

### 3.6 Supervisor refactor

The 11 supervisor files (~4,150 lines: `runner.go`, `runner_acquire.go`, `runner_dispatch.go`, `runner_held_claims.go`, `runner_locks.go`, `runner_terminal.go`, `auto_terminal.go`, `callback.go`, `supervisor.go`, `on_error.go`, `terminal_outcome.go`) are rewritten to drop direct pgx usage:

- Every function that takes a `pgx.Tx` parameter takes a `persistence.Tx` instead.
- Every call to `args.QueuePool.BeginTx(ctx, pgx.TxOptions{})` becomes `args.Store.Transaction(ctx, func(ctx, tx) error { ... })`.
- Every call to the deleted pgx-direct `core/store/lockholders.go::LockHoldersClient` becomes `args.Store.LockHolders().*`.
- Every raw `SELECT … FOR UPDATE` in `core/supervisor/auto_terminal.go` (e.g., `lockLockHolderRow` at `auto_terminal.go:122`) becomes a method on `LockHoldersStore` (e.g., `LockForUpdate(ctx, id, tx)` — same shape as the existing `TemplateStore.LockForUpdate`). Under SQLite the method body omits the `FOR UPDATE` clause (the writer-slot hold is implicit per §6.4). **Implementer:** grep the supervisor (and any other refactored package) for any other raw `SELECT ... FOR UPDATE` patterns and lift each to a typed `*Store.LockForUpdate` method; this enumeration is not exhaustive.
- Calls to the deleted `TakeNamedLockAdvisory` / `TakeRegionAdvisory` become `args.Coordinator.TakeNamedLockInTx(ctx, tx, name)` and `args.Coordinator.TakeRegionLockInTx(ctx, tx, storeName, regionData)`.
- The `pgstorage.WrapPgxTx(tx)` calls (per §3.7) are deleted; the `tx` variable is already a `persistence.Tx` after the refactor.

The supervisor's `RunArgs` struct (currently holding `*pgxpool.Pool`) changes to hold `persistence.Store`, `persistence.Queue`, `persistence.Coordinator` (or a single `persistence.Driver`).

### 3.7 Scheduler refactor

`core/scheduler/{scheduler,sweep_locks,invalidate,pure_cascade,schedule_ticker,recalculate}.go` similarly:

- `pg_try_advisory_lock(SCHEDULER_TICK_KEY)` becomes `coordinator.TrySchedulerTick(ctx)`. The constant `RimskySchedulerTickLockKey` (`core/scheduler/scheduler.go:61`) moves into `core/persistence/postgres/coordinator.go`.
- `pgstorage.PgxTxFromStorage(stx)` calls in `invalidate.go:94` and `pure_cascade.go:233` are deleted; the surrounding code is rewritten to call frame-engine entry points that themselves take `persistence.Store`.
- The orphan reaper's direct `pgxpool` access becomes calls through `store.LockHolders()` (existing `ListExpired` method) and the Postgres-only TTL-arithmetic SQL moves into the impl.

### 3.8 ControlAPI refactor

`core/controlapi/instances.go:470` and `core/controlapi/nodes.go:204` use `pgstorage.PgxTxFromStorage(tx)` to escape into the frame engine. After §3.5 the frame engine takes `persistence.Store` directly; these escape-hatch calls are deleted, replaced by direct frame-engine calls.

### 3.9 Escape-hatch deletion

`core/storage/postgres/backend.go::WrapPgxTx` (line 157) and `::PgxTxFromStorage` (line 169) — both deleted. After §3.6, §3.7, §3.8 there are no callers. The `core/storage/postgres/backend.go::pgTx` type (line 146) stays (it's the internal `Tx` carrier for the Postgres driver), but loses its exported wrap/unwrap accessors.

### 3.10 Blessed-invariant preservation

Every blessed invariant the refactor touches must be preserved or the spec must explicitly justify the change. None are weakened:

- **Inv 1 (illegal state transitions rejected):** node-state transitions live in `core/node/state.go` (unchanged); the persistence calls go through `NodeStore.UpdateState` (existing). The `@blessed-invariant 1` annotation in `core/storage/postgres/nodes.go:5,296` moves with the file to `core/persistence/postgres/nodes.go`. Behavior unchanged.

- **Inv 19 (frame_id non-null on claimable candidates):** preserved by retaining the frame-engine algorithmic logic; the persistence boundary changes only how the row is INSERTed (now via the `FrameStore` interface), not the requirement. The `Candidate.FrameID` doc-comment that today lives in `core/queue/interface.go` moves with the file to `core/persistence/queue.go`.

(CLAUDE.md's blessed-invariant enumeration today jumps from 15 to 20; invariants 16–19 are present in code annotations but not yet listed in CLAUDE.md. That's a pre-existing CLAUDE.md gap; the implementation pass should fill it in as a follow-up doc fix when updating the file paths in the existing entries.)
- **Inv 2 (dispatch claim brackets running window):** `Queue.ClaimDispatchRow` is the same operation under a new interface; lock-eligibility joins against `rimsky_lock_holders` in the impl. Unchanged.
- **Inv 3 (deterministic sort-order acquisition):** the supervisor's runner still computes the sort order; `Coordinator.TakeNamedLockInTx` and `TakeRegionLockInTx` are called in that order. Unchanged.
- **Inv 4 (claimant-guarded release):** all release operations on `Queue` and `LockHoldersStore` carry the `expectedClaimedBy` / `expectedSupervisorID` parameter as today. Unchanged.
- **Inv 4b (single-writer-per-region):** preserved by the `TakeRegionLockInTx` call and the byte-equal region predicate in the lock-holder INSERT path. Unchanged.
- **Inv 5 (verify-before-run):** `Queue.GetClaimedBy` is the same operation. Unchanged.
- **Inv 6 (orphan-claim 5× heartbeat cutoff):** the cutoff is a parameter passed by the scheduler; `LockHoldersStore.ListExpired` returns rows past it. Unchanged.
- **Inv 7 (advisory lock on scheduler tick):** Postgres impl preserves `pg_try_advisory_lock`. SQLite impl uses `sync.Mutex` (per §4.2 and the dev-only positioning).
- **Inv 8 (session advisory lock on migrations):** Postgres impl preserves `pg_advisory_lock`. SQLite impl uses `sync.Mutex`.
- **Inv 9a (lock state lives only in postgres):** the wording broadens — lock state lives in the **persistence layer** (`rimsky_lock_holders` table; same name in both drivers). The original wording "only in postgres" was a function of having one driver; the substantive constraint (no store implementation persists lock state) is preserved. The CLAUDE.md text updates to "Lock state lives only in the persistence layer; `rimsky_lock_holders` is the sole authority. No store-service persists lock state."
- **Inv 9b (no reader-lease serialization in stores):** unaffected by this spec.
- **Inv 10 (acquisition tx atomicity):** the supervisor's acquisition tx is now bracketed by `Store.Transaction(ctx, fn)`; the dispatch claim, lock-holder INSERTs, and address UPDATE all happen inside the same `persistence.Tx`. The store's own state mutations (the RPC to `Store.Open` from rimsky's perspective) remain in their own store-internal tx, decoupled. Unchanged in semantics.
- **Inv 11 (userdata opaque):** unaffected.
- **Inv 12 (attributes validate at dispatch + commit):** unaffected.
- **Inv 13 (held-claim auto-terminal):** the `SELECT … FOR UPDATE` on the lock-holder row becomes `LockHoldersStore.LockForUpdate(ctx, id, tx)`. Same semantics under Postgres; under SQLite the writer-slot hold is implicit. Aggregate-outcome resolution (commit vs abandon) is unchanged.
- **Inv 14 (region byte-equality):** the conflict predicate in lock-holder INSERT remains byte-equal on `region_data`. Each driver implements byte-equal comparison its own way (Postgres `JSONB` byte-equality vs SQLite `TEXT` byte-equality; both return the same boolean). Unchanged.
- **Inv 15 (Open inside acquisition tx):** the supervisor's call to `Store.Open` (the wire RPC, not the persistence interface) still happens inside the `persistence.Tx` bracketed by `Store.Transaction`. Under SQLite this serializes other writers — documented constraint per §6.5.
- **Inv 20 (claim content inert):** unaffected.

Additionally the `@blessed-invariant` source-code annotations (per CLAUDE.md) move to their new file paths after the refactor. The invariants themselves are unchanged.

---

## 4. Coordinator implementation

### 4.1 Postgres (`core/persistence/postgres/coordinator.go`)

Preserves blessed invariants 7 and 8 directly:

- `TrySchedulerTick(ctx)`: acquires a dedicated connection, runs `SELECT pg_try_advisory_lock($1)` with the existing `RimskySchedulerTickLockKey` constant (relocated from `core/scheduler/scheduler.go:61`). If true → release fn calls `pg_advisory_unlock` then `conn.Release()` (the unlock uses `context.Background()` so a cancelled parent ctx never strands the lock). If false → release fn is nil and `conn.Release()` runs immediately.
- `AcquireMigrationLock(ctx)`: same shape with `pg_advisory_lock` (blocking) and the existing `advisoryLockKey` constant (relocated from `core/migrations/runner.go:18`). Release fn runs `pg_advisory_unlock` then `conn.Release()`, also using `context.Background()` for the unlock.

  **Note on connection split.** Today's runner holds the advisory lock and runs migration SQL on the **same** dedicated connection (`runner.go:26-39`). After the refactor, `coord.AcquireMigrationLock` opens its own dedicated conn (per the bullet above); the `exec/queryHas/recordRun` callbacks the driver supplies to `Migrator` (per §5.2) will run on a different conn (likely pool-acquired). This is functionally fine — the lock-holder conn sits idle holding the session-scoped advisory lock for the migration's duration; cross-process serialization is still correct because the lock is session-scoped and the lock-conn's lifetime spans the migration. Behavior is preserved; the connection-affinity changes.
- `TakeNamedLockInTx(ctx, tx, name)`: unwraps `tx` to a `pgx.Tx`, runs `SELECT pg_advisory_xact_lock(hashtext('rimsky_lock:' || $1))`. Released automatically at tx end.
- `TakeRegionLockInTx(ctx, tx, storeName, regionData)`: unwraps `tx`, runs `SELECT pg_advisory_xact_lock(hashtext('rimsky_region:' || $1 || ':' || encode($2, 'hex')))`. Released automatically at tx end.

The two key constants (`RimskySchedulerTickLockKey`, `advisoryLockKey`) consolidate into this file.

### 4.2 SQLite (`core/persistence/sqlite/coordinator.go`)

A `sync.Mutex` per coordination point, and nothing else for the cross-process methods:

```go
type sqliteCoordinator struct {
    schedulerTick sync.Mutex
    migration     sync.Mutex
}
```

- `TrySchedulerTick`: `mu.TryLock()`. If acquired → release fn unlocks. If not → held=false, release fn nil.
- `AcquireMigrationLock`: `mu.Lock()`. Release fn unlocks.
- `TakeNamedLockInTx(ctx, tx, name)`: returns nil. (The surrounding `BEGIN IMMEDIATE` writer-slot hold already serializes all writes — strictly stronger than per-name advisory locking.)
- `TakeRegionLockInTx(ctx, tx, storeName, regionData)`: returns nil. Same reason.

The two no-op methods are honest: under SQLite, the per-name and per-region advisory locks are degenerate because the writer slot is held for the entire transaction. Documented in the method comments.

The reason for the minimal cross-process impl: the blessed invariants exist because Postgres deployments support multi-replica schedulers — `pg_try_advisory_lock` is the only thing keeping two scheduler replicas from double-firing the tick. Under SQLite-as-dev-only, that scenario is forbidden by the startup banner. Defending against it in code (TTL'd table rows, etc.) buys nothing for a dev driver and adds maintenance surface.

---

## 5. Migration story

### 5.1 Per-driver migration trees

```
core/persistence/
  migrations.go                     # driver-agnostic runner (lifted from core/migrations/runner.go)
  postgres/
    migrations/
      001-initial.sql               # moved from core/migrations/001-initial.sql
      002-frame-resolution.sql      # moved
      003-template-registry-and-lifecycle.sql   # moved
      embed.go                      # //go:embed *.sql
  sqlite/
    migrations/
      001-initial.sql               # hand-written, current schema state in SQLite dialect
      embed.go
```

Pre-v1 rule applies (`.claude/rules/rules.md`): SQLite starts with **one** consolidated migration that captures the current schema state, not a translation of every Postgres migration. There is no SQLite production data to preserve, and the SQLite tree starts the day this spec lands. Once v1 ships, both trees become append-only with their own numbering.

### 5.2 Runner shape

`core/persistence/migrations.go`:

```go
type Migrator struct {
    fs        embed.FS                            // driver-supplied
    exec      func(ctx, sql string) error         // driver-supplied
    queryHas  func(ctx, filename string) (bool, error)
    recordRun func(ctx, filename string) error
}

func (m *Migrator) Run(ctx context.Context, coord Coordinator, log shared.Logger) error {
    release, err := coord.AcquireMigrationLock(ctx)
    if err != nil { return err }
    // The release fn must run even if ctx is cancelled (the Postgres impl
    // uses context.Background internally for the unlock; the SQLite impl
    // just calls mu.Unlock). Defer is sufficient because both impls'
    // release fns are ctx-independent.
    defer func() { _ = release() }()
    // for each *.sql, sorted by filename:
    //   if queryHas → continue
    //   exec sql + recordRun in a single tx (driver-implemented)
}
```

The runner has no SQL of its own; the driver supplies four small functions. Idempotent table-creation (`CREATE TABLE IF NOT EXISTS rimsky_migrations`) lives in each driver's `001-initial.sql` plus a small bootstrap call before the loop.

### 5.3 Why no shared IR / dialect translator

Postgres-specific features are sprinkled through the migrations: `JSONB`, `UUID`, `TIMESTAMPTZ`, `BIGSERIAL`, `gen_random_uuid()`, `UUID[]`, partial indexes with `WHERE` clauses, `TEXT[]`, `ON DELETE CASCADE`, CHECK constraints. SQLite has different shapes for every one. A translator would either be a leaky abstraction the dialect rules trample on, or a full SQL parser. Hand-writing the SQLite tree once is the cheap, honest path.

### 5.4 Documented dialect drift

The driver-specific repository implementations encapsulate these — interface methods take/return Go types (`uuid.UUID`, `time.Time`, `[]uuid.UUID`, `[]string`, `json.RawMessage`), and each driver serializes appropriately. Callers never see the storage representation.

| Feature | Postgres | SQLite |
|---|---|---|
| JSON | `JSONB` | `TEXT` (no JSON1 queries needed; rimsky never queries inside JSON columns) |
| UUID | `UUID` + `gen_random_uuid()` | `TEXT` + app-side `uuid.New()` at INSERT |
| Timestamp | `TIMESTAMPTZ` + `NOW()` | `TEXT` ISO-8601 + app-side `time.Now().UTC().Format(time.RFC3339Nano)` |
| Auto-increment | `BIGSERIAL` | `INTEGER PRIMARY KEY AUTOINCREMENT` |
| `UUID[]` | native array | JSON array stored as TEXT |
| `TEXT[]` | native array | JSON array stored as TEXT |
| Partial index | `CREATE INDEX … WHERE …` | same syntax (supported since 3.8) |
| Upsert | `INSERT … ON CONFLICT (col) DO UPDATE SET col = EXCLUDED.col` | same shape, lowercase `excluded` (3.24+) |
| Returning rows | `RETURNING` | same (3.35+; `modernc.org/sqlite` ships well newer) |
| Per-row pessimistic lock | `SELECT … FOR UPDATE` | implicit via the surrounding `BEGIN IMMEDIATE` writer hold; SQL omits the `FOR UPDATE` clause |
| Foreign-key cascade | always on | requires `_foreign_keys=ON` pragma at connection time (see §6.1) |

### 5.5 `rimsky-migrate` binary

Today: `pgxpool.New(dsn)` → `migrations.Run(ctx, pool, log)`. After: `persistence.Open(ctx, cfg)` → `driver.Migrate(ctx)` → `driver.Close()`. Same flag surface, same behavior, just driver-aware.

### 5.6 The lifted `core/migrations/` package

The package goes away. `runner.go` becomes `core/persistence/migrations.go`; `runner_test.go` follows; `embed.go` and the `*.sql` files move under `core/persistence/postgres/migrations/`. The `advisoryLockKey` constant moves into `core/persistence/postgres/coordinator.go` (it's coordinator state, not migration state). No back-compat shim.

---

## 6. SQLite driver implementation notes

### 6.1 Connection & pragmas

Single `*sql.DB` opened with DSN flags wired into the connect string:

```
file:<path>?_journal_mode=WAL&_synchronous=NORMAL&_busy_timeout=5000&_foreign_keys=ON&_txlock=immediate
```

- `WAL` — concurrent readers + one writer; required for the rimsky workload.
- `NORMAL` synchronous — durable across app crashes, not power loss; right default for dev. Not exposed as a knob.
- `_busy_timeout=5000` — SQLite waits up to 5s on `SQLITE_BUSY`.
- `_foreign_keys=ON` — load-bearing for blessed invariants 13 and 4 (the `ON DELETE CASCADE` clauses in the schema).
- `_txlock=immediate` — every transaction starts as `BEGIN IMMEDIATE` (writer slot acquired up-front).

### 6.2 Pool sizing

`db.SetMaxOpenConns(1)` for writes — SQLite serializes writers anyway. Reads can use a separate read-only `*sql.DB` opened with `?mode=ro` and a normal-sized pool if read concurrency becomes a bottleneck. v1 ships single-connection pool; revisit only if profiling shows a real problem.

### 6.3 Time, JSON, UUID, array handling

- **Time.** Every method serializes via `time.Time.UTC().Format(time.RFC3339Nano)` and parses via `time.Parse(time.RFC3339Nano, …)` at the SQL boundary. App-side `time.Now()` everywhere a Postgres method would have used `NOW()` / `DEFAULT NOW()`. (Whether `modernc.org/sqlite`'s default time-binding makes the explicit format redundant: see §11 open question 2.)
- **JSON.** `JSONB` columns become `TEXT`. Repository methods that accept `json.RawMessage` write the bytes as-is; methods that read return `json.RawMessage` from the TEXT.
- **UUID.** `UUID` columns become `TEXT` storing canonical 36-char form. Repository methods take `uuid.UUID`, format/parse at the SQL boundary. `gen_random_uuid()` defaults become app-side `uuid.New()` at the call site.
- **Arrays.** `UUID[]` and `TEXT[]` become `TEXT` columns holding JSON arrays. Marshal with `json.Marshal([]string{…})`; unmarshal symmetric. (Whether existing call sites distinguish empty-array from NULL: see §11 open question 3.)

### 6.4 `SELECT … FOR UPDATE` translation

The pattern in `TemplateStore.LockForUpdate` and the new `LockHoldersStore.LockForUpdate` (§3.6) is: under SQLite, the surrounding `BEGIN IMMEDIATE` already holds the writer slot for the entire transaction, which is strictly stronger than per-row `FOR UPDATE`. The repository method shape is the same; only the SQL differs (omit the `FOR UPDATE` clause). Document inline.

### 6.5 Inv 15's `Store.Open` inside the acquisition tx

Under SQLite this serializes every other write for the duration of the wire-RPC `Store.Open` call. Documented constraint, no fix; the startup banner makes the dev-only positioning loud enough.

### 6.6 File location & permissions

`core/persistence/sqlite/Open(ctx, cfg)` accepts an absolute `path`. If the parent directory doesn't exist, returns an error (don't auto-mkdir — surfacing the misconfig is better than silently creating a directory). DB file created with `0600`; WAL/SHM sidecars get the same permissions.

### 6.7 Startup banner

Every binary that opens a SQLite driver logs at startup, at warn level:

```
log.Warn("persistence driver in use",
    "driver", "sqlite",
    "path", "/var/lib/rimsky/state.db",
    "warning", "SQLite driver is for local development only — not supported for production. Use the postgres driver for deployed rimsky instances.")
```

Logged once per process, at `persistence.Open` time.

### 6.8 SQLite Go library: `modernc.org/sqlite`

Pure-Go SQLite (transpiled from C). No cgo. Drop-in `database/sql` driver. The performance gap vs. cgo `mattn/go-sqlite3` (typically 2–3×) is irrelevant for a dev driver.

The cgo alternative was rejected: cgo would force every binary off the existing pure-static `gcr.io/distroless/static` base, complicate the GitHub Actions release matrix with cgo cross-compilers per target platform, and break the existing pure-Go static-binary story. None of those costs buys anything for a dev driver. Build-tag-split between both was rejected as twice the surface to maintain for no benefit.

---

## 7. Unified Docker image (`rimsky/all`)

### 7.1 Image contents

- `/usr/local/bin/rimsky-scheduler`
- `/usr/local/bin/rimsky-supervisor`
- `/usr/local/bin/rimsky-control-api`
- `/usr/local/bin/rimsky-migrate`
- `/usr/local/bin/rimsky-entrypoint` (the in-tree process supervisor; see §7.3)
- `/etc/rimsky/rimsky.yml` (default, SQLite-flavored — see §7.4)
- `/etc/rimsky/supervisor-config.yml` (default, callback-host = `localhost`)
- `/var/lib/rimsky/` (volume mount target; default DB path `state.db` lives here)

### 7.2 Base image

`gcr.io/distroless/static-debian12:nonroot`. Pure-Go `modernc.org/sqlite` keeps the binaries cgo-free, so `static` works without glibc. `nonroot` runs as uid 65532, which owns `/var/lib/rimsky/`.

### 7.3 Process supervisor: `rimsky-entrypoint`

A new `core/cmd/rimsky-entrypoint/main.go` (~150 lines) is the image's `ENTRYPOINT`:

1. Runs `rimsky-migrate` synchronously. Non-zero exit → entrypoint exits with the same code; container restarts under whatever orchestrator is running it.
2. Spawns `rimsky-scheduler`, `rimsky-supervisor`, `rimsky-control-api` as child processes, each inheriting stdout/stderr.
3. Forwards `SIGTERM` and `SIGINT` to all three children. Waits for all three to exit with a deadline (default 30s); after the deadline, sends `SIGKILL`.
4. If any child exits non-zero before a shutdown signal, the entrypoint kills the others and exits with that child's exit code. Crash-loop semantics: container orchestrator restarts the whole thing.
5. All three children run with `RIMSKY_CONFIG=/etc/rimsky/rimsky.yml` and `RIMSKY_SUPERVISOR_CONFIG=/etc/rimsky/supervisor-config.yml` unless overridden via env.

**Log multiplexing.** Today the rimsky binaries don't add a per-binary discriminator field to their slog output (verified in `core/cmd/rimsky-{scheduler,supervisor,control-api}/main.go:48,55,68`). The entrypoint sets `RIMSKY_LOG_BINARY={scheduler,supervisor,control-api}` in each child's env; the child binaries' slog setup (a small change in each `main.go`) reads that env and adds it as a structured field via `slog.With("binary", os.Getenv("RIMSKY_LOG_BINARY"))` if set. Operators grepping the container's stdout can filter by `"binary":"supervisor"` etc. The change is small and benefits non-unified deployments too (operators in any environment can set the env var).

**Why a Go binary instead of s6-overlay / runit / shell + traps:**

- s6-overlay adds runtime, init scripts, and a different mental model than the rest of the codebase. Not worth it for three children.
- A shell entrypoint with `trap` is fragile around signal forwarding, exit-code propagation, and migrate-then-spawn ordering. Eventually rewritten in something typed.
- A small Go binary in the same module reuses the existing `log/slog`, follows the same style rules, and is unit-testable.

### 7.4 Default `rimsky.yml` baked into the image

```yaml
persistence:
  driver: sqlite
  sqlite:
    path: /var/lib/rimsky/state.db

stores: {}
named_locks: {}
executors: {}
```

`stores: {}` is intentional: the image is the orchestrator only, not the orchestrator + every executor + every store-service. Operators who want a richer dev environment with stub executors use the existing `deploy/docker-compose.yml` or a future `init` scaffold update.

### 7.5 Default invocation

```sh
docker run --rm -p 8080:8080 -v rimsky-state:/var/lib/rimsky rimsky/all
```

Boots all three processes against `/var/lib/rimsky/state.db`. Control-api on `:8080`, supervisor callback on `localhost:9090` (in-image only). Health check: `curl http://localhost:8080/health`.

### 7.6 Operator override paths

- `-v ./my-rimsky.yml:/etc/rimsky/rimsky.yml:ro` — replace the default config.
- `-e RIMSKY_CONFIG=/path/to/elsewhere.yml` — point at a different config path.
- `--entrypoint /usr/local/bin/rimsky-control-api` — bypass the unified entrypoint and run a single binary (drop-in replacement for the existing single-purpose images).

### 7.7 Build

`deploy/Dockerfile.all`, built by `deploy/build-images.sh` alongside the existing per-process Dockerfiles (which also live under `deploy/` per the current convention). The single-purpose images keep being built and published unchanged.

### 7.8 What this image is not

- Not a replacement for `deploy/docker-compose.yml`. That stack remains the reference for "full dev environment with stub executors and stub stores in separate containers."
- Not a production deployment target. Image labels (`org.opencontainers.image.description`) say so explicitly. SQLite startup banner says so on every boot.
- Not orchestrated for high availability. One image = one PID 1 = one host. `docker compose up rimsky/all` with `replicas: 3` would create three independent SQLite databases — broken. Document; don't try to detect.

---

## 8. Config surface (`rimsky.yml`)

### 8.1 Schema

A new top-level `persistence:` block, parsed by all three runtime binaries plus `rimsky-migrate`. Same strict-equality validation posture as the existing `stores:` block:

```yaml
persistence:
  driver: postgres            # required; "postgres" | "sqlite"

  postgres:                   # required iff driver == postgres
    dsn: postgres://rimsky:rimsky@postgres:5432/rimsky?sslmode=disable
    max_open_conns: 25        # optional; pgx pool default if omitted
    max_idle_conns: 5         # optional
    conn_max_lifetime: 1h     # optional Go-style duration

  sqlite:                     # required iff driver == sqlite
    path: /var/lib/rimsky/state.db
```

### 8.2 Validation

**Mutually-exclusive sub-blocks.** Setting `driver: postgres` while a `sqlite:` block is also present (or vice versa) is a loud pre-flight startup error. Same posture as the CLI/compose spec's `rimsky_config.inline` ↔ `rimsky_config.path` mutual exclusion.

**Required-field-by-driver:**

- `driver: postgres` → `postgres.dsn` required; everything else optional.
- `driver: sqlite` → `sqlite.path` required, must be absolute. Parent directory must exist (per §6.6).

### 8.3 Loaded by

All four binaries (`rimsky-scheduler`, `rimsky-supervisor`, `rimsky-control-api`, `rimsky-migrate`) read `RIMSKY_CONFIG` per the control-plane v1 spec §3.1. Each calls `persistence.Open(ctx, cfg.Persistence)` once at startup, holds the resulting `Driver` for the process lifetime, and `Close()`s it on shutdown.

### 8.4 Cross-binary consistency

The three runtime binaries don't validate that they're pointed at the *same* persistence target — operator's responsibility (intrinsic when they all read from one `rimsky.yml`). For the unified image, the entrypoint passes one `RIMSKY_CONFIG` to all three.

### 8.5 Default `rimsky.yml` precedence

1. `RIMSKY_CONFIG` env var (highest).
2. `/etc/rimsky/rimsky.yml` (default in the unified image).
3. Startup error if neither resolves to a readable file.

### 8.6 Removed env vars

The current `RIMSKY_DB_URL` (set in each binary's `main.go`) goes away in favor of `persistence.postgres.dsn`. No fallback shim. Pre-v1 demolition rule applies. Documented in `CHANGELOG.md` and the operator-guide migration section.

### 8.7 Backwards-compat with the existing `deploy/docker-compose.yml`

The existing compose stack mounts `deploy/rimsky.yml` into the three containers. That file gains a `persistence:` block (`driver: postgres`, dsn pointing at the postgres sidecar). One file change, no code change beyond the loader. The existing single-purpose images keep working as-is.

---

## 9. Test story

### 9.1 Driver-conformance suite (load-bearing)

A new `core/persistence/conformance/` package declares table-driven tests parameterized on a `func(t *testing.T) persistence.Driver` factory. Same suite runs against both drivers; any blessed-invariant-relevant operation that can land in `Queue`, `Store`, or `Coordinator` gets a conformance test. Coverage targets:

- **Dispatch claim/release semantics** (inv 4, inv 6).
- **Multi-lock acquisition tx atomicity** (inv 10) — failure injection mid-tx via a wrapper driver.
- **Verify-before-run read** (inv 5).
- **Held-claim auto-terminal serialization** (inv 13).
- **Migration idempotency** (inv 8).
- **Coordinator scheduler-tick exclusion** (inv 7) — limited form under SQLite (caveat below).
- **Foreign-key cascade** (inv 13).
- **Region byte-equality** (inv 14).
- **Time-arithmetic queries** (orphan reaper).
- **Tx semantics** — `Store.Transaction(ctx, fn)` runs all participating store calls inside one tx; rollback on error reverts every change; multi-call atomicity verified (inv 10).
- **Sort-order coordination** — `TakeNamedLockInTx` and `TakeRegionLockInTx` called in sort order do not deadlock under contention (inv 3); SQLite no-op is observably consistent.

The suite runs as `TestConformance_Postgres` (testcontainers via `core/internal/pgtest`) and `TestConformance_SQLite` (ephemeral file in `t.TempDir()`). Both run on every CI invocation that touches `core/persistence/`.

**Caveat.** Inv 7 and inv 8 conformance tests under SQLite verify only the `sync.Mutex` semantics, since the SQLite coordinator is not designed for cross-process exclusion (per §4.2). The tests are no-op-shaped under SQLite — they verify mutex correctness, not cross-process correctness. Documented in the test file.

### 9.2 Existing scenario suite (`test/scenarios/`) stays Postgres-only

The scenarios in `test/scenarios/` exercise the full supervisor + scheduler + control-api stack against a real Postgres via testcontainers. They remain the canonical truth for invariant coverage under realistic concurrency. The SQLite driver is **not** plugged into `test/scenarios/`:

1. The scenarios test deployment behaviors (heartbeat races, multi-replica scheduler ticks, multi-supervisor contention) that don't apply to SQLite's single-process-per-host posture.
2. SQLite's `BEGIN IMMEDIATE` serialization changes timing characteristics enough that a passing scenario test against SQLite proves nothing about the same scenario against Postgres. The conformance suite covers what's actually portable.

Documented in the conformance package's doc comment.

### 9.3 Migration runner tests

`core/persistence/migrations_test.go` (lifted from `core/migrations/runner_test.go`) becomes driver-parameterized: same factory pattern as the conformance suite. Tests cover runner-level concerns (filename ordering, record-then-skip, lock-acquired-then-released).

### 9.4 Per-driver smoke

`core/persistence/postgres/integration_test.go` and `core/persistence/sqlite/integration_test.go` — small driver-specific tests for things the conformance suite can't observe (e.g., the SQLite driver opens with `_foreign_keys=ON`, the Postgres driver's pool config gets applied).

### 9.5 Refactor regression coverage

The pgx-decoupling refactor (§3) is mechanical, but the supervisor and frame-engine changes are large enough to risk regressions. The existing `core/supervisor/*_test.go` and `core/scheduler/*_test.go` and the scenario suite cover the behavior; running them under both drivers (the conformance suite) and against the testcontainers Postgres (the scenario suite) provides defense in depth. No new test classes — this isn't new functionality.

### 9.6 Unified-image smoke

A new `test/smoke/all/smoke_test.go`:

1. `docker build -f deploy/Dockerfile.all` (skip if Docker unavailable).
2. `docker run --rm -d -p :8080 -v <tempdir>:/var/lib/rimsky rimsky/all:test`.
3. Polls `/health` until 200.
4. Hits `POST /templates` with a minimal template, `POST /templates/{ref}/deploy`, `POST /instances`.
5. Polls `GET /instances/{id}` until `terminated_at` is set.
6. `docker stop`; asserts clean exit codes.

Catches "did the entrypoint forward signals correctly," "does migrate-then-spawn ordering work," "does the SQLite file persist across container restart with the volume mount." Gated behind `//go:build smoke`; runs nightly, not every PR.

### 9.7 TypeScript executor

`executors/claude-agent/` doesn't touch persistence; no test changes there.

### 9.8 CI surface

- `go test ./core/persistence/...` — unit + conformance against Postgres (testcontainers) + conformance against SQLite (ephemeral file). Every PR.
- `go test ./test/scenarios/...` — Postgres-only, unchanged.
- `go test ./test/smoke/all/...` — gated `//go:build smoke`, nightly job.

---

## 10. Affected code

This section is large because the refactor is large. Each item is necessary; none are speculative.

### 10.1 New files

**Persistence protocol:**
- `core/persistence/driver.go` — `Driver`, `Coordinator` interfaces.
- `core/persistence/queue.go` — `Queue` interface (lifted from `core/queue/interface.go` with pgx.Tx → persistence.Tx).
- `core/persistence/store.go` — `Store` interface umbrella.
- `core/persistence/types_*.go` — per-feature types (`types_templates.go`, `types_instances.go`, `types_nodes.go`, `types_lock_holders.go`, `types_claim_holders.go`, `types_node_attributes.go`, `types_events.go`, `types_schedules.go`, `types_supervisors.go`, `types_template_tags.go`, `types_store_lifecycle.go`, `types_frames.go`, `types_dispatch.go`) — lifted from `core/storage/interfaces.go` + `core/queue/interface.go`. Or one consolidated `interfaces.go` if implementer prefers (see §11 open question 1).
- `core/persistence/types.go` — `Config`, `PostgresConfig`, `SQLiteConfig`, `Tx`, `TxMarker`.
- `core/persistence/open.go` — `Open(ctx, cfg) (Driver, error)` switch.
- `core/persistence/migrations.go` — driver-agnostic runner.
- `core/persistence/migrations_test.go` — driver-parameterized.
- `core/persistence/conformance/conformance.go` — table-driven test suite parameterized on a driver factory.
- `core/persistence/conformance/conformance_test.go` — runs the suite against both drivers.

**Postgres driver:**
- `core/persistence/postgres/driver.go` — `Driver` impl wrapping `*pgxpool.Pool`.
- `core/persistence/postgres/queue.go` — moved from `core/queue/postgres/queue.go`; pgx-direct internals; `TakeNamedLockAdvisory` and `TakeRegionAdvisory` deleted (their behavior moves to `coordinator.go`).
- `core/persistence/postgres/queue_test.go` — moved.
- `core/persistence/postgres/{nodes,instances,templates,template_tags,schedules,supervisors,events,node_attributes,store_lifecycle,lock_holders,claim_holders,backend}.go` — moved from `core/storage/postgres/`, preserving filenames per cold-read's feature-per-file rule.
- `core/persistence/postgres/lock_holders.go` — extended to absorb the deleted `core/store/lockholders.go::LockHoldersClient` methods (per §3.3).
- `core/persistence/postgres/node_attributes.go` — extended to absorb the deleted `core/attributes/store.go` methods (per §3.4).
- `core/persistence/postgres/frames.go` — new; SQL for the new `FrameStore` interface (extracted from `core/frame/engine.go` + `producer.go`, per §3.5).
- `core/persistence/postgres/postgres_test.go` — moved.
- `core/persistence/postgres/coordinator.go` — `pg_advisory_lock` impls for `TrySchedulerTick`, `AcquireMigrationLock`, `TakeNamedLockInTx`, `TakeRegionLockInTx`. Constants `RimskySchedulerTickLockKey` and `advisoryLockKey` consolidated here.
- `core/persistence/postgres/migrate.go` — wires the runner with Postgres exec/has/record callbacks.
- `core/persistence/postgres/migrations/{001-initial,002-frame-resolution,003-template-registry-and-lifecycle}.sql` — moved from `core/migrations/`.
- `core/persistence/postgres/migrations/embed.go`.
- `core/persistence/postgres/integration_test.go` — driver-specific smoke.

**SQLite driver:**
- `core/persistence/sqlite/driver.go` — `Driver` impl wrapping `*sql.DB`.
- `core/persistence/sqlite/queue.go`.
- `core/persistence/sqlite/{nodes,instances,templates,template_tags,schedules,supervisors,events,node_attributes,store_lifecycle,lock_holders,claim_holders,frames,backend}.go` — per-feature impls mirroring the Postgres layout.
- `core/persistence/sqlite/coordinator.go` — `sync.Mutex` + no-op xact-locks per §4.2.
- `core/persistence/sqlite/migrate.go`.
- `core/persistence/sqlite/migrations/001-initial.sql` — hand-written, current schema state in SQLite dialect.
- `core/persistence/sqlite/migrations/embed.go`.
- `core/persistence/sqlite/integration_test.go`.

**Unified image:**
- `core/cmd/rimsky-entrypoint/main.go` — process supervisor (~150 lines).
- `core/cmd/rimsky-entrypoint/main_test.go` — signal forwarding, migrate-then-spawn ordering, child-crash propagation.
- `deploy/Dockerfile.all` — unified image (matches the convention of other Dockerfiles under `deploy/`).
- `deploy/rimsky-all.yml` — default `rimsky.yml` baked into the unified image.
- `test/smoke/all/smoke_test.go` — gated by `//go:build smoke`.

### 10.2 Moved (relocated, not deleted)

- `core/queue/postgres/queue.go` → `core/persistence/postgres/queue.go`.
- `core/queue/postgres/queue_test.go` → `core/persistence/postgres/queue_test.go`.
- `core/storage/postgres/*.go` → `core/persistence/postgres/*.go`.
- `core/migrations/{001-initial,002-frame-resolution,003-template-registry-and-lifecycle}.sql` → `core/persistence/postgres/migrations/`.
- `core/migrations/embed.go` → `core/persistence/postgres/migrations/embed.go` (regenerated for the new package).
- `core/migrations/runner.go` → `core/persistence/migrations.go` (refactored to driver-agnostic).
- `core/migrations/runner_test.go` → `core/persistence/migrations_test.go` (refactored to driver-parameterized).
- `core/queue/interface.go` content → `core/persistence/queue.go` (refactored: pgx.Tx → persistence.Tx).
- `core/storage/interfaces.go` content → `core/persistence/{store.go, types_*.go}` (interfaces and types).

### 10.3 Deleted

- `core/migrations/` — entire directory.
- `core/queue/postgres/` — entire directory.
- `core/storage/postgres/` — entire directory.
- `core/queue/` — entire directory (interfaces moved to `core/persistence/`).
- `core/storage/` — entire directory (interfaces moved to `core/persistence/`).
- `core/store/lockholders.go` — pgx-direct duplicate, merged into `LockHoldersStore` per §3.3.
- `core/attributes/store.go` — pgx-direct duplicate, merged into `NodeAttributesStore` per §3.4.
- `core/storage/postgres/backend.go::WrapPgxTx`, `::PgxTxFromStorage` — escape hatches deleted per §3.9 (the file moves; these specific exported helpers don't).

### 10.4 Refactored in place

These files keep their package locations but lose pgx imports and switch to the persistence interface.

**Supervisor (every file in `core/supervisor/`):**
- `runner.go`, `runner_acquire.go`, `runner_dispatch.go`, `runner_held_claims.go`, `runner_locks.go`, `runner_terminal.go` — pgx.Tx parameters → persistence.Tx; `pool.BeginTx` → `store.Transaction`; calls into the deleted `LockHoldersClient` → `store.LockHolders().*`; calls into `TakeNamedLockAdvisory` / `TakeRegionAdvisory` → `coord.TakeNamedLockInTx` / `TakeRegionLockInTx`; `pgstorage.WrapPgxTx` calls deleted.
- `auto_terminal.go` — same; raw `SELECT … FOR UPDATE` becomes `store.LockHolders().LockForUpdate(ctx, id, tx)`.
- `callback.go`, `supervisor.go` — `*pgxpool.Pool` parameters → `persistence.Driver` (or its sub-interfaces).
- `on_error.go`, `terminal_outcome.go` — likely unaffected, verify.
- `RunArgs` struct refactored to hold `persistence.Driver` (or `Store`, `Queue`, `Coordinator` separately).

**Scheduler (every file in `core/scheduler/`):**
- `scheduler.go` — `pg_try_advisory_lock` call → `coord.TrySchedulerTick`; `*pgxpool.Pool` → `persistence.Driver`; orphan reaper uses `store.LockHolders().ListExpired`.
- `sweep_locks.go` — pgx.Tx → persistence.Tx; tx open via `store.Transaction`.
- `invalidate.go`, `pure_cascade.go` — `pgstorage.PgxTxFromStorage` calls deleted; frame-engine entry points take `persistence.Store` directly.
- `recalculate.go`, `schedule_ticker.go` — verify; likely small.

**ControlAPI (`core/controlapi/`):**
- `instances.go:470`, `nodes.go:204` — `pgstorage.PgxTxFromStorage` calls deleted; frame-engine calls go through `persistence.Store`.
- All other files: `*pgxpool.Pool` parameters → `persistence.Store` (and `Queue` where used).

**Frame engine (`core/frame/`):**
- `engine.go`, `producer.go` — algorithmic logic preserved; pgx imports removed; `FrameDB` abstraction deleted; functions take `persistence.Store`; SQL extracted to `core/persistence/{postgres,sqlite}/frames.go`.

**Cmd binaries (`core/cmd/rimsky-{scheduler,supervisor,control-api,migrate}/main.go`):**
- `pgxpool.New(dsn)` → `persistence.Open(ctx, cfg.Persistence)`.
- `RIMSKY_DB_URL` env var read → `cfg.Persistence` from `RIMSKY_CONFIG`.
- `rimsky-migrate` calls `driver.Migrate(ctx)` then `driver.Close()`.
- All four binaries: read `RIMSKY_LOG_BINARY` env var, call `slog.SetDefault(slog.New(handler).With("binary", binaryName))` if set (for the unified-image log multiplex; benefits non-unified deployments too).

**Config & internal:**
- `core/config/scheduler.go` (and any peer files) — `StartScheduler`, `StartSupervisor`, `StartControlAPI` change signatures to accept `persistence.Driver` instead of `*pgxpool.Pool`.
- `core/internal/pgtest/pgtest.go` — exposes a `persistence.Driver` (Postgres-flavored) instead of `*pgxpool.Pool`.
- `core/scenario/harness.go` — same; takes a `persistence.Driver` for the test harness.

**Stores (out-of-process, separate concern):**
- `stores/postgres/store/store.go` — this is the *postgres-store-service* (a store impl, not rimsky's persistence). It legitimately uses pgx for its own state; unaffected by this spec. Verify the rule "no pgx outside `core/persistence/`" excludes the `stores/` tree (a separate Go module-level concern under the existing layout).

**Deploy & build:**
- `deploy/rimsky.yml` — gains a `persistence:` block.
- `deploy/docker-compose.yml` — no structural change; existing per-process containers keep working with the updated `rimsky.yml`.
- `deploy/build-images.sh` — adds `Dockerfile.all` to the build matrix.
- `Makefile` — adds `build-image-all` target; `test-conformance` target.
- `go.mod` — adds `modernc.org/sqlite`. Verify no transitive cgo dependencies.
- `.golangci.yml` — adds depguard or forbidigo rule denying `pgx` / `pgxpool` / `pgconn` imports outside `core/persistence/postgres/`, `core/cmd/`, and `stores/`.

### 10.5 CLAUDE.md updates

- **Package import rules section:** delete the `core/queue/`, `core/storage/`, `core/migrations/` lines; add a `core/persistence/` line per §2.10. Update the rules.md file's exclusion list correspondingly.
- **Blessed-invariants section:** any invariant whose source file moved gets a path-rename in its annotation. The invariants themselves are unchanged. Specific moves:
  - Inv 1 (`core/storage/postgres/nodes.go:5,296` → `core/persistence/postgres/nodes.go`).
  - Inv 2 (`core/queue/postgres/queue.go` → `core/persistence/postgres/queue.go`).
  - Inv 4 (`core/queue/postgres/queue.go` → `core/persistence/postgres/queue.go`; the `core/supervisor/runner.go` and `core/scheduler/scheduler.go` annotations stay where they are, content unchanged but the surrounding code is refactored).
  - Inv 7 (`core/scheduler/scheduler.go` annotation stays in place, but the lock-acquisition site changes from direct pg call to `coord.TrySchedulerTick`).
  - Inv 8 (`core/migrations/runner.go` → `core/persistence/migrations.go`).
  - Inv 9a — wording change per §3.10. The annotation lives in `core/store/interface.go:29` (file stays — `core/store/` is unaffected by this spec) and in `core/store/lockholders.go:9` (file deleted per §3.3 — the annotation goes with it). Update the surviving annotation text in `core/store/interface.go:29` to match the CLAUDE.md wording change ("Lock state lives only in the persistence layer").
  - Inv 10 (`core/supervisor/runner_acquire.go` annotation stays in place; surrounding code refactored).
  - Inv 13 (`core/supervisor/auto_terminal.go` annotation stays in place; raw SQL replaced by interface call).
  - Inv 14 — `core/store/conflict.go::RegionsByteEqual` is unaffected by this spec (the helper stays where it is).
  - Inv 19 — `Candidate.FrameID` doc-comment moves from `core/queue/interface.go` to `core/persistence/queue.go`.
- **Gotchas section:** add two entries:
  - "SQLite is the dev-only driver. Multi-process / multi-host SQLite is not supported. The startup banner and operator-guide say so; do not 'fix' the banner to be quieter."
  - "The unified image (`rimsky/all`) bundles the three runtime processes under a single PID-1 entrypoint (`rimsky-entrypoint`). Running it with replicas > 1 creates independent SQLite databases — broken. Use the per-process images for multi-replica deployments."

### 10.6 Documentation

- `docs/architecture.md` — replace the "core/queue/, core/storage/ — interfaces + Postgres impls" line with the new persistence package layout. Add a section describing the driver protocol and the SQLite-as-dev-only posture.
- `docs/operator-guide.md` — new "Persistence drivers" section: Postgres for prod, SQLite for dev, the `persistence:` block schema, the unified Docker image, the volume layout, the override paths.

---

## 11. Open questions

Real "implementation will settle" items the spec deliberately doesn't decide.

1. **One `interfaces.go` vs per-feature `types_*.go`.** §10.1 lists per-feature files; some implementers may prefer one consolidated file. The current `core/storage/interfaces.go` is one file (~514 lines) and works fine. Cold-read says ~500 lines max as a guideline; once the persistence types absorb the queue interface and the new `FrameStore`, one file would push past that. Implementer chooses based on actual line count after the move.

2. **`time.Time` round-tripping under `modernc.org/sqlite`.** §6.3 calls for `time.RFC3339Nano` text encoding. `modernc.org/sqlite` ships a built-in time-to-text converter when `database/sql` sees a `time.Time` value bound as a parameter — depending on its default format, the explicit `.UTC().Format(...)` may or may not be redundant. A small unit test that round-trips `time.Now().UTC()` through a TIMESTAMPTZ-equivalent column and asserts equality (down to nanoseconds) settles this on day one. Worst case: drop the explicit format and let the driver handle it.

3. **`UUID[]` and `TEXT[]` nullability semantics.** The SQLite JSON-array translation must match whatever distinction (empty-array vs NULL) existing call sites depend on. Verify by reading the Postgres impl and the call sites during step-1 implementation.

4. **`FrameStore` interface enumeration.** §3.5 says the `FrameStore` interface enumerates every frame-engine persistence operation, but the spec doesn't list them — the spec can't accurately cover ~660 lines of frame-engine code without the implementer reading it. Step-1 of the frame-engine refactor produces the canonical list and the spec is updated (or an addendum file in `docs/specs/notes/` captures it).

5. **Scope of `RIMSKY_LOG_BINARY`.** §7.3 has the entrypoint set this env var and the binary read it to add a structured slog field. Whether to also have the binaries set a default (e.g., the binary name is implicit in `os.Args[0]`) so the field is always populated, even outside the unified image, is an implementer choice. The unified image case is the load-bearing one; everything else is polish.

---

## 12. Out of scope

- Any change to the `rimsky-cli` / `rimsky-compose` `init` scaffold or its embedded `deploy/docker-compose.yml`. The `init` scaffold may benefit from defaulting to the unified image once this work lands; that change is a follow-up spec.
- Any persistence driver beyond SQLite. MySQL, distributed SQL engines, embedded KV stores, event-sourced flavors — all explicitly deferred. The driver protocol makes them additive.
- Web UI, observability extensions, auth, audit logging.
- The Helm chart at `deploy/kubernetes/rimsky-chart/` is documented-stale (env-var names lag behind the binaries). Adding `persistence:` config support to it is a small additive change but is deferred to the same follow-up that refreshes the stale env-vars.
- Changes to the existing single-purpose images (`rimsky/scheduler`, `rimsky/supervisor`, `rimsky/control-api`). They keep working unchanged.
- A SQLite-flavored richer "first-touch" scaffold that includes stub executors and stub stores in the same image. The unified image is orchestrator-only.
- Performance tuning of the SQLite driver (separate read pool, prepared-statement caching, etc.). v1 ships single-connection writes and revisits only if profiling shows a real problem.
- Refactoring the postgres store-service (`stores/postgres/store/store.go`) to use the rimsky persistence layer. That's a different module-boundary concern; the store-service is allowed to use pgx directly because it's the impl of a store, not a consumer of rimsky's persistence.
- `CHANGELOG.md` updates — those happen at implementation time per `.claude/rules/rules.md` step 7, not as a spec deliverable.

---

## 13. Summary of decisions

| # | Decision | Rationale |
|---|---|---|
| 1 | Pluggable persistence via a `Driver` interface aggregating `Queue()`, `Store()`, `Coordinator()` plus `Migrate(ctx)` and `Close()` | One umbrella keeps driver selection a single config decision; the existing per-feature `*Store` interface granularity is preserved |
| 2 | Two drivers in v1: Postgres (existing impl, lifted) and SQLite (new) | Postgres is prod; SQLite is the simplest possible second driver and unlocks the unified-image story |
| 3 | SQLite is dev-only, with a loud warn-level startup banner | "Multi-host SQLite" is not a supported deployment shape; defending against it in code buys nothing |
| 4 | SQLite Go library: `modernc.org/sqlite` (pure Go, no cgo) | Preserves the existing pure-static binary story; performance gap is irrelevant for a dev driver |
| 5 | Per-driver migration trees, no shared SQL IR | Postgres-specific features need hand-translation; SQLite tree starts as one consolidated init migration per pre-v1 rule |
| 6 | Coordinator under SQLite is `sync.Mutex` only — no table, no TTL; xact-locks are no-ops | Cross-process coordination isn't a SQLite-driver requirement; `BEGIN IMMEDIATE` writer hold subsumes per-name and per-region xact-locks |
| 7 | `Tx` is first-class in the persistence protocol; `Store.Transaction(ctx, fn)` brackets multi-call atomicity | Load-bearing for blessed invariants 10 and 13; SQLite Tx is `*sql.Tx` opened with `_txlock=immediate` |
| 8 | The pgx-direct duplicates `core/store/lockholders.go` and `core/attributes/store.go` are deleted; their methods merge into the proper interface impls | One implementation per concept; eliminates the dual-accessor confusion that exists today |
| 9 | The frame engine (`core/frame/`) is refactored to take `persistence.Store`; SQL moves into `core/persistence/{postgres,sqlite}/frames.go` | Without this, the SQLite driver is broken end-to-end the moment a frame tick fires |
| 10 | The supervisor, scheduler, controlapi all stop importing pgx; every `pgx.Tx` parameter becomes `persistence.Tx`; every `pool.BeginTx` becomes `store.Transaction` | The whole protocol means nothing if business logic still holds pgx directly |
| 11 | `WrapPgxTx` and `PgxTxFromStorage` escape hatches are deleted | After the supervisor/scheduler/controlapi refactor there are no callers; deleting prevents reintroduction |
| 12 | A golangci-lint rule (depguard or forbidigo) denies `pgx` imports outside `core/persistence/postgres/`, `core/cmd/`, `stores/` | Mechanical enforcement so the decoupling doesn't bit-rot |
| 13 | New `persistence:` block in `rimsky.yml`; mutually-exclusive `postgres:` / `sqlite:` sub-blocks; required-field-by-driver validation | Same posture as the existing `stores:` block; loud pre-flight failure on misconfig |
| 14 | Unified Docker image (`rimsky/all`) bundles the three runtime processes plus `rimsky-migrate` under a Go process supervisor (`rimsky-entrypoint`) | "docker run rimsky" onboarding path; the per-process images keep working for split deployments |
| 15 | The unified image defaults to SQLite + a volume-mounted DB file | Zero-config dev experience; operators override via `-v ./my-rimsky.yml:/etc/rimsky/rimsky.yml` for postgres |
| 16 | The entrypoint sets `RIMSKY_LOG_BINARY=…` per child; binaries add it as a structured slog field | Operators can grep stdout by binary in the multiplexed log stream |
| 17 | Driver-conformance test suite parameterized on a driver factory; runs against both drivers on every PR | Shared truth for the portable subset of behavior |
| 18 | `test/scenarios/` stays Postgres-only | Scenarios test deployment behaviors that don't apply to SQLite; SQLite serialization changes timing characteristics enough to make scenario passes meaningless |
| 19 | Existing single-purpose images and `deploy/docker-compose.yml` keep working unchanged with a one-line `rimsky.yml` update | Backwards-compat for split-deployment operators |
| 20 | The `init` scaffold (in the CLI/compose spec) is not modified by this spec | Avoids re-litigating an in-flight spec; follow-up may revise |
