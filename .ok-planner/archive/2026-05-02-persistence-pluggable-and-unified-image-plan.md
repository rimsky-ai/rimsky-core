# Pluggable Persistence + Unified Image Implementation Plan

**Goal:** Decouple rimsky from `pgx` end-to-end, ship Postgres + SQLite drivers behind a `core/persistence/` protocol, and bundle a unified Docker image (`rimsky/all`) that defaults to SQLite for dev.

**Architecture:** A `Driver` interface aggregates `Queue`, `Store`, and `Coordinator` sub-interfaces plus `Migrate(ctx)` and `Close()`. Two driver implementations — `core/persistence/postgres/` (lifts existing code) and `core/persistence/sqlite/` (new, dev-only, pure-Go via `modernc.org/sqlite`). All business-logic packages (`core/supervisor/`, `core/scheduler/`, `core/controlapi/`, `core/frame/`) drop direct pgx imports and call through the persistence protocol. The three runtime processes share state through the same `persistence.Driver`. A new `rimsky-entrypoint` Go binary multiplexes them inside one Docker image.

**Tech Stack:** Go 1.22+ (existing module), `jackc/pgx/v5` (Postgres driver-internal use), `modernc.org/sqlite` (SQLite, pure Go — no cgo), stdlib `database/sql`, stdlib `log/slog`, `gcr.io/distroless/static-debian12:nonroot` base image.

**Spec:** `docs/specs/2026-05-02-persistence-pluggable-and-unified-image-design.md`

---

## Pre-flight context for the implementer

Read these before starting:

- **The spec.** `docs/specs/2026-05-02-persistence-pluggable-and-unified-image-design.md`. Authoritative on every behavioral question. When the plan and the spec differ, the spec wins; flag the discrepancy and continue.
- **Project rules.** `.claude/rules/rules.md` and `.claude/rules/cold-read-cheatsheet.md`. Pre-v1 demolition is allowed (no migration paths, no compat shims, dev databases get nuked). Cold-read conventions apply to every new file.
- **Blessed invariants.** `CLAUDE.md` — invariants 1, 2, 3, 4, 4b, 5, 6, 7, 8, 9a, 9b, 10, 11, 12, 13, 14, 15, 19, 20. The persistence layer is load-bearing for most of these. Any task that touches an annotated site must preserve the invariant; the spec §3.10 enumerates exactly how.
- **Build & test commands.** Per `CLAUDE.md`:
  ```sh
  go build ./...
  go test ./...
  make lint
  go test ./test/scenarios/... ./core/storage/... -count=1   # testcontainers
  ```
- **Module path.** `github.com/rimsky-ai/rimsky-core`; `go.mod` is at the repo root.

The full spec sections most-cited by this plan: §2 (driver interface), §3 (pgx-decoupling refactor), §3.10 (invariant preservation), §4 (coordinator), §5 (migrations), §6 (SQLite specifics), §7 (unified image), §8 (config), §9 (tests).

---

## Strategy

The refactor is staged so that **every task ends with a runnable verification** (`go build ./...`, `go test ./...`, or a targeted subset). The strategy:

1. Land the new `core/persistence/` interfaces first. Nothing uses them yet; everything still compiles because the existing `core/queue/`, `core/storage/`, `core/migrations/` are untouched.
2. Land the Postgres driver under `core/persistence/postgres/` as a parallel implementation (lifted from existing code, with `pgx.Tx` → `persistence.Tx` in signatures). The old packages still exist; the new ones are unused.
3. Switch `rimsky-migrate` (smallest binary, no business logic) over to the new driver. Verify the existing scenario suite still passes.
4. Refactor the duplicate accessors (`core/store/lockholders.go`, `core/attributes/store.go`) into the proper interface impls. Update callers. Delete the duplicates.
5. Add per-name and per-region xact-lock methods to `Coordinator`. Lift `TakeNamedLockAdvisory` / `TakeRegionAdvisory` from the queue.
6. Define `FrameStore`. Refactor `core/frame/` to take `persistence.Store`. Lift the SQL into `core/persistence/postgres/frames.go`.
7. Refactor the supervisor, scheduler, controlapi to drop pgx imports. Each package becomes one task; verification is the existing test suite.
8. Switch the three runtime binaries to `persistence.Open`. Delete the escape hatches (`WrapPgxTx`, `PgxTxFromStorage`). Delete the old packages.
9. Add the SQLite driver. Run the conformance suite against both drivers.
10. Add the unified Docker image, the entrypoint binary, and the `RIMSKY_LOG_BINARY` plumbing.
11. Final docs / lint-rule updates.

The plan lands the SQLite driver only after the full pgx decoupling, so the conformance suite has a known-good Postgres reference at every step.

---

## File structure

### New files

```
core/persistence/
  driver.go                       # Driver, Coordinator interfaces
  queue.go                        # Queue interface (lifted from core/queue/interface.go, pgx.Tx → persistence.Tx)
  store.go                        # Store interface umbrella + per-feature interface declarations
  types.go                        # Config, PostgresConfig, SQLiteConfig, Tx, TxMarker
  open.go                         # Open(ctx, cfg) (Driver, error)
  migrations.go                   # driver-agnostic Migrator + Run
  migrations_test.go              # driver-parameterized
  conformance/
    conformance.go                # table-driven test suite parameterized on a driver factory
    conformance_test.go           # runs the suite against both drivers
  postgres/
    driver.go                     # postgres.Driver impl wrapping *pgxpool.Pool
    coordinator.go                # advisory locks (scheduler tick, migration, named, region)
    queue.go                      # lifted from core/queue/postgres/queue.go
    queue_test.go                 # lifted
    backend.go                    # lifted from core/storage/postgres/backend.go (escape hatches deleted)
    nodes.go                      # lifted
    instances.go                  # lifted
    templates.go                  # lifted
    template_tags.go              # lifted
    schedules.go                  # lifted
    supervisors.go                # lifted
    events.go                     # lifted
    node_attributes.go            # lifted (extended to absorb core/attributes/store.go)
    store_lifecycle.go            # lifted
    lock_holders.go               # lifted (extended to absorb core/store/lockholders.go)
    claim_holders.go              # lifted
    frames.go                     # NEW; SQL extracted from core/frame/{engine,producer}.go
    migrate.go                    # wires Migrator with Postgres callbacks
    integration_test.go           # driver-specific smoke
    migrations/
      001-initial.sql             # moved from core/migrations/
      002-frame-resolution.sql    # moved
      003-template-registry-and-lifecycle.sql   # moved
      embed.go                    # //go:embed *.sql
  sqlite/
    driver.go                     # sqlite.Driver impl wrapping *sql.DB
    coordinator.go                # sync.Mutex impls + xact-lock no-ops
    queue.go
    backend.go
    nodes.go
    instances.go
    templates.go
    template_tags.go
    schedules.go
    supervisors.go
    events.go
    node_attributes.go
    store_lifecycle.go
    lock_holders.go
    claim_holders.go
    frames.go
    migrate.go
    integration_test.go
    migrations/
      001-initial.sql             # hand-written SQLite-dialect schema
      embed.go

core/cmd/rimsky-entrypoint/
  main.go                         # process supervisor for the unified image
  main_test.go                    # signal forwarding, migrate-then-spawn, child-crash propagation

deploy/
  Dockerfile.all                  # unified image
  rimsky-all.yml                  # default rimsky.yml baked into the unified image

test/smoke/all/
  smoke_test.go                   # gated by //go:build smoke
```

### Moved files

```
core/queue/postgres/queue.go        → core/persistence/postgres/queue.go
core/queue/postgres/queue_test.go   → core/persistence/postgres/queue_test.go
core/storage/postgres/*.go          → core/persistence/postgres/*.go
core/storage/postgres/postgres_test.go → core/persistence/postgres/postgres_test.go
core/migrations/001-initial.sql                        → core/persistence/postgres/migrations/001-initial.sql
core/migrations/002-frame-resolution.sql               → core/persistence/postgres/migrations/002-frame-resolution.sql
core/migrations/003-template-registry-and-lifecycle.sql → core/persistence/postgres/migrations/003-template-registry-and-lifecycle.sql
core/migrations/embed.go            → core/persistence/postgres/migrations/embed.go (regenerated for new package)
core/migrations/runner.go           → core/persistence/migrations.go (refactored to driver-agnostic)
core/migrations/runner_test.go      → core/persistence/migrations_test.go
core/queue/interface.go content     → core/persistence/queue.go (with pgx.Tx → persistence.Tx)
core/storage/interfaces.go content  → core/persistence/store.go + core/persistence/types.go
```

### Deleted files / directories

```
core/migrations/                    # entire directory
core/queue/                         # entire directory (interface moved)
core/queue/postgres/                # entire directory
core/storage/                       # entire directory (interface moved)
core/storage/postgres/              # entire directory
core/store/lockholders.go           # merged into core/persistence/postgres/lock_holders.go
core/attributes/store.go            # merged into core/persistence/postgres/node_attributes.go
```

`WrapPgxTx` and `PgxTxFromStorage` (currently in `core/storage/postgres/backend.go`) are deleted as part of the lift; they don't appear in `core/persistence/postgres/backend.go`.

### Refactored in place

```
core/supervisor/{runner,runner_acquire,runner_dispatch,runner_held_claims,runner_locks,runner_terminal,auto_terminal,callback,supervisor,on_error,terminal_outcome}.go
core/scheduler/{scheduler,sweep_locks,invalidate,pure_cascade,recalculate,schedule_ticker}.go
core/controlapi/{instances,nodes,...}.go    # all files importing pgx
core/frame/{engine,producer,types}.go
core/cmd/rimsky-{scheduler,supervisor,control-api,migrate}/main.go
core/config/scheduler.go
core/internal/pgtest/pgtest.go
core/scenario/harness.go
test/smoke/setup.go
```

### Changed (small edits)

```
deploy/rimsky.yml                   # add `persistence:` block
deploy/build-images.sh              # add Dockerfile.all
Makefile                            # add build-image-all and test-conformance targets
go.mod                              # add modernc.org/sqlite
.golangci.yml                       # depguard rule denying pgx outside core/persistence/postgres/, core/cmd/, stores/
CLAUDE.md                           # package import rules, invariant annotation paths, gotchas
docs/architecture.md                # persistence package layout, SQLite-as-dev-only posture
docs/operator-guide.md              # new "Persistence drivers" section + unified image docs
.claude/rules/rules.md              # search-scoping update if needed
```

---

## Tasks

### Task 1 — Persistence package skeleton (`types.go`)

**Goal:** Lay down the package and the smallest building-block types with no dependencies on anything else.

**Files:** `core/persistence/types.go` (new)

1. Create the package directory: `mkdir -p core/persistence`.
2. Create `core/persistence/types.go`:

   ```go
   // Package persistence is the runtime-state-storage protocol for rimsky.
   // The Driver interface (driver.go) aggregates Queue, Store, and Coordinator
   // sub-interfaces. Two impls live under postgres/ and sqlite/.
   //
   // Spec: docs/specs/2026-05-02-persistence-pluggable-and-unified-image-design.md
   package persistence

   import "time"

   // Config is the operator-supplied driver selection + parameters. Loaded
   // from the `persistence:` block in rimsky.yml. Validation rules per spec §8.2:
   //   - Driver in {"postgres","sqlite"}.
   //   - Exactly one of Postgres / SQLite is non-nil; mutual exclusion is enforced
   //     at the loader and re-checked here in Open.
   type Config struct {
       Driver   string
       Postgres *PostgresConfig
       SQLite   *SQLiteConfig
   }

   type PostgresConfig struct {
       DSN             string
       MaxOpenConns    int
       MaxIdleConns    int
       ConnMaxLifetime time.Duration
   }

   type SQLiteConfig struct {
       Path string // absolute; relative paths rejected at the loader
   }

   // Tx is the transaction handle threaded through Queue and per-feature
   // *Store methods. Driver-implemented; opaque to callers. Concrete carriers
   // embed TxMarker so they satisfy Tx without being forgeable from outside
   // the persistence package tree.
   type Tx interface{ isTx() }

   // TxMarker is the zero-cost embed driver impls use to satisfy Tx.
   type TxMarker struct{}

   func (TxMarker) isTx() {}
   ```

3. Verify: `go build ./core/persistence/...`

**Verification:** Build passes.

---

### Task 2 — Persistence Coordinator interface (`driver.go`)

**Goal:** Define the Driver and Coordinator interfaces. Both surface only what every driver must implement; sub-interfaces (Queue, Store) are filled in by later tasks.

**Files:** `core/persistence/driver.go` (new)

1. Create `core/persistence/driver.go`:

   ```go
   package persistence

   import "context"

   // Driver is the umbrella over the rimsky persistence layer. One Driver
   // is constructed per process via Open(); the three runtime processes hold
   // it for their lifetime and Close() it on shutdown.
   //
   // Implementations live under postgres/ and sqlite/. No code outside this
   // package tree may depend on driver-specific libraries (pgx, modernc).
   type Driver interface {
       Queue() Queue
       Store() Store
       Coordinator() Coordinator
       Migrate(ctx context.Context) error
       Close() error
   }

   // Coordinator carries the cross-process synchronization primitives the
   // scheduler, migration runner, and supervisor's acquisition tx depend on.
   //
   // Postgres impl: pg_(try_)advisory_lock and pg_advisory_xact_lock.
   // SQLite impl: sync.Mutex for the cross-process methods and no-ops for the
   // xact-lock methods (the surrounding BEGIN IMMEDIATE writer hold subsumes
   // them — strictly stronger than per-name advisory locking).
   //
   // Per spec §4 and §3.10. Load-bearing for blessed invariants 3, 4b, 7, 8, 10.
   type Coordinator interface {
       // TrySchedulerTick: returns held=true plus a release fn if the
       // scheduler-tick exclusion was acquired; held=false and a nil release
       // fn if another replica already holds it. The scheduler skips the
       // tick when held=false. Inv 7.
       TrySchedulerTick(ctx context.Context) (held bool, release func(), err error)

       // AcquireMigrationLock blocks until the migration exclusion is held.
       // The release fn must be safe to call even after the parent ctx is
       // cancelled (Postgres impl uses context.Background() internally for
       // the unlock; SQLite impl is a plain mu.Unlock). Inv 8.
       AcquireMigrationLock(ctx context.Context) (release func() error, err error)

       // TakeNamedLockInTx acquires the per-named-lock advisory exclusion
       // inside the supplied tx. Released automatically at tx end. Callers
       // MUST take locks in the deterministic sort order from v3 spec §4.10
       // invariant 3 (named-lock names sorted lexically before region locks
       // sorted by store-name then by region-data bytes). Inv 3, 10.
       //
       // Postgres: pg_advisory_xact_lock(hashtext('rimsky_lock:'+name)).
       // SQLite: no-op (writer slot already held).
       TakeNamedLockInTx(ctx context.Context, tx Tx, name string) error

       // TakeRegionLockInTx: same pattern, scoped to (storeName, regionData).
       // Inv 3, 4b, 10.
       //
       // Postgres: pg_advisory_xact_lock(hashtext('rimsky_region:'+store+':'+hex(region))).
       // SQLite: no-op.
       TakeRegionLockInTx(ctx context.Context, tx Tx, storeName string, regionData []byte) error
   }
   ```

2. Verify: `go build ./core/persistence/...`

**Verification:** Build passes. The `Queue` and `Store` types referenced in `Driver` don't exist yet → expected compile error pointing at `driver.go`. (We'll add them in tasks 3 and 4.)

---

### Task 3 — Persistence Queue interface (`queue.go`)

**Goal:** Lift `core/queue/interface.go` into `core/persistence/queue.go`, replacing `pgx.Tx` parameters with `persistence.Tx`.

**Files:** `core/persistence/queue.go` (new)

1. Read `core/queue/interface.go` to understand the existing surface (the package doc comment is dense and load-bearing — keep its substance).
2. Create `core/persistence/queue.go` mirroring the existing interface but with two changes:
   - Package is `persistence`, not `queue`.
   - `pgx.Tx` parameters in `SelectCandidates` and `ClaimDispatchRow` become `Tx` (i.e., `persistence.Tx`).
3. Move the supporting types (`DispatchRequest`, `SelectCandidatesRequest`, `Candidate`, `ClaimOwnership`) into the same file; keep the field doc comments verbatim.
4. The `core/queue/interface.go` file is **not** deleted in this task — it stays as a parallel declaration that nothing uses yet. (Deletion happens in Task 26 once nothing imports it.)
5. Verify: `go build ./core/persistence/...`

**Verification:** Build passes. The `Queue` symbol now exists; the `Driver.Queue()` method in `driver.go` resolves.

---

### Task 4 — Persistence Store interface + per-feature types (`store.go`)

**Goal:** Lift the contents of `core/storage/interfaces.go` into `core/persistence/store.go`, renaming `StorageBackend` → `Store` and adding the new `FrameStore` placeholder. Per spec §11 open question 1, decide between one consolidated file and per-feature files based on actual line count after the move.

**Files:** `core/persistence/store.go` (new). If line count exceeds ~600, split into per-feature `types_*.go` files (one per `*Store` interface) — implementer's call.

1. Read `core/storage/interfaces.go` (~514 lines).
2. Create `core/persistence/store.go` (or split files), preserving every existing type:
   - The 11 per-feature interfaces: `TemplateStore`, `TemplateTagsStore`, `InstanceStore`, `StoreLifecycleStore`, `NodeStore`, `LockHoldersStore`, `NodeAttributesStore`, `ClaimHoldersStore`, `EventStore`, `ScheduleStore`, `SupervisorStore`.
   - Their `*Row` and `*Input` types, the enums (`LockKind`, `ClaimHolderState`, `TemplateState`, `StoreLifecycleScopeKind`, `StoreLifecycleState`).
   - The `Store` umbrella (renamed from `StorageBackend`).
3. Add a placeholder `FrameStore` interface — `type FrameStore interface {}` — to be filled in during Task 24:

   ```go
   // FrameStore is the persistence surface the frame engine (core/frame)
   // talks to. The method set is enumerated during the frame-engine refactor
   // (Task 24) — defining all of it up-front would require porting code
   // ahead of the refactor. Until then it's empty; callers cannot use it.
   //
   // Per spec §3.5.
   type FrameStore interface{}
   ```

4. Add `Frames() FrameStore` to the `Store` interface umbrella:

   ```go
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
       Frames() FrameStore
       Transaction(ctx context.Context, fn func(ctx context.Context, tx Tx) error) error
   }
   ```

5. Reuse the `Tx` and `TxMarker` types defined in Task 1 (`types.go`) — do not redeclare them. Existing code in `core/storage/interfaces.go` defines its own `Tx` and `TxMarker`; the new file uses the persistence-package versions.

6. The `core/storage/interfaces.go` file is not deleted yet (other code still imports `core/storage`).

7. Verify: `go build ./core/persistence/...`

**Verification:** Build passes. The full `Driver` interface in `driver.go` now resolves.

---

### Task 5 — Persistence Open dispatcher (`open.go`)

**Goal:** Add the `Open` constructor with a switch on `cfg.Driver`. Both branches return "driver not yet implemented" errors until Tasks 7 and 30 land.

**Files:** `core/persistence/open.go` (new)

1. Create `core/persistence/open.go`:

   ```go
   package persistence

   import (
       "context"
       "errors"
       "fmt"
   )

   // Open constructs a Driver for the given config. Validates mutual
   // exclusion of postgres / sqlite sub-blocks (spec §8.2) and dispatches to
   // the per-driver constructor. Constructors live in postgres/ and sqlite/
   // and are wired in via the tagged build helpers in their package init.
   func Open(ctx context.Context, cfg Config) (Driver, error) {
       if err := cfg.validate(); err != nil {
           return nil, fmt.Errorf("persistence: invalid config: %w", err)
       }
       switch cfg.Driver {
       case "postgres":
           return openPostgres(ctx, *cfg.Postgres)
       case "sqlite":
           return openSQLite(ctx, *cfg.SQLite)
       default:
           return nil, fmt.Errorf("persistence: unknown driver %q", cfg.Driver)
       }
   }

   func (c Config) validate() error {
       switch c.Driver {
       case "postgres":
           if c.Postgres == nil {
               return errors.New("driver=postgres requires postgres: block")
           }
           if c.SQLite != nil {
               return errors.New("driver=postgres but sqlite: block also present (mutually exclusive)")
           }
           if c.Postgres.DSN == "" {
               return errors.New("postgres.dsn is required")
           }
       case "sqlite":
           if c.SQLite == nil {
               return errors.New("driver=sqlite requires sqlite: block")
           }
           if c.Postgres != nil {
               return errors.New("driver=sqlite but postgres: block also present (mutually exclusive)")
           }
           if c.SQLite.Path == "" {
               return errors.New("sqlite.path is required")
           }
       case "":
           return errors.New("persistence.driver is required")
       default:
           return fmt.Errorf("unknown driver %q (want postgres or sqlite)", c.Driver)
       }
       return nil
   }

   // openPostgres and openSQLite are defined as package-private vars so the
   // postgres/ and sqlite/ subpackages can install them via init(). This
   // avoids open.go importing postgres/ (which imports persistence/) and
   // creating an import cycle.
   var (
       openPostgres func(ctx context.Context, cfg PostgresConfig) (Driver, error) = stubOpenPostgres
       openSQLite   func(ctx context.Context, cfg SQLiteConfig) (Driver, error)   = stubOpenSQLite
   )

   func stubOpenPostgres(context.Context, PostgresConfig) (Driver, error) {
       return nil, errors.New("postgres driver not yet wired")
   }
   func stubOpenSQLite(context.Context, SQLiteConfig) (Driver, error) {
       return nil, errors.New("sqlite driver not yet wired")
   }
   ```

2. Add a small sanity test `core/persistence/open_test.go`:

   ```go
   package persistence

   import (
       "context"
       "testing"
   )

   func TestOpenValidation(t *testing.T) {
       cases := []struct {
           name    string
           cfg     Config
           wantErr string
       }{
           {"empty", Config{}, "driver is required"},
           {"unknown", Config{Driver: "mysql"}, "unknown driver"},
           {"postgres-no-block", Config{Driver: "postgres"}, "requires postgres: block"},
           {"postgres-no-dsn", Config{Driver: "postgres", Postgres: &PostgresConfig{}}, "dsn is required"},
           {"postgres-with-sqlite", Config{Driver: "postgres", Postgres: &PostgresConfig{DSN: "x"}, SQLite: &SQLiteConfig{}}, "mutually exclusive"},
           {"sqlite-no-path", Config{Driver: "sqlite", SQLite: &SQLiteConfig{}}, "path is required"},
       }
       for _, tc := range cases {
           t.Run(tc.name, func(t *testing.T) {
               _, err := Open(context.Background(), tc.cfg)
               if err == nil || !contains(err.Error(), tc.wantErr) {
                   t.Fatalf("want %q, got %v", tc.wantErr, err)
               }
           })
       }
   }

   func contains(s, sub string) bool {
       for i := 0; i+len(sub) <= len(s); i++ {
           if s[i:i+len(sub)] == sub { return true }
       }
       return false
   }
   ```

3. Verify: `go test ./core/persistence/...`

**Verification:** All validation tests pass.

---

### Task 6 — Driver-agnostic migration runner (`migrations.go`)

**Goal:** Lift `core/migrations/runner.go` into `core/persistence/migrations.go`, refactored to take a driver-supplied callback set instead of pgx directly.

**Files:** `core/persistence/migrations.go` (new), `core/persistence/migrations_test.go` (new — driver-parameterized; will be expanded in conformance tasks)

1. Read `core/migrations/runner.go` to understand the existing flow (acquire advisory lock, ensure tracker table, iterate `*.sql`, exec + record per file).
2. Create `core/persistence/migrations.go`:

   ```go
   package persistence

   import (
       "context"
       "embed"
       "fmt"
       "io/fs"
       "sort"
       "strings"

       "github.com/rimsky-ai/rimsky-core/core/shared"
   )

   // Migrator runs *.sql files in filename-sorted order under the
   // coordinator's migration lock. Each driver supplies its own filesystem,
   // exec function, has-applied query, and record-applied mutator.
   //
   // The lock is held for the full pass via Coordinator.AcquireMigrationLock
   // (Postgres: session-level pg_advisory_lock on a dedicated conn; SQLite:
   // sync.Mutex). The release fn must run even if ctx is cancelled — both
   // driver impls honor this.
   //
   // Note (Postgres specific): the lock is held on a dedicated connection
   // separate from the conn used by Exec / QueryHas / RecordRun (which the
   // driver acquires from the pool). This is a behavior change from the
   // pre-refactor runner, which held the lock on the same conn it ran SQL
   // on. Cross-process serialization is preserved because the advisory lock
   // is session-scoped and the lock conn lives for the migration's
   // duration. See spec §4.1.
   type Migrator struct {
       FS        embed.FS
       QueryHas  func(ctx context.Context, filename string) (bool, error)
       Bootstrap func(ctx context.Context) error // ensures rimsky_migrations exists
       // ApplyOne runs the migration SQL and records it in rimsky_migrations
       // inside a single driver-internal transaction. Per-file atomicity is
       // load-bearing — a partially-applied migration with no
       // rimsky_migrations row would re-run on the next pass and likely
       // crash on duplicate-table errors. Each driver implements this
       // with its own tx primitive (Postgres: pool.Begin; SQLite: db.BeginTx).
       ApplyOne  func(ctx context.Context, sql string, filename string) error
   }

   func (m Migrator) Run(ctx context.Context, coord Coordinator, log shared.Logger) error {
       release, err := coord.AcquireMigrationLock(ctx)
       if err != nil {
           return fmt.Errorf("persistence.Migrator: acquire lock: %w", err)
       }
       defer func() { _ = release() }()

       if m.Bootstrap != nil {
           if err := m.Bootstrap(ctx); err != nil {
               return fmt.Errorf("persistence.Migrator: bootstrap: %w", err)
           }
       }

       entries, err := fs.ReadDir(m.FS, ".")
       if err != nil {
           return fmt.Errorf("persistence.Migrator: read embed fs: %w", err)
       }
       var files []string
       for _, e := range entries {
           if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
               files = append(files, e.Name())
           }
       }
       sort.Strings(files)

       applied := 0
       for _, filename := range files {
           has, err := m.QueryHas(ctx, filename)
           if err != nil {
               return fmt.Errorf("persistence.Migrator: check %s: %w", filename, err)
           }
           if has {
               continue
           }
           sqlBytes, err := fs.ReadFile(m.FS, filename)
           if err != nil {
               return fmt.Errorf("persistence.Migrator: read %s: %w", filename, err)
           }
           if err := m.ApplyOne(ctx, string(sqlBytes), filename); err != nil {
               return fmt.Errorf("persistence.Migrator: apply %s: %w", filename, err)
           }
           if log != nil {
               log.Info("migration applied", "filename", filename)
           }
           applied++
       }
       if log != nil {
           if applied == 0 {
               log.Info("no migrations to apply")
           } else {
               log.Info("migrations complete", "applied", applied)
           }
       }
       return nil
   }
   ```

3. Create an empty `core/persistence/migrations_test.go` with a placeholder `TestMigratorRunsInOrder` (will be filled in via the conformance suite in Task 32). For now just `package persistence`.
4. Verify: `go build ./core/persistence/...`

**Verification:** Build passes.

---

### Task 7 — Postgres driver skeleton (`postgres/driver.go`)

**Goal:** Lay down the `core/persistence/postgres/` package, the `Driver` impl that wraps `*pgxpool.Pool`, and the package init that registers `openPostgres`.

**Files:** `core/persistence/postgres/driver.go` (new)

1. Create `core/persistence/postgres/driver.go`:

   ```go
   // Package postgres is the Postgres-backed persistence.Driver. Lifted from
   // the previous core/queue/postgres + core/storage/postgres + core/migrations
   // packages and refactored to use persistence.Tx instead of pgx.Tx in
   // every interface signature.
   //
   // This package is the only place outside core/cmd/ permitted to import
   // pgx (per spec §2.10). A golangci-lint depguard rule enforces this
   // (Task 28).
   package postgres

   import (
       "context"
       "errors"
       "fmt"

       "github.com/jackc/pgx/v5/pgxpool"

       "github.com/rimsky-ai/rimsky-core/core/persistence"
   )

   // driver is the persistence.Driver impl. Constructed via persistence.Open
   // with cfg.Driver == "postgres". The accessor methods return nil until
   // the per-area impls land in Tasks 8–11; this Task only wires the
   // package skeleton and registers the constructor.
   type driver struct {
       pool *pgxpool.Pool
       // q, s, c fields will be added by Tasks 11, 10, 8 respectively as
       // each impl lands. Don't add them now.
   }

   func (d *driver) Queue() persistence.Queue             { return nil }
   func (d *driver) Store() persistence.Store             { return nil }
   func (d *driver) Coordinator() persistence.Coordinator { return nil }
   func (d *driver) Close() error                          { d.pool.Close(); return nil }

   // Migrate is wired in Task 12; Task 7 returns a placeholder error.
   func (d *driver) Migrate(ctx context.Context) error {
       return errors.New("postgres driver not yet wired (migrate)")
   }

   func init() {
       persistence.RegisterPostgres(open)
   }

   func open(ctx context.Context, cfg persistence.PostgresConfig) (persistence.Driver, error) {
       pcfg, err := pgxpool.ParseConfig(cfg.DSN)
       if err != nil {
           return nil, fmt.Errorf("postgres: parse dsn: %w", err)
       }
       if cfg.MaxOpenConns > 0 {
           pcfg.MaxConns = int32(cfg.MaxOpenConns)
       }
       if cfg.MaxIdleConns > 0 {
           pcfg.MinConns = int32(cfg.MaxIdleConns)
       }
       if cfg.ConnMaxLifetime > 0 {
           pcfg.MaxConnLifetime = cfg.ConnMaxLifetime
       }
       pool, err := pgxpool.NewWithConfig(ctx, pcfg)
       if err != nil {
           return nil, fmt.Errorf("postgres: create pool: %w", err)
       }
       return &driver{pool: pool}, nil
   }
   ```

2. Add a `RegisterPostgres` helper to `core/persistence/open.go` (so `postgres/driver.go`'s `init()` can wire itself in without an import cycle):

   ```go
   // RegisterPostgres / RegisterSQLite are called from each driver's init()
   // to install the constructor. Tests may also call these to install fakes.
   func RegisterPostgres(fn func(context.Context, PostgresConfig) (Driver, error)) {
       openPostgres = fn
   }
   func RegisterSQLite(fn func(context.Context, SQLiteConfig) (Driver, error)) {
       openSQLite = fn
   }
   ```

3. The accessor methods on `*driver` return **nil** for now. `nil` is a valid value for an interface type in Go; the build compiles, and runtime calls like `driver.Queue().Enqueue(...)` would NPE — but no caller invokes them yet. Tasks 8–11 each fill in one impl, switch the corresponding accessor to return the impl pointer, and add the field to `driver`.

   Concretely, Task 7's `driver` struct has only `pool *pgxpool.Pool`; the four interface accessors (`Queue`, `Store`, `Coordinator`, `Migrate`) return `nil`, `nil`, `nil`, and `errors.New("postgres driver not yet wired")` respectively. `Close()` calls `pool.Close()`. The `open` function constructs `&driver{pool: pool}`.

4. Verify: `go build ./core/persistence/...`

**Verification:** Build passes. The driver registers itself via `init()` and `persistence.Open(... driver: postgres ...)` returns a `*driver` whose accessors are nil-returning placeholders.

---

### Task 8 — Postgres coordinator (`postgres/coordinator.go`)

**Goal:** Implement the four `Coordinator` methods against pgx, consolidating the existing scheduler-tick advisory lock and migration advisory lock plus adding the per-name and per-region xact-locks (lifted from `core/queue/postgres/queue.go::TakeNamedLockAdvisory` and `::TakeRegionAdvisory`).

**Files:** `core/persistence/postgres/coordinator.go` (new)

1. Read the existing call sites:
   - `core/scheduler/scheduler.go:61` (constant `RimskySchedulerTickLockKey`)
   - `core/migrations/runner.go:18` (constant `advisoryLockKey`)
   - `core/queue/postgres/queue.go::TakeNamedLockAdvisory` and `::TakeRegionAdvisory` (the existing helpers, called from `core/supervisor/runner_acquire.go`)

2. Create `core/persistence/postgres/coordinator.go`:

   ```go
   package postgres

   import (
       "context"
       "encoding/hex"
       "fmt"

       "github.com/jackc/pgx/v5"
       "github.com/jackc/pgx/v5/pgxpool"

       "github.com/rimsky-ai/rimsky-core/core/persistence"
   )

   // Constants moved here from core/scheduler/scheduler.go and
   // core/migrations/runner.go. Never reuse these int64s elsewhere.
   const (
       RimskySchedulerTickLockKey int64 = ... // same value as before; copy verbatim
       advisoryMigrationLockKey   int64 = ... // same value as before; copy verbatim
   )

   type coordinatorImpl struct {
       pool *pgxpool.Pool
   }

   func newCoordinator(pool *pgxpool.Pool) *coordinatorImpl {
       return &coordinatorImpl{pool: pool}
   }

   // TrySchedulerTick — @blessed-invariant 7
   func (c *coordinatorImpl) TrySchedulerTick(ctx context.Context) (bool, func(), error) {
       conn, err := c.pool.Acquire(ctx)
       if err != nil {
           return false, nil, fmt.Errorf("postgres.TrySchedulerTick: acquire: %w", err)
       }
       var got bool
       if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", RimskySchedulerTickLockKey).Scan(&got); err != nil {
           conn.Release()
           return false, nil, fmt.Errorf("postgres.TrySchedulerTick: try lock: %w", err)
       }
       if !got {
           conn.Release()
           return false, nil, nil
       }
       release := func() {
           // context.Background so a cancelled parent ctx doesn't strand the lock.
           _, _ = conn.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", RimskySchedulerTickLockKey)
           conn.Release()
       }
       return true, release, nil
   }

   // AcquireMigrationLock — @blessed-invariant 8
   //
   // Note: holds the lock on a dedicated conn separate from the conn used by
   // exec/queryHas/recordRun (per spec §4.1 connection-split note). This is
   // a behavior change from the pre-refactor runner; cross-process
   // serialization is preserved because the advisory lock is session-scoped
   // and this conn lives for the migration's duration.
   func (c *coordinatorImpl) AcquireMigrationLock(ctx context.Context) (func() error, error) {
       conn, err := c.pool.Acquire(ctx)
       if err != nil {
           return nil, fmt.Errorf("postgres.AcquireMigrationLock: acquire: %w", err)
       }
       if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", advisoryMigrationLockKey); err != nil {
           conn.Release()
           return nil, fmt.Errorf("postgres.AcquireMigrationLock: lock: %w", err)
       }
       return func() error {
           _, _ = conn.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", advisoryMigrationLockKey)
           conn.Release()
           return nil
       }, nil
   }

   // TakeNamedLockInTx — @blessed-invariant 3, 10
   func (c *coordinatorImpl) TakeNamedLockInTx(ctx context.Context, tx persistence.Tx, name string) error {
       pgT, err := unwrapTx(tx)
       if err != nil {
           return fmt.Errorf("postgres.TakeNamedLockInTx: %w", err)
       }
       _, err = pgT.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtext($1))", "rimsky_lock:"+name)
       return err
   }

   // TakeRegionLockInTx — @blessed-invariant 3, 4b, 10
   func (c *coordinatorImpl) TakeRegionLockInTx(ctx context.Context, tx persistence.Tx, storeName string, regionData []byte) error {
       pgT, err := unwrapTx(tx)
       if err != nil {
           return fmt.Errorf("postgres.TakeRegionLockInTx: %w", err)
       }
       key := "rimsky_region:" + storeName + ":" + hex.EncodeToString(regionData)
       _, err = pgT.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtext($1))", key)
       return err
   }

   // unwrapTx asserts that tx was issued by this driver and returns the
   // underlying pgx.Tx. Defined in backend.go (Task 9).
   var unwrapTx func(persistence.Tx) (pgx.Tx, error)
   ```

3. Set `unwrapTx` in `backend.go` (Task 9 will own the actual definition; for this task, declare it as a package-level var and leave the assignment to Task 9 — until then, calls to `unwrapTx` will panic at runtime, but the build compiles).

4. Update `core/persistence/postgres/driver.go`:
   - Add `c *coordinatorImpl` field to `driver` struct.
   - Set `d.c = newCoordinator(pool)` at the end of `open`.
   - Change `Coordinator()` accessor to return `d.c` instead of `nil`.

5. Verify: `go build ./core/persistence/postgres/...`

**Verification:** Build passes. The `Coordinator` interface is now satisfied by `*coordinatorImpl`; `driver.Coordinator()` returns it.

---

### Task 9 — Postgres backend & Tx carrier (`postgres/backend.go`)

**Goal:** Lift `core/storage/postgres/backend.go` into `core/persistence/postgres/backend.go` with the escape hatches (`WrapPgxTx`, `PgxTxFromStorage`) deleted and the `unwrapTx` helper exposed for `coordinator.go`.

**Files:** `core/persistence/postgres/backend.go` (new)

1. Read `core/storage/postgres/backend.go` to understand the existing `pgTx` carrier and the `q()` querier helper.
2. Create `core/persistence/postgres/backend.go`:

   ```go
   package postgres

   import (
       "context"
       "errors"
       "fmt"

       "github.com/jackc/pgx/v5"
       "github.com/jackc/pgx/v5/pgconn"
       "github.com/jackc/pgx/v5/pgxpool"

       "github.com/rimsky-ai/rimsky-core/core/persistence"
   )

   // pgTx is the persistence.Tx carrier for this driver. Embeds
   // persistence.TxMarker so it satisfies the interface; the persistence
   // package's Tx is the only Tx callers see.
   type pgTx struct {
       persistence.TxMarker
       tx pgx.Tx
   }

   // unwrapTx asserts that tx was issued by this driver and returns the
   // underlying pgx.Tx.
   func init() {
       unwrapTx = func(tx persistence.Tx) (pgx.Tx, error) {
           if tx == nil {
               return nil, errors.New("nil persistence.Tx")
           }
           t, ok := tx.(*pgTx)
           if !ok {
               return nil, fmt.Errorf("persistence.Tx is not a postgres tx: %T", tx)
           }
           return t.tx, nil
       }
   }

   // storeImpl is the persistence.Store impl. The per-feature *Store methods
   // return the same impl pointer downcast to its narrow interface — each
   // file (nodes.go, instances.go, ...) defines the methods on storeImpl.
   //
   // A single storeImpl carries the pool. Per-feature impls are lightweight
   // wrappers; we keep them as aspect-typed subsets so the *Store accessors
   // type-check.
   type storeImpl struct {
       pool *pgxpool.Pool
   }

   func newStore(pool *pgxpool.Pool) *storeImpl { return &storeImpl{pool: pool} }

   // Per-feature accessors are added in Task 10 once each per-feature impl
   // satisfies its interface. They look like:
   //   func (s *storeImpl) Templates() persistence.TemplateStore { return (*templatesImpl)(s) }
   // ... one per *Store sub-interface. Don't add them here in Task 9 —
   // the aspect types don't satisfy the interfaces yet, so the build
   // would fail.

   // Transaction begins a tx, runs fn, and commits/rolls back. If fn returns
   // an error or panics, the tx rolls back. The tx passed to fn is a *pgTx
   // wrapped in persistence.Tx; unwrap via the package-private unwrapTx.
   func (s *storeImpl) Transaction(ctx context.Context, fn func(ctx context.Context, tx persistence.Tx) error) error {
       pgT, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
       if err != nil {
           return fmt.Errorf("postgres.Transaction: begin: %w", err)
       }
       defer func() {
           if p := recover(); p != nil {
               _ = pgT.Rollback(ctx)
               panic(p)
           }
       }()
       if err := fn(ctx, &pgTx{tx: pgT}); err != nil {
           _ = pgT.Rollback(ctx)
           return err
       }
       return pgT.Commit(ctx)
   }

   // querier is the common surface shared by *pgxpool.Pool and pgx.Tx.
   type querier interface {
       Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
       Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
       QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
   }

   // q returns a querier — pool when tx is nil, otherwise the tx carrier.
   // Per-feature impls call this at the top of every method.
   func (s *storeImpl) q(tx persistence.Tx) querier {
       if tx == nil {
           return s.pool
       }
       t, ok := tx.(*pgTx)
       if !ok {
           // This is a programmer error; a Tx that isn't a *pgTx came from
           // another driver. Panic so the misuse surfaces immediately.
           panic(fmt.Sprintf("postgres.q: persistence.Tx is not a postgres tx: %T", tx))
       }
       return t.tx
   }

   // Per-feature aspect types — empty wrappers so each *Store has a distinct
   // method set. Defined here so other files can attach methods.
   type (
       templatesImpl       storeImpl
       templateTagsImpl    storeImpl
       instancesImpl       storeImpl
       storeLifecycleImpl  storeImpl
       nodesImpl           storeImpl
       lockHoldersImpl     storeImpl
       nodeAttributesImpl  storeImpl
       claimHoldersImpl    storeImpl
       eventsImpl          storeImpl
       schedulesImpl       storeImpl
       supervisorsImpl     storeImpl
       framesImpl          storeImpl
   )
   ```

3. Update `core/persistence/postgres/driver.go`:
   - Add `s *storeImpl` field to `driver`.
   - Set `d.s = newStore(pool)` at the end of `open`.
   - Leave `Store()` returning `nil` for now (Task 10 will switch it to `return d.s` once the per-feature impls satisfy the per-feature interfaces). This keeps the build green during Task 9.

4. Verify: `go build ./core/persistence/postgres/...`

**Verification:** Build passes. The aspect types exist but don't implement their per-feature interfaces yet; nothing in `driver.go` references them as interface values. Task 10 fills in the per-feature methods, then switches `Store()` to return `d.s`.

---

### Task 10 — Lift per-feature Postgres impls

**Goal:** Move every `core/storage/postgres/*.go` file into `core/persistence/postgres/`, retargeting the methods from `*backend` (or whatever the current type is) to the per-feature aspect types defined in Task 9. Replace `storage.Tx` with `persistence.Tx` in every signature.

**Files:** every `core/storage/postgres/*.go` file (move + edit). Specifically:
- `core/storage/postgres/nodes.go` → `core/persistence/postgres/nodes.go`
- `core/storage/postgres/instances.go` → `core/persistence/postgres/instances.go`
- `core/storage/postgres/templates.go` → `core/persistence/postgres/templates.go`
- `core/storage/postgres/template_tags.go` → `core/persistence/postgres/template_tags.go`
- `core/storage/postgres/schedules.go` → `core/persistence/postgres/schedules.go`
- `core/storage/postgres/supervisors.go` → `core/persistence/postgres/supervisors.go`
- `core/storage/postgres/events.go` → `core/persistence/postgres/events.go`
- `core/storage/postgres/node_attributes.go` → `core/persistence/postgres/node_attributes.go`
- `core/storage/postgres/store_lifecycle.go` → `core/persistence/postgres/store_lifecycle.go`
- `core/storage/postgres/lock_holders.go` → `core/persistence/postgres/lock_holders.go`
- `core/storage/postgres/claim_holders.go` → `core/persistence/postgres/claim_holders.go`

Per file, the mechanical edits:

1. Change `package postgres` (already correct).
2. Change every method receiver: `func (b *backend) Insert(...)` → `func (b *templatesImpl) Insert(...)` (and respective per-feature aspect type from `backend.go`).
3. Change every parameter `tx storage.Tx` → `tx persistence.Tx`.
4. Change every type reference `storage.TemplateRow` → `persistence.TemplateRow` (and similarly for every type lifted into `core/persistence/store.go` in Task 4).
5. Change `b.q(tx)` (or whatever the existing helper is named) calls to `b.q(tx)` — the helper signature stays the same, only the carrier type changed (the new helper lives on `*storeImpl`; aspect types share its layout via the `type templatesImpl storeImpl` trick).

   For aspect types to call the helper, add a small inline helper at the top of each file (or define once in `backend.go`):

   ```go
   func (b *templatesImpl) q(tx persistence.Tx) querier { return (*storeImpl)(b).q(tx) }
   ```

   Apply this pattern uniformly across all 11 per-feature files.

6. Update the import block: drop `core/storage`, add `core/persistence`.

After moving each file, the corresponding source under `core/storage/postgres/` is deleted.

7. Add the per-feature accessor methods to `core/persistence/postgres/backend.go`:

   ```go
   func (s *storeImpl) Templates() persistence.TemplateStore         { return (*templatesImpl)(s) }
   func (s *storeImpl) TemplateTags() persistence.TemplateTagsStore  { return (*templateTagsImpl)(s) }
   func (s *storeImpl) Instances() persistence.InstanceStore         { return (*instancesImpl)(s) }
   func (s *storeImpl) StoreLifecycle() persistence.StoreLifecycleStore { return (*storeLifecycleImpl)(s) }
   func (s *storeImpl) Nodes() persistence.NodeStore                 { return (*nodesImpl)(s) }
   func (s *storeImpl) LockHolders() persistence.LockHoldersStore    { return (*lockHoldersImpl)(s) }
   func (s *storeImpl) NodeAttributes() persistence.NodeAttributesStore { return (*nodeAttributesImpl)(s) }
   func (s *storeImpl) ClaimHolders() persistence.ClaimHoldersStore  { return (*claimHoldersImpl)(s) }
   func (s *storeImpl) Events() persistence.EventStore               { return (*eventsImpl)(s) }
   func (s *storeImpl) Schedules() persistence.ScheduleStore         { return (*schedulesImpl)(s) }
   func (s *storeImpl) Supervisors() persistence.SupervisorStore     { return (*supervisorsImpl)(s) }
   // Frames() is added in Task 20 once *framesImpl exists with methods.
   // For now Store has Frames() in its interface (Task 4 added the
   // FrameStore placeholder); satisfy it with a panic-on-call stub
   // until Task 20:
   func (s *storeImpl) Frames() persistence.FrameStore { return nil }
   ```

8. Update `core/persistence/postgres/driver.go`'s `Store()` accessor:
   - Change `func (d *driver) Store() persistence.Store { return nil }` → `return d.s`.

9. Verify: `go build ./core/persistence/postgres/...`

**Verification:** Build passes. `*storeImpl` satisfies `persistence.Store`; `driver.Store()` returns it.

---

### Task 11 — Lift Postgres queue impl (`postgres/queue.go`)

**Goal:** Move `core/queue/postgres/queue.go` into `core/persistence/postgres/queue.go` and add the `Queue` interface methods on a `*queueImpl` aspect type.

**Files:**
- `core/queue/postgres/queue.go` → `core/persistence/postgres/queue.go`
- `core/queue/postgres/queue_test.go` → `core/persistence/postgres/queue_test.go`

1. Read `core/queue/postgres/queue.go` to understand the existing methods.
2. In the new file:
   - Package becomes `postgres` (already correct after move).
   - The receiver type becomes `*queueImpl` (defined in Task 7).
   - Replace `queue.*` type references with `persistence.*` (DispatchRequest, Candidate, etc.).
   - Replace `pgx.Tx` parameters with `persistence.Tx`; unwrap internally via `unwrapTx(tx)`.
   - **Delete** the top-level helpers `TakeNamedLockAdvisory` and `TakeRegionAdvisory` — their behavior moved to `coordinator.go::TakeNamedLockInTx` / `TakeRegionLockInTx`.
3. Update `queue_test.go` to use `persistence.*` types and the new aspect-type receiver. Tests that exercised `TakeNamedLockAdvisory` / `TakeRegionAdvisory` directly are removed (the conformance suite in Task 38 covers them).
4. Add `func newQueue(pool *pgxpool.Pool) *queueImpl { return &queueImpl{pool: pool} }` and define the struct: `type queueImpl struct { pool *pgxpool.Pool }`.

   Note: `queueImpl` doesn't share `storeImpl`'s aspect-type trick because it's a peer of `Store`, not a sub-store.

5. Update `core/persistence/postgres/driver.go`:
   - Add `q *queueImpl` field to `driver`.
   - Set `d.q = newQueue(pool)` at the end of `open`.
   - Change `Queue()` accessor: `return d.q`.

6. Verify: `go build ./core/persistence/postgres/... && go test ./core/persistence/postgres/...`

**Verification:** Build passes; queue tests pass (they were Postgres tests before, still are — they testcontainer-pull a real Postgres).

---

### Task 12 — Add Postgres migration files + glue (parallel to existing `core/migrations/`)

**Goal:** Stand up the new `core/persistence/postgres/migrations/` package and wire `migrate.go` so `driver.Migrate(ctx)` works. The existing `core/migrations/` is left intact (its runner is still used by the unrefactored cmd binaries) until Task 26 deletes it after every binary has switched.

**Files:**
- `core/persistence/postgres/migrations/001-initial.sql` (new — copied from `core/migrations/001-initial.sql`)
- `core/persistence/postgres/migrations/002-frame-resolution.sql` (new — copied)
- `core/persistence/postgres/migrations/003-template-registry-and-lifecycle.sql` (new — copied)
- `core/persistence/postgres/migrations/embed.go` (new)
- `core/persistence/postgres/migrate.go` (new)

The duplication between `core/migrations/*.sql` and `core/persistence/postgres/migrations/*.sql` is deliberate and temporary; both copies share the same `rimsky_migrations` tracker table so re-running either runner is a no-op once the migration has been applied. The duplicates are deleted along with the rest of `core/migrations/` in Task 26.

1. Copy the three `.sql` files (no content changes; do not move — the originals stay in place):

   ```sh
   cp core/migrations/001-initial.sql                        core/persistence/postgres/migrations/
   cp core/migrations/002-frame-resolution.sql               core/persistence/postgres/migrations/
   cp core/migrations/003-template-registry-and-lifecycle.sql core/persistence/postgres/migrations/
   ```

2. Create `core/persistence/postgres/migrations/embed.go`:

   ```go
   // Package migrations embeds the Postgres migration tree.
   package migrations

   import "embed"

   //go:embed *.sql
   var FS embed.FS
   ```

3. Create `core/persistence/postgres/migrate.go`:

   ```go
   package postgres

   import (
       "context"
       "fmt"

       "github.com/jackc/pgx/v5/pgxpool"

       "github.com/rimsky-ai/rimsky-core/core/persistence"
       "github.com/rimsky-ai/rimsky-core/core/persistence/postgres/migrations"
   )

   func newMigrator(pool *pgxpool.Pool) persistence.Migrator {
       return persistence.Migrator{
           FS: migrations.FS,
           Bootstrap: func(ctx context.Context) error {
               _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS rimsky_migrations (
                   filename    TEXT PRIMARY KEY,
                   applied_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
               )`)
               if err != nil {
                   return fmt.Errorf("bootstrap rimsky_migrations: %w", err)
               }
               return nil
           },
           QueryHas: func(ctx context.Context, filename string) (bool, error) {
               var exists bool
               err := pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM rimsky_migrations WHERE filename = $1)", filename).Scan(&exists)
               return exists, err
           },
           // ApplyOne runs the migration SQL and records it inside a single
           // pgx tx, preserving the pre-refactor per-file atomicity.
           ApplyOne: func(ctx context.Context, sql string, filename string) error {
               tx, err := pool.Begin(ctx)
               if err != nil {
                   return fmt.Errorf("begin tx: %w", err)
               }
               defer tx.Rollback(ctx)
               if _, err := tx.Exec(ctx, sql); err != nil {
                   return fmt.Errorf("exec sql: %w", err)
               }
               if _, err := tx.Exec(ctx, "INSERT INTO rimsky_migrations (filename) VALUES ($1) ON CONFLICT DO NOTHING", filename); err != nil {
                   return fmt.Errorf("record run: %w", err)
               }
               return tx.Commit(ctx)
           },
       }
   }
   ```

4. Update `core/persistence/postgres/driver.go`'s `Migrate` method to use the real runner:

   ```go
   func (d *driver) Migrate(ctx context.Context) error {
       return newMigrator(d.pool).Run(ctx, d.c, nil)
   }
   ```

5. **Verify the new runner end-to-end against testcontainers Postgres.** Add a small smoke test under `core/persistence/postgres/migrate_test.go`:

   ```go
   //go:build !short

   package postgres_test

   import (
       "context"
       "testing"

       "github.com/rimsky-ai/rimsky-core/core/internal/pgtest"
       "github.com/rimsky-ai/rimsky-core/core/persistence"
       _ "github.com/rimsky-ai/rimsky-core/core/persistence/postgres"
   )

   func TestMigrateAgainstTestcontainers(t *testing.T) {
       ctx := context.Background()
       dsn := pgtest.StartPostgres(t) // existing helper; returns DSN string
       d, err := persistence.Open(ctx, persistence.Config{
           Driver:   "postgres",
           Postgres: &persistence.PostgresConfig{DSN: dsn},
       })
       if err != nil { t.Fatalf("open: %v", err) }
       defer d.Close()
       if err := d.Migrate(ctx); err != nil {
           t.Fatalf("migrate: %v", err)
       }
       // Idempotency: re-run is a no-op.
       if err := d.Migrate(ctx); err != nil {
           t.Fatalf("re-migrate: %v", err)
       }
   }
   ```

   The exact `pgtest.StartPostgres(t)` helper name comes from the existing fixture in `core/internal/pgtest/pgtest.go`; check the file and use whatever helper returns a fresh DSN.

6. Verify:
   ```sh
   go build ./...
   go test ./core/persistence/postgres/... -run TestMigrateAgainstTestcontainers -count=1
   ```

   `go build ./...` should pass — `core/migrations/` is unchanged so existing importers (the runtime cmd binaries) keep compiling. The new package is additive.

**Verification:** Full build passes; new migration smoke passes against testcontainers Postgres.

---

### Task 13 — Add `persistence:` block to config loader

**Goal:** Wire the `persistence:` block into the existing `RIMSKY_CONFIG` loader so `cfg.Persistence` is populated.

**Files:**
- `core/config/` — find the file that defines the rimsky.yml unmarshal structure (likely `core/config/rimsky_config.go` or similar; check via `grep -rn 'stores:' core/config/`).

1. Find the existing top-level config struct (the one that holds `Stores`, `NamedLocks`, `Executors`).
2. Add a `Persistence persistence.Config` field with a `yaml:"persistence"` tag.
3. Add validation: at unmarshal time, populate `cfg.Persistence.Driver`, `cfg.Persistence.Postgres`, `cfg.Persistence.SQLite` from the YAML; reject (with a wrapped error) if both sub-blocks are present or if the required fields for the chosen driver are missing. Reuse `persistence.Config.validate()` if exposed; otherwise inline-call it.
4. Verify: `go test ./core/config/... -run Persistence` (write a small test if no parsing test exists yet for a new config field).

**Verification:** New unit test passes; loader handles all four shapes from spec §8.2.

---

### Task 14 — Switch `rimsky-migrate` to the new driver

**Goal:** Smallest binary, contained change. Switches `rimsky-migrate` from `pgxpool.New(dsn)` + `migrations.Run(ctx, pool, log)` to `persistence.Open(ctx, cfg.Persistence)` + `driver.Migrate(ctx)`.

**Files:** `core/cmd/rimsky-migrate/main.go`

1. Read the existing `core/cmd/rimsky-migrate/main.go` to see the current shape.
2. Replace the pgxpool init and the `migrations.Run` call with:

   ```go
   import (
       "github.com/rimsky-ai/rimsky-core/core/persistence"
       _ "github.com/rimsky-ai/rimsky-core/core/persistence/postgres"  // wire init()
       // _ "github.com/rimsky-ai/rimsky-core/core/persistence/sqlite"   // UNCOMMENT in Task 30
       // ... existing imports
   )

   // ... after parsing cfg:
   driver, err := persistence.Open(ctx, cfg.Persistence)
   if err != nil { ... }
   defer driver.Close()
   if err := driver.Migrate(ctx); err != nil { ... }
   ```

   The sqlite import is left commented in this task. Task 30 includes a step to uncomment it across all four binaries (`rimsky-migrate`, `rimsky-scheduler`, `rimsky-supervisor`, `rimsky-control-api`) once the SQLite driver package exists.

3. Drop the `RIMSKY_DB_URL` env var read from this binary (it now flows through `RIMSKY_CONFIG.persistence.postgres.dsn`). Confirm by `grep -n RIMSKY_DB_URL core/cmd/rimsky-migrate/main.go` after the edit — should return nothing.

4. Verify:
   ```sh
   go build ./core/cmd/rimsky-migrate/...
   go test ./core/persistence/postgres/... -run TestMigrateAgainstTestcontainers -count=1
   ```

   Don't run the wider scenario suite yet — the runtime binaries still use the old `core/migrations/` path until Task 22, and the test for *this* binary's correctness is "the persistence-layer migrate path works end-to-end against testcontainers Postgres," which Task 12 already exercises.

**Verification:** Build passes; the migration smoke from Task 12 still passes after `rimsky-migrate` switches over.

---

### Task 15 — Switch `core/internal/pgtest` to expose a `persistence.Driver`

**Goal:** Update the test fixture so subsequent tests of the runtime binaries (which we'll switch in Task 22) have a clean way to get a `persistence.Driver` instead of a `*pgxpool.Pool`.

**Files:** `core/internal/pgtest/pgtest.go`

1. Read the existing file. It exposes a `*pgxpool.Pool` plus a Postgres testcontainer.
2. Add a new function `OpenDriver(ctx, t) persistence.Driver`:
   - Spins up the testcontainer (or reuses the existing setup).
   - Constructs the persistence driver via `persistence.Open(ctx, persistence.Config{Driver: "postgres", Postgres: &persistence.PostgresConfig{DSN: dsn}})`.
   - **Calls `driver.Migrate(ctx)`** so the schema is applied before the test sees the driver. This is load-bearing for the conformance suite (Task 37) — tests assume the schema exists.
   - Registers cleanup via `t.Cleanup(func() { driver.Close() })`.
   - Returns the driver.

   Keep the existing pool-exposing function as-is for now; remove it in Task 27 once nothing references it.

3. Update internal callers (just `core/scenario/harness.go` and any test that imports pgtest directly) — these can wait until the main refactor (Task 22 onward); Task 15 just adds the new helper without removing the old.

4. Verify: `go build ./core/internal/pgtest/...`

**Verification:** Build passes.

---

### Task 16 — Lift `core/store/lockholders.go` into `LockHoldersStore` (interface extension)

**Goal:** The pgx-direct `LockHoldersClient` in `core/store/lockholders.go` has methods (`CountByNamedLock`, `ListByStoreRegion`, `DeleteIfExpired`, etc.) that don't exist on the proper `LockHoldersStore` interface. Add them to the interface (in `core/persistence/store.go`) and implement them on the Postgres impl.

**Files:**
- `core/persistence/store.go` (extend the `LockHoldersStore` interface)
- `core/persistence/postgres/lock_holders.go` (add the new methods)

1. Read `core/store/lockholders.go` to enumerate its full method set.
2. Compare against the existing `LockHoldersStore` interface in `core/persistence/store.go` (lifted from `core/storage/interfaces.go:312`). The methods to add:
   - `CountByNamedLock(ctx, lockName, tx) (int, error)`
   - `ListByStoreRegion(ctx, storeName, tx) ([]LockHolderRow, error)`
   - `DeleteIfExpired(ctx, id, supervisorID, tx) (bool, error)`
   - `LockForUpdate(ctx, id, tx) (*LockHolderRow, error)` (used by `auto_terminal.go::lockLockHolderRow`)
   - Any others spotted in the read.
3. Extend the interface in `core/persistence/store.go` to include them.
4. Implement them on `*lockHoldersImpl` in `core/persistence/postgres/lock_holders.go`. Copy the SQL from `core/store/lockholders.go` verbatim; only the receiver type and the `tx pgx.Tx` → `tx persistence.Tx` (with internal unwrap) change.
5. Do **not** delete `core/store/lockholders.go` yet — supervisor and other callers still import it. Deletion is Task 17.
6. Verify: `go build ./core/persistence/...`

**Verification:** Build passes. The conformance suite (Task 33) will exercise the new methods.

---

### Task 17 — Update supervisor to use `LockHoldersStore` instead of `LockHoldersClient`; delete `core/store/lockholders.go`

**Goal:** Replace every call site of `core/store.LockHoldersClient` in the supervisor (and anywhere else) with `store.LockHolders().*`. After this task no code imports `core/store/lockholders.go`; delete it.

**Files:**
- `core/supervisor/runner_acquire.go`, `runner_terminal.go`, `runner_held_claims.go`, `auto_terminal.go`, anywhere else that imports `LockHoldersClient`.
- `core/store/lockholders.go` (delete)

1. Find every importer: `grep -rln "LockHoldersClient" core/`.
2. For each call site, replace `client.Insert(ctx, tx, row)` (etc.) with `store.LockHolders().Insert(ctx, in, tx)` — note the parameter order change between the old pgx-direct shape and the storage-interface shape (the storage interface puts `tx` last per `core/storage/interfaces.go:313`).
3. Change function signatures that took a `*store.LockHoldersClient` to take a `persistence.LockHoldersStore` (or `persistence.Store` and call `.LockHolders()` inside).
4. Delete `core/store/lockholders.go`.
5. Verify: `go build ./...` and `go test ./core/supervisor/... -count=1`

**Verification:** Build passes; supervisor unit tests pass.

---

### Task 18 — Lift `core/attributes/store.go` into `NodeAttributesStore`

**Goal:** Same pattern as Task 16/17 for the attributes store. The pgx-direct `attributes.Store` is parallel to `NodeAttributesStore`; merge them.

**Files:**
- `core/persistence/store.go` (extend `NodeAttributesStore` if any methods are missing)
- `core/persistence/postgres/node_attributes.go` (add any missing methods)
- All callers of `attributes.Store` (refactor)
- `core/attributes/store.go` (delete)

1. Read `core/attributes/store.go`. Compare against `NodeAttributesStore` (current methods: `Get`, `Upsert`, `MergeDelta`).
2. Add any missing methods to the interface and the Postgres impl.
3. Find callers via `grep -rln "attributes\.Store\b" core/` (or `grep -rln "attributes.New" core/` since `New` likely returns `*Store`).
4. Refactor each caller to use `store.NodeAttributes().*`.
5. Delete `core/attributes/store.go`. Note: the attributes package may have other files (e.g., substitution logic) — only delete the store.go file, not the whole package.
6. Verify: `go build ./... && go test ./core/attributes/... -count=1`

**Verification:** Build passes; attributes tests pass.

---

### Task 19 — Define `FrameStore` interface (full enumeration)

**Goal:** Read `core/frame/engine.go` and `producer.go` end-to-end. Enumerate every persistence operation the frame engine performs. Replace the `FrameStore = struct{}` placeholder from Task 4 with the full interface.

**Files:**
- `core/persistence/store.go` (or a `core/persistence/types_frames.go` if splitting)

1. Read `core/frame/engine.go` (~500 lines) and `core/frame/producer.go` (~105 lines) and `core/frame/types.go` (~55 lines).
2. List every distinct SQL operation. Group by table (rimsky_frames, rimsky_dispatch, rimsky_nodes when frame-engine-driven, etc.).
3. Translate each SQL operation into an interface method. Method-naming convention: imperative + entity (e.g., `InsertFrame`, `UpdateFrameState`, `EnqueueDispatchInFrame`, `MarkNodeStaleInFrame`).
4. Each method takes `(ctx, ..., tx persistence.Tx)`. The tx parameter is required for every method that participates in the frame-tick tx; methods called outside a tx (e.g., a status query) may take `tx = nil`.
5. Add `FrameRow`, `FrameInsertInput`, `FrameUpdateInput` etc. types — lifted from whatever `core/frame/types.go` carries today (and renamed to `persistence.Frame*` if needed for namespacing).
6. The interface ends up roughly:

   ```go
   type FrameStore interface {
       Insert(ctx context.Context, in FrameInsertInput, tx Tx) (FrameRow, error)
       Get(ctx context.Context, id shared.UUID, tx Tx) (*FrameRow, error)
       UpdateState(ctx context.Context, id shared.UUID, state string, tx Tx) error
       ListRunningByInstance(ctx context.Context, instanceID shared.UUID, tx Tx) ([]FrameRow, error)
       // ... every other method needed by core/frame/{engine,producer}.go
   }
   ```

7. The exact method list cannot be authored from the spec alone; this task's deliverable is the enumeration done correctly. Document the chosen method set in a short note (`docs/specs/notes/2026-05-02-framestore-methods.md`) for future readers.

   **Note for the implementer:** Tasks 19, 20, 21 are best executed as a tight loop. Task 19's verification (build passes) cannot catch a missing method — the omission only surfaces in Task 21 when the frame-engine refactor needs a method that doesn't exist on `FrameStore`. When that happens, return to Task 19, add the method to the interface and the corresponding type, then add the impl in Task 20, then continue Task 21. Plan to iterate.

8. Verify: `go build ./core/persistence/...`

**Verification:** Build passes (only `core/persistence/` is affected; the frame engine still uses pgx and isn't touched yet).

---

### Task 20 — Implement `FrameStore` for Postgres

**Goal:** Implement every `FrameStore` method on `*framesImpl` in `core/persistence/postgres/frames.go`. Lift the SQL from `core/frame/engine.go` and `producer.go`.

**Files:** `core/persistence/postgres/frames.go` (new)

1. For each method declared in Task 19, find the corresponding SQL in `core/frame/engine.go` or `producer.go`. Move the SQL into a method on `*framesImpl`. Replace `pgx.Tx` parameters with `persistence.Tx` (unwrap internally).
2. The frame engine has its own `BeginTx`-shaped abstraction (`FrameDB` per `core/frame/engine.go:54`); ignore it for now — the new methods just take `persistence.Tx` and call through `b.q(tx)` (the helper from `backend.go`).
3. Verify: `go build ./core/persistence/postgres/...`

**Verification:** Build passes; `*framesImpl` satisfies `FrameStore`.

---

### Task 21 — Refactor `core/frame/` to use `persistence.Store`

**Goal:** Replace every pgx call in `core/frame/engine.go`, `producer.go`, `types.go` with calls through `persistence.Store` and `persistence.FrameStore`. Drop the `FrameDB` abstraction.

**Files:** `core/frame/engine.go`, `core/frame/producer.go`, `core/frame/types.go`

1. Replace the `FrameDB` interface (line 54 of `engine.go`) with a parameter type `persistence.Store`.
2. Replace every `db.BeginTx(ctx, pgx.TxOptions{})` with `store.Transaction(ctx, func(ctx, tx) error { ... })`.
3. Inside the closure, replace every raw SQL call with the appropriate `store.Frames().*`, `store.Dispatch()` (wait — there's no DispatchStore on Store; the dispatch table is owned by Queue. So calls to enqueue dispatch from inside a frame tick should go through `queue.Enqueue(...)` instead, which itself takes a `persistence.Tx`. Verify the Queue interface supports being called inside an externally-owned tx — `Queue.Enqueue` per `core/queue/interface.go:143` does not take a tx today, so it auto-commits. That's a problem for the frame-tick-tx usage. Two options:
   - Add a tx-taking variant `Queue.EnqueueInTx(ctx, req, tx)` to the interface; implement on both drivers.
   - Move the enqueue SQL into `FrameStore` (since it's part of the frame-tick tx anyway).
   The first option is cleaner (keeps queue semantics in queue). Take it.
4. Add `EnqueueInTx` to `persistence.Queue` (Task 3's interface). Implement on `*queueImpl` in `core/persistence/postgres/queue.go` — share the SQL with the existing `Enqueue` (refactor `Enqueue` to internally call `EnqueueInTx(ctx, req, nil)` with the auto-commit path).
5. Refactor `core/frame/engine.go` and `producer.go` until no `pgx` import remains.
6. The exported entry points (`frame.RunTick`, `frame.EnqueueOrCoalesce`, etc.) change signature from `(*pgxpool.Pool, ...)` (or `(FrameDB, ...)`) to `(persistence.Store, ...)`. **In this task, also update every direct caller of these entry points** so the build stays green. Callers (find via `grep -rn "frame\." core/`):
   - `core/scheduler/invalidate.go::InvalidateNode` — calls `frame.EnqueueOrCoalesce`. Plumb a `persistence.Store` parameter through (the scheduler still holds `*pgxpool.Pool` for its own SQL at this point, but it can also hold a `persistence.Store` constructed from the same pool via a helper added to `core/persistence/postgres`: `postgres.StoreFromPool(pool) persistence.Store` — temporary helper, deleted in Task 26).
   - `core/scheduler/pure_cascade.go` — same.
   - `core/scheduler/scheduler.go` — same; `frame.RunTick` is called from the scheduler tick.
   - `core/controlapi/instances.go:470`, `core/controlapi/nodes.go:204` — same.
   - `core/cmd/rimsky-scheduler/main.go` — passes the pool to the scheduler today; add a `persistence.Driver` construction so it can pass `driver.Store()` alongside the pool. The pool stays in use by other unrefactored code paths.

   Add a temporary helper file `core/persistence/postgres/from_pool.go` (deleted in Task 26):

   ```go
   package postgres

   import (
       "github.com/jackc/pgx/v5/pgxpool"
       "github.com/rimsky-ai/rimsky-core/core/persistence"
   )

   // StoreFromPool wraps an existing *pgxpool.Pool as a persistence.Store
   // for the transition window between Tasks 21 and 26. After Task 26 every
   // caller has switched to the full persistence.Driver path; delete this
   // helper at that time.
   func StoreFromPool(pool *pgxpool.Pool) persistence.Store {
       return newStore(pool)
   }
   ```

7. Verify:
   ```sh
   go build ./...
   go test ./core/frame/... ./core/persistence/... -count=1
   ```

**Verification:** Full build passes; frame and persistence tests pass.

---

### Task 22 — Switch the runtime cmd binaries to `persistence.Driver`

**Goal:** Switch `rimsky-scheduler`, `rimsky-supervisor`, `rimsky-control-api` from `pgxpool.New(dsn)` to `persistence.Open(ctx, cfg.Persistence)`. Pass `persistence.Driver` (or `Store`/`Queue`/`Coordinator`) into the existing `core/config.Start*` entry points instead of `*pgxpool.Pool`.

**Files:**
- `core/cmd/rimsky-scheduler/main.go`
- `core/cmd/rimsky-supervisor/main.go`
- `core/cmd/rimsky-control-api/main.go`
- `core/config/scheduler.go` (and any peer files: `supervisor.go`, `controlapi.go` if they exist)

1. For each `main.go`:
   - Replace `pgxpool.New(dsn)` with `persistence.Open(ctx, cfg.Persistence)`.
   - Drop `RIMSKY_DB_URL` env var read.
   - Add `_ "github.com/rimsky-ai/rimsky-core/core/persistence/postgres"` import to register the driver.
   - The supervisor/scheduler/controlapi packages still hold pgx directly until Tasks 23–25; until then, extract the underlying pool from the driver and pass *both* `*pgxpool.Pool` and `persistence.Driver` (or its sub-interfaces) into the `core/config/Start*` entry points. Add a temporary accessor on `core/persistence/postgres`:

     ```go
     // PoolFromDriver returns the underlying *pgxpool.Pool for a postgres
     // driver. Temporary helper for the transition window between Task 22
     // and Task 26; delete in Task 26 when no caller still needs raw pool
     // access.
     func PoolFromDriver(d persistence.Driver) *pgxpool.Pool {
         pd, ok := d.(*driver)
         if !ok { panic("PoolFromDriver: not a postgres driver") }
         return pd.pool
     }
     ```

   - In each `main.go`, use it to keep the existing `Start*` signatures working: `pool := postgres.PoolFromDriver(d)`.

2. For each `core/config/Start*` function: add a `persistence.Driver` parameter alongside the existing `*pgxpool.Pool` parameter (don't delete the pool param yet). Pass the driver through to whatever needs it (e.g., the scheduler's frame-engine call sites from Task 21 use `driver.Store()`); the pool param continues to feed the unrefactored supervisor/scheduler/controlapi internals. Both params will be present until Tasks 23–25 drop the pool dependency from their respective packages.

3. Verify:
   ```sh
   go build ./...
   ```

**Verification:** Full build passes. The cmd binaries now construct the driver via `persistence.Open`; the supervisor/scheduler/controlapi keep using the pool extracted via `PoolFromDriver` until they're refactored in Tasks 23–25.

---

### Task 23 — Refactor supervisor to drop pgx (mass refactor)

**Goal:** Refactor every file in `core/supervisor/` to drop direct pgx imports. Per spec §3.6.

**Files:** all 11 files in `core/supervisor/`:
- `runner.go`, `runner_acquire.go`, `runner_dispatch.go`, `runner_held_claims.go`, `runner_locks.go`, `runner_terminal.go`, `auto_terminal.go`, `callback.go`, `supervisor.go`, `on_error.go`, `terminal_outcome.go`

1. For each file, mechanically apply the following transformations:
   - Every `pgx.Tx` parameter → `persistence.Tx`.
   - Every `args.QueuePool.BeginTx(ctx, pgx.TxOptions{})` → `args.Store.Transaction(ctx, func(ctx, tx persistence.Tx) error { ... })`. Wrap the existing post-Begin code inside the closure.
   - Every call to the old `LockHoldersClient.*` (already removed in Task 17) — no further work.
   - Every call to `TakeNamedLockAdvisory` / `TakeRegionAdvisory` (helpers from queue/postgres) → `args.Coordinator.TakeNamedLockInTx(ctx, tx, name)` / `TakeRegionLockInTx(ctx, tx, storeName, regionData)`.
   - Every `pgstorage.WrapPgxTx(tx)` and every `pgstorage.PgxTxFromStorage(stx)` call → delete; the `tx` is already `persistence.Tx`.
   - Every raw `SELECT ... FOR UPDATE` (specifically `auto_terminal.go::lockLockHolderRow` at line 122) → `store.LockHolders().LockForUpdate(ctx, id, tx)`.
   - Update `RunArgs` (or whatever struct holds the persistence references) to hold `persistence.Driver` (or `Store` + `Queue` + `Coordinator` separately).
   - Drop `import "github.com/jackc/pgx/v5"` from each file.

2. **Implementer:** grep the supervisor for any other raw `SELECT ... FOR UPDATE` patterns: `grep -rn "FOR UPDATE" core/supervisor/`. Lift each to a typed `*Store.LockForUpdate` method (extending the relevant interface and impl if needed).

3. The supervisor's tests in `core/supervisor/*_test.go` may need parameter updates (replace `*pgxpool.Pool` parameters with the new `persistence.Driver`).

4. Verify:
   ```sh
   go build ./core/supervisor/...
   go test ./core/supervisor/... -count=1
   ```

**Verification:** Build passes; supervisor tests pass.

---

### Task 24 — Refactor scheduler to drop pgx

**Goal:** Same pattern as Task 23 for `core/scheduler/`.

**Files:** all files in `core/scheduler/` that import pgx: `scheduler.go`, `sweep_locks.go`, `invalidate.go`, `pure_cascade.go`, possibly `recalculate.go`, `schedule_ticker.go`

1. Apply the same transformations:
   - `pg_try_advisory_lock(SCHEDULER_TICK_KEY)` (in `scheduler.go`) → `coordinator.TrySchedulerTick(ctx)`. Delete the `RimskySchedulerTickLockKey` constant from `scheduler.go` (it was already moved to `core/persistence/postgres/coordinator.go` in Task 8).
   - Every `pgstorage.PgxTxFromStorage(stx)` (in `invalidate.go:94`, `pure_cascade.go:233`) → delete; the surrounding code calls into the frame engine, which now takes `persistence.Store` directly (per Task 21).
   - Every `*pgxpool.Pool` parameter → `persistence.Store` (or `Queue` / `Coordinator` as needed).
   - Orphan reaper's direct pool access → `store.LockHolders().ListExpired(ctx, cutoff, nil)` etc.
   - Drop `import "github.com/jackc/pgx/v5"` and `pgxpool`.

2. Verify:
   ```sh
   go build ./core/scheduler/...
   go test ./core/scheduler/... -count=1
   ```

**Verification:** Build passes; scheduler tests pass.

---

### Task 25 — Refactor controlapi to drop pgx

**Goal:** Same pattern for `core/controlapi/`.

**Files:** every file in `core/controlapi/` that imports pgx (find via `grep -rln "github.com/jackc/pgx" core/controlapi/`)

1. Apply transformations:
   - `pgstorage.PgxTxFromStorage(tx)` calls (notably `instances.go:470`, `nodes.go:204`) → delete; frame-engine entry points take `persistence.Store` directly.
   - `*pgxpool.Pool` parameters → `persistence.Store`.
   - Drop pgx imports.

2. Verify:
   ```sh
   go build ./core/controlapi/...
   go test ./core/controlapi/... -count=1
   ```

**Verification:** Build passes; controlapi tests pass.

---

### Task 26 — Final cleanup: delete escape hatches, transition helpers, and old packages

**Goal:** After Tasks 23–25 the supervisor/scheduler/controlapi no longer use the pool or the pgx-Tx escape hatches. Delete every transition helper, old directory, and dead file in one cleanup pass.

**Files (deletions):**
- `core/storage/postgres/backend.go` — delete `WrapPgxTx`, `PgxTxFromStorage` if anything still defines them. (After Task 10 the directory should be empty; verify and delete the whole directory.)
- `core/storage/` — delete the directory entirely (interfaces lifted in Task 4).
- `core/queue/postgres/` — delete (queue.go lifted in Task 11).
- `core/queue/` — delete (interface lifted in Task 3).
- `core/migrations/` — delete (the runner was lifted in Task 6; the SQL files were copied in Task 12; nothing imports this package after Task 22).
- `core/persistence/postgres/from_pool.go` — delete the `StoreFromPool` transition helper added in Task 21.
- `core/persistence/postgres/driver.go` — delete the `PoolFromDriver` transition helper added in Task 22.
- Each `core/cmd/rimsky-{scheduler,supervisor,control-api}/main.go` — delete the `PoolFromDriver` call and the `pool` argument passed to `core/config/Start*`.
- Each `core/config/Start*` function — drop the `*pgxpool.Pool` parameter; the `persistence.Driver` parameter is now sufficient.

1. Run `grep -rln "fallguy/rimsky/core/storage\|fallguy/rimsky/core/queue\|fallguy/rimsky/core/migrations" core/ test/` — should be empty after the supervisor/scheduler/controlapi refactors. Any survivors are bugs from earlier tasks; fix them by switching to the new persistence import.

2. Run `grep -rn "PoolFromDriver\|StoreFromPool" core/ test/` — every hit must be in code being deleted. Fix any caller that still uses these helpers (it means an earlier task left a pool-based path in place).

3. Delete the directories and the helper files in the order listed above.

4. Verify:
   ```sh
   go build ./...
   go test ./... -count=1
   make lint
   ```

**Verification:** Full build passes; full test suite passes against the new persistence layer (still Postgres-only at this point — SQLite lands in Tasks 30–35).

---

### Task 27 — Switch scenario harness and pgtest to `persistence.Driver`

**Goal:** Update the test infrastructure to expose the new driver type, completing the Postgres-side transition.

**Files:**
- `core/scenario/harness.go` — replace `*pgxpool.Pool` with `persistence.Driver`.
- `core/internal/pgtest/pgtest.go` — make `OpenDriver` (added in Task 15) the canonical helper; remove the legacy pool-only function if nothing references it.
- `test/smoke/setup.go` — same treatment.

1. Update the harness `Start` (or equivalent) function to take a `persistence.Driver` and pass it through to whatever the runtime processes need.
2. Update existing scenario tests' setup code to use the new helper.
3. Verify:
   ```sh
   go test ./test/scenarios/... -count=1
   ```

**Verification:** Scenario suite still passes (these tests exercise the full supervisor + scheduler + control-api stack against testcontainers Postgres).

---

### Task 28 — golangci-lint depguard rule for pgx

**Goal:** Mechanically prevent reintroduction of pgx imports outside the sanctioned packages.

**Files:** `.golangci.yml`

1. Read the existing `.golangci.yml`.
2. Add a `depguard` (or `forbidigo`) block:

   ```yaml
   linters-settings:
     depguard:
       rules:
         pgx-isolation:
           list-mode: lax
           files:
             - "$all"
             - "!core/persistence/postgres/**"
             - "!core/cmd/**"
             - "!core/internal/pgtest/**"
             - "!core/scenario/**"
             - "!stores/**"
             - "!test/smoke/**"
           deny:
             - pkg: "github.com/jackc/pgx/v5"
               desc: "pgx is allowed only in core/persistence/postgres/, core/cmd/, core/internal/pgtest/, core/scenario/, stores/, and test/smoke/. Use the persistence interface."
             - pkg: "github.com/jackc/pgx/v5/pgxpool"
               desc: "see pgx isolation rule above"
             - pkg: "github.com/jackc/pgx/v5/pgconn"
               desc: "see pgx isolation rule above"
   ```

   The exclusion list covers test fixtures (`pgtest`, `scenario`, `test/smoke`) that legitimately need testcontainers Postgres setup. The exact syntax depends on the golangci-lint version in use; adjust to match.

3. Add `depguard` to the `linters:` enabled list if it's not already there.

4. Verify: `make lint` passes. If it fails, the diagnostic will name the offending file → either fix it (more likely the right move) or extend the allow-list.

**Verification:** `make lint` passes.

---

### Task 29 — Update `deploy/rimsky.yml` and verify the docker-compose stack still works

**Goal:** Add the `persistence:` block to the existing `deploy/rimsky.yml` so the docker-compose dev stack keeps booting.

**Files:** `deploy/rimsky.yml`

1. Add at the top of the file:

   ```yaml
   persistence:
     driver: postgres
     postgres:
       dsn: postgres://rimsky:rimsky@postgres:5432/rimsky?sslmode=disable
   ```

   (Match the DSN to whatever the existing `RIMSKY_DB_URL` env var pointed at in `deploy/docker-compose.yml`.)

2. Remove the `RIMSKY_DB_URL` environment-variable lines from `deploy/docker-compose.yml` (they're no longer read by the binaries).

3. Verify:
   ```sh
   docker compose -f deploy/docker-compose.yml down -v
   docker compose -f deploy/docker-compose.yml up -d --build
   curl http://localhost:8080/health
   ```

**Verification:** `/health` returns 200.

---

### Task 30 — SQLite driver skeleton

**Goal:** Lay down the `core/persistence/sqlite/` package, the `Driver` impl, and the package init that registers `openSQLite`.

**Files:**
- `go.mod` — add `modernc.org/sqlite`
- `core/persistence/sqlite/driver.go` (new)

1. Add the dependency: `cd /Users/.../rimsky && go get modernc.org/sqlite@latest`.
2. Verify `go.sum` has no transitive cgo dependencies: `go list -m all | grep -i cgo` — should return nothing.
3. Create `core/persistence/sqlite/driver.go`:

   ```go
   // Package sqlite is the SQLite-backed persistence.Driver.
   //
   // SQLite is the dev-only driver per spec §1 and §6. Multi-host /
   // multi-replica deployments require Postgres. The startup banner says
   // so loudly on every process that opens it.
   package sqlite

   import (
       "context"
       "database/sql"
       "fmt"
       "log/slog"
       "net/url"
       "os"
       "path/filepath"

       _ "modernc.org/sqlite"

       "github.com/rimsky-ai/rimsky-core/core/persistence"
   )

   func init() {
       persistence.RegisterSQLite(open)
   }

   // driver is the persistence.Driver impl. The accessors return nil until
   // Tasks 31–35 fill in the per-area impls; same staging pattern as
   // Task 7's Postgres driver.
   type driver struct {
       db *sql.DB
       // q, s, c fields will be added by Tasks 35, 33, 31 respectively as
       // each impl lands. Don't add them now.
   }

   func (d *driver) Queue() persistence.Queue             { return nil }
   func (d *driver) Store() persistence.Store             { return nil }
   func (d *driver) Coordinator() persistence.Coordinator { return nil }
   func (d *driver) Close() error                          { return d.db.Close() }

   // Migrate is wired in Task 32; Task 30 returns a placeholder error.
   func (d *driver) Migrate(ctx context.Context) error {
       return errors.New("sqlite driver not yet wired (migrate)")
   }

   func open(ctx context.Context, cfg persistence.SQLiteConfig) (persistence.Driver, error) {
       if !filepath.IsAbs(cfg.Path) {
           return nil, fmt.Errorf("sqlite: path %q must be absolute", cfg.Path)
       }
       parent := filepath.Dir(cfg.Path)
       if _, err := os.Stat(parent); err != nil {
           return nil, fmt.Errorf("sqlite: parent dir %q: %w", parent, err)
       }

       q := url.Values{}
       q.Set("_journal_mode", "WAL")
       q.Set("_synchronous", "NORMAL")
       q.Set("_busy_timeout", "5000")
       q.Set("_foreign_keys", "ON")
       q.Set("_txlock", "immediate")
       dsn := "file:" + cfg.Path + "?" + q.Encode()

       db, err := sql.Open("sqlite", dsn)
       if err != nil {
           return nil, fmt.Errorf("sqlite: open: %w", err)
       }
       db.SetMaxOpenConns(1) // single-writer per spec §6.2

       if err := db.PingContext(ctx); err != nil {
           db.Close()
           return nil, fmt.Errorf("sqlite: ping: %w", err)
       }

       slog.Warn("persistence driver in use",
           "driver", "sqlite",
           "path", cfg.Path,
           "warning", "SQLite driver is for local development only — not supported for production. Use the postgres driver for deployed rimsky instances.")

       return &driver{db: db}, nil
   }
   ```

   Add `errors` to the imports.

4. Same nil-returning pattern as Task 7. The `*driver` accessors in Task 30's code already return `nil` for `Queue`, `Store`, `Coordinator`; `Migrate` returns `errors.New("not yet wired")`. Tasks 31–35 fill them in one piece at a time, each adding a field to `driver` and switching the corresponding accessor to return the impl pointer.

5. **Uncomment the SQLite registration import in every cmd binary** (added but commented out in Task 14):

   ```go
   _ "github.com/rimsky-ai/rimsky-core/core/persistence/sqlite"
   ```

   Apply to all four binaries:
   - `core/cmd/rimsky-migrate/main.go`
   - `core/cmd/rimsky-scheduler/main.go`
   - `core/cmd/rimsky-supervisor/main.go`
   - `core/cmd/rimsky-control-api/main.go`

6. Verify: `go build ./...`

**Verification:** Build passes. The SQLite driver registers itself; `persistence.Open(... driver: sqlite ...)` returns a `*driver` whose accessors are nil-returning placeholders until Tasks 31–35.

---

### Task 31 — SQLite coordinator

**Goal:** Implement the four `Coordinator` methods on the SQLite driver per spec §4.2 — `sync.Mutex` for the cross-process methods, no-ops for the xact-locks.

**Files:** `core/persistence/sqlite/coordinator.go` (new)

1. Create:

   ```go
   package sqlite

   import (
       "context"
       "database/sql"
       "sync"

       "github.com/rimsky-ai/rimsky-core/core/persistence"
   )

   type coordinatorImpl struct {
       schedulerTick sync.Mutex
       migration     sync.Mutex
   }

   func newCoordinator(*sql.DB) *coordinatorImpl { return &coordinatorImpl{} }

   func (c *coordinatorImpl) TrySchedulerTick(ctx context.Context) (bool, func(), error) {
       if !c.schedulerTick.TryLock() {
           return false, nil, nil
       }
       return true, c.schedulerTick.Unlock, nil
   }

   func (c *coordinatorImpl) AcquireMigrationLock(ctx context.Context) (func() error, error) {
       c.migration.Lock()
       return func() error { c.migration.Unlock(); return nil }, nil
   }

   // TakeNamedLockInTx is a no-op under SQLite. The surrounding BEGIN
   // IMMEDIATE writer-slot hold subsumes per-name advisory locking
   // (strictly stronger). Per spec §4.2.
   func (c *coordinatorImpl) TakeNamedLockInTx(ctx context.Context, tx persistence.Tx, name string) error {
       return nil
   }

   func (c *coordinatorImpl) TakeRegionLockInTx(ctx context.Context, tx persistence.Tx, storeName string, regionData []byte) error {
       return nil
   }
   ```

2. Update `core/persistence/sqlite/driver.go`:
   - Add `c *coordinatorImpl` field to `driver`.
   - Set `d.c = newCoordinator(d.db); return &driver{db: db, c: ...}` style — easier to refactor `open()` to construct the impls bottom-up at the end.
   - Change `Coordinator()` accessor: `return d.c`.

3. Verify: `go build ./core/persistence/sqlite/...`

**Verification:** Build passes; `*coordinatorImpl` satisfies `persistence.Coordinator`.

---

### Task 32 — SQLite migration tree (one consolidated init file)

**Goal:** Hand-write `001-initial.sql` for SQLite, capturing the current schema state in SQLite dialect per spec §5.1 and §5.4.

**Files:**
- `core/persistence/sqlite/migrations/001-initial.sql` (new)
- `core/persistence/sqlite/migrations/embed.go` (new)
- `core/persistence/sqlite/migrate.go` (new)

1. Read all three Postgres migration files (`001-initial.sql`, `002-frame-resolution.sql`, `003-template-registry-and-lifecycle.sql`) under `core/persistence/postgres/migrations/`. Compose the union into a single SQLite-dialect `001-initial.sql`.

2. Apply the §5.4 dialect drift rules:
   - `JSONB` → `TEXT`
   - `UUID` + `gen_random_uuid()` → `TEXT` (no default; app generates with `uuid.New()`)
   - `TIMESTAMPTZ` + `NOW()` → `TEXT` (no default; app generates with `time.Now().UTC().Format(time.RFC3339Nano)`)
   - `BIGSERIAL` → `INTEGER PRIMARY KEY AUTOINCREMENT`
   - `UUID[]` and `TEXT[]` → `TEXT` holding JSON arrays
   - Partial indexes (`CREATE INDEX ... WHERE ...`) — SQLite supports same syntax (3.8+)
   - `ON DELETE CASCADE` — SQLite supports same syntax; foreign keys must be enabled per connection (handled in driver.go via `_foreign_keys=ON`)
   - `CHECK` constraints — supported

3. Create `embed.go`:

   ```go
   package migrations

   import "embed"

   //go:embed *.sql
   var FS embed.FS
   ```

4. Create `migrate.go` mirroring the Postgres version:

   ```go
   package sqlite

   import (
       "context"
       "database/sql"

       "github.com/rimsky-ai/rimsky-core/core/persistence"
       "github.com/rimsky-ai/rimsky-core/core/persistence/sqlite/migrations"
   )

   func newMigrator(db *sql.DB) persistence.Migrator {
       return persistence.Migrator{
           FS: migrations.FS,
           Bootstrap: func(ctx context.Context) error {
               _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS rimsky_migrations (
                   filename    TEXT PRIMARY KEY,
                   applied_at  TEXT NOT NULL DEFAULT (datetime('now'))
               )`)
               return err
           },
           QueryHas: func(ctx context.Context, filename string) (bool, error) {
               var n int
               err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM rimsky_migrations WHERE filename = ?", filename).Scan(&n)
               return n > 0, err
           },
           ApplyOne: func(ctx context.Context, sql string, filename string) error {
               tx, err := db.BeginTx(ctx, nil)
               if err != nil { return fmt.Errorf("begin tx: %w", err) }
               defer tx.Rollback()
               if _, err := tx.ExecContext(ctx, sql); err != nil {
                   return fmt.Errorf("exec sql: %w", err)
               }
               if _, err := tx.ExecContext(ctx, "INSERT INTO rimsky_migrations (filename) VALUES (?) ON CONFLICT DO NOTHING", filename); err != nil {
                   return fmt.Errorf("record run: %w", err)
               }
               return tx.Commit()
           },
       }
   }
   ```

5. Update `core/persistence/sqlite/driver.go`'s `Migrate` method:

   ```go
   func (d *driver) Migrate(ctx context.Context) error {
       return newMigrator(d.db).Run(ctx, d.c, nil)
   }
   ```

6. **Run the migration end-to-end against an ephemeral SQLite DB.** Add `core/persistence/sqlite/migrate_test.go`:

   ```go
   package sqlite_test

   import (
       "context"
       "testing"

       "github.com/rimsky-ai/rimsky-core/core/persistence"
       _ "github.com/rimsky-ai/rimsky-core/core/persistence/sqlite"
   )

   func TestSQLiteMigrationApplies(t *testing.T) {
       dir := t.TempDir()
       d, err := persistence.Open(context.Background(), persistence.Config{
           Driver: "sqlite",
           SQLite: &persistence.SQLiteConfig{Path: dir + "/state.db"},
       })
       if err != nil { t.Fatalf("open: %v", err) }
       defer d.Close()
       if err := d.Migrate(context.Background()); err != nil {
           t.Fatalf("migrate: %v", err)
       }
       // Idempotency.
       if err := d.Migrate(context.Background()); err != nil {
           t.Fatalf("re-migrate: %v", err)
       }
   }
   ```

   This is the first checkpoint that the hand-written SQLite SQL is syntactically valid and runs end-to-end. SQL syntax errors surface here, not in Task 36.

7. Verify:
   ```sh
   go build ./...
   go test ./core/persistence/sqlite/... -run TestSQLiteMigrationApplies -count=1
   ```

**Verification:** Build passes; the SQLite migration applies cleanly, twice.

---

### Task 33 — SQLite backend & Tx carrier

**Goal:** Implement `core/persistence/sqlite/backend.go` mirroring the Postgres backend's structure, with `*sql.Tx` as the underlying carrier.

**Files:** `core/persistence/sqlite/backend.go` (new)

1. Create:

   ```go
   package sqlite

   import (
       "context"
       "database/sql"
       "errors"
       "fmt"

       "github.com/rimsky-ai/rimsky-core/core/persistence"
   )

   type sqliteTx struct {
       persistence.TxMarker
       tx *sql.Tx
   }

   func unwrapTx(tx persistence.Tx) (*sql.Tx, error) {
       if tx == nil {
           return nil, errors.New("nil persistence.Tx")
       }
       t, ok := tx.(*sqliteTx)
       if !ok {
           return nil, fmt.Errorf("persistence.Tx is not a sqlite tx: %T", tx)
       }
       return t.tx, nil
   }

   type storeImpl struct {
       db *sql.DB
   }

   func newStore(db *sql.DB) *storeImpl { return &storeImpl{db: db} }

   // Per-feature accessors are added in Task 34 once each per-feature impl
   // satisfies its interface. Don't add them here in Task 33 — the aspect
   // types don't satisfy the interfaces yet, so the build would fail.
   // Same staging as Task 9's Postgres backend.

   func (s *storeImpl) Transaction(ctx context.Context, fn func(ctx context.Context, tx persistence.Tx) error) error {
       sTx, err := s.db.BeginTx(ctx, nil)
       if err != nil {
           return fmt.Errorf("sqlite.Transaction: begin: %w", err)
       }
       defer func() {
           if p := recover(); p != nil {
               _ = sTx.Rollback()
               panic(p)
           }
       }()
       if err := fn(ctx, &sqliteTx{tx: sTx}); err != nil {
           _ = sTx.Rollback()
           return err
       }
       return sTx.Commit()
   }

   // Querier abstraction — both *sql.DB and *sql.Tx have the same methods.
   type querier interface {
       ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
       QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
       QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
   }

   func (s *storeImpl) q(tx persistence.Tx) querier {
       if tx == nil {
           return s.db
       }
       t, ok := tx.(*sqliteTx)
       if !ok {
           panic(fmt.Sprintf("sqlite.q: persistence.Tx is not a sqlite tx: %T", tx))
       }
       return t.tx
   }

   // Per-feature aspect types.
   type (
       templatesImpl       storeImpl
       templateTagsImpl    storeImpl
       instancesImpl       storeImpl
       storeLifecycleImpl  storeImpl
       nodesImpl           storeImpl
       lockHoldersImpl     storeImpl
       nodeAttributesImpl  storeImpl
       claimHoldersImpl    storeImpl
       eventsImpl          storeImpl
       schedulesImpl       storeImpl
       supervisorsImpl     storeImpl
       framesImpl          storeImpl
   )

   // Helper for aspect types to call s.q
   func (b *templatesImpl) q(tx persistence.Tx) querier { return (*storeImpl)(b).q(tx) }
   // ... mirror for every aspect type
   ```

2. Update `core/persistence/sqlite/driver.go`:
   - Add `s *storeImpl` field to `driver`.
   - Set `d.s = newStore(db)` after `db.PingContext(...)`.
   - Leave `Store()` returning `nil` for now; Task 34 switches it after the per-feature impls land.

3. Verify: `go build ./core/persistence/sqlite/...`

**Verification:** Build passes. The aspect types and the `storeImpl` exist; nothing references them as interface values yet. Task 34 fills in the methods, then switches `Store()` to return `d.s`.

---

### Task 34 — SQLite per-feature impls (mass implementation)

**Goal:** Implement every per-feature interface method on the SQLite backend. Translate each Postgres method's SQL into SQLite dialect per spec §6.3.

**Files:** every per-feature file in `core/persistence/sqlite/`:
- `nodes.go`, `instances.go`, `templates.go`, `template_tags.go`, `schedules.go`, `supervisors.go`, `events.go`, `node_attributes.go`, `store_lifecycle.go`, `lock_holders.go`, `claim_holders.go`, `frames.go`

For each file:

1. Open the corresponding Postgres impl (e.g., `core/persistence/postgres/nodes.go`).
2. Create the SQLite mirror (`core/persistence/sqlite/nodes.go`). Copy the method signatures verbatim (they're interface-defined). For each method:
   - Translate the SQL to SQLite dialect:
     - Parameter placeholders: pgx `$1, $2, ...` → SQLite `?, ?, ...`
     - `NOW()` → app-side `time.Now().UTC().Format(time.RFC3339Nano)` passed as a parameter
     - `gen_random_uuid()` → app-side `uuid.New()` passed as a parameter
     - `JSONB` columns: marshal `json.RawMessage` and `map[string]any` to TEXT before binding
     - Array columns: marshal `[]uuid.UUID` and `[]string` as JSON arrays to TEXT
     - Time columns: scan into a `string` first, then `time.Parse(time.RFC3339Nano, …)`
     - UUID columns: scan into a `string`, then `uuid.Parse(…)`
     - `RETURNING` clauses: SQLite ≥3.35 supports them; usable directly
     - `ON CONFLICT (col) DO UPDATE SET col = EXCLUDED.col` → `ON CONFLICT(col) DO UPDATE SET col = excluded.col` (lowercase `excluded`)
     - `SELECT ... FOR UPDATE` → omit the `FOR UPDATE` clause; the surrounding `BEGIN IMMEDIATE` (per `_txlock=immediate`) holds the writer slot for the entire tx
     - Postgres time arithmetic (`expires_at < NOW() - INTERVAL '5 seconds'`) → app-side compute the cutoff `time.Time`, format as RFC3339Nano, pass as a parameter; SQL is `WHERE expires_at < ?`

3. Apply the spec §6.3 type-handling rules consistently across every file. A small per-file helper for time formatting / UUID stringification reduces repetition; share via `core/persistence/sqlite/types.go` if needed.

4. The frame engine's per-method SQL (`frames.go`) follows the same pattern as the rest. Refer to `core/persistence/postgres/frames.go` for the source SQL.

5. Verify per file as you go: `go build ./core/persistence/sqlite/...`

After all 12 files land, the `*storeImpl` satisfies `persistence.Store`.

6. Add the per-feature accessor methods to `core/persistence/sqlite/backend.go`, mirroring Task 10's Postgres pattern:

   ```go
   func (s *storeImpl) Templates() persistence.TemplateStore         { return (*templatesImpl)(s) }
   func (s *storeImpl) TemplateTags() persistence.TemplateTagsStore  { return (*templateTagsImpl)(s) }
   func (s *storeImpl) Instances() persistence.InstanceStore         { return (*instancesImpl)(s) }
   func (s *storeImpl) StoreLifecycle() persistence.StoreLifecycleStore { return (*storeLifecycleImpl)(s) }
   func (s *storeImpl) Nodes() persistence.NodeStore                 { return (*nodesImpl)(s) }
   func (s *storeImpl) LockHolders() persistence.LockHoldersStore    { return (*lockHoldersImpl)(s) }
   func (s *storeImpl) NodeAttributes() persistence.NodeAttributesStore { return (*nodeAttributesImpl)(s) }
   func (s *storeImpl) ClaimHolders() persistence.ClaimHoldersStore  { return (*claimHoldersImpl)(s) }
   func (s *storeImpl) Events() persistence.EventStore               { return (*eventsImpl)(s) }
   func (s *storeImpl) Schedules() persistence.ScheduleStore         { return (*schedulesImpl)(s) }
   func (s *storeImpl) Supervisors() persistence.SupervisorStore     { return (*supervisorsImpl)(s) }
   func (s *storeImpl) Frames() persistence.FrameStore               { return (*framesImpl)(s) }
   ```

7. Update `core/persistence/sqlite/driver.go`'s `Store()` accessor: change `return nil` to `return d.s`.

**Verification:** Build passes; no unsatisfied interface errors.

---

### Task 35 — SQLite queue impl

**Goal:** Implement `Queue` for SQLite. Same translation pattern as Task 34.

**Files:** `core/persistence/sqlite/queue.go` (new)

1. Define `type queueImpl struct { db *sql.DB }` and `func newQueue(db *sql.DB) *queueImpl { return &queueImpl{db: db} }`.
2. Mirror `core/persistence/postgres/queue.go` method-for-method.
3. Translate SQL to SQLite dialect (see Task 34 rules).
4. Note: `SelectCandidates` uses `FOR UPDATE SKIP LOCKED` in Postgres. Under SQLite, omit `FOR UPDATE SKIP LOCKED` — the surrounding `BEGIN IMMEDIATE` already holds the single writer slot, so there's no contention to skip. Document inline.
5. The `EnqueueInTx` method added in Task 21: implement here. The auto-commit `Enqueue` calls `EnqueueInTx(ctx, req, nil)` and lets `q(nil)` return the DB.
6. Update `core/persistence/sqlite/driver.go`:
   - Add `q *queueImpl` field to `driver`.
   - Set `d.q = newQueue(db)` after `db.PingContext`.
   - Change `Queue()` accessor: `return d.q`.
7. Verify: `go build ./core/persistence/sqlite/...`

**Verification:** Build passes; `*queueImpl` satisfies `persistence.Queue`. The full SQLite driver now satisfies `persistence.Driver`.

---

### Task 36 — Postgres + SQLite per-driver smoke tests

**Goal:** Each driver gets a small integration test for things the conformance suite can't observe.

**Files:**
- `core/persistence/postgres/integration_test.go` (new)
- `core/persistence/sqlite/integration_test.go` (new)

1. **Postgres smoke** (`core/persistence/postgres/integration_test.go`):
   - Spin up a testcontainers Postgres via `core/internal/pgtest`.
   - Open a driver via `persistence.Open` with the testcontainer's DSN.
   - Run `Migrate(ctx)` and assert it returns nil.
   - Verify the driver implements `persistence.Driver` end-to-end: `_ persistence.Driver = d` compile-time assertion at the top of the test file.

   The pool-config introspection that earlier drafts of this plan suggested is not feasible — the `*pgxpool.Pool` is a private field of the `*driver` struct and cannot be reached from a test in `_test` package. If a future need arises to verify pool config, add a test-only accessor; v1 doesn't need it.

2. **SQLite smoke** (`core/persistence/sqlite/integration_test.go`):
   - Open a driver against a `t.TempDir()`-rooted DB file.
   - Run `Migrate(ctx)` and assert it returns nil.
   - Verify `_foreign_keys=ON`: insert a parent row + child row with FK; delete parent; assert child cascade-deleted.
   - Verify `_journal_mode=WAL`: query `PRAGMA journal_mode` via the underlying conn (or via raw `db.QueryRow("PRAGMA journal_mode").Scan(&mode)`).
   - Verify the startup banner is logged: capture slog output and assert it contains "SQLite driver is for local development only".

3. Verify: `go test ./core/persistence/postgres/... ./core/persistence/sqlite/... -count=1`

**Verification:** Both per-driver smokes pass.

---

### Task 37 — Conformance suite scaffolding

**Goal:** Lay down `core/persistence/conformance/` with the table-driven shape that runs against any driver factory.

**Files:**
- `core/persistence/conformance/conformance.go` (new)
- `core/persistence/conformance/conformance_test.go` (new)

1. Create `conformance.go`:

   ```go
   // Package conformance is the cross-driver test suite. Both Postgres and
   // SQLite drivers must pass every test here. Run via the per-driver
   // wrappers in conformance_test.go.
   //
   // Spec: §9.1.
   package conformance

   import (
       "context"
       "testing"

       "github.com/rimsky-ai/rimsky-core/core/persistence"
   )

   // Suite runs every conformance check against the driver returned by
   // factory. Each subtest is independent; factory is called once per
   // subtest so each gets a fresh DB.
   func Suite(t *testing.T, factory func(*testing.T) persistence.Driver) {
       t.Helper()
       t.Run("DispatchClaimRelease", func(t *testing.T) { testDispatchClaimRelease(t, factory(t)) })
       t.Run("VerifyBeforeRunRead", func(t *testing.T) { testVerifyBeforeRunRead(t, factory(t)) })
       t.Run("MigrationIdempotency", func(t *testing.T) { testMigrationIdempotency(t, factory(t)) })
       t.Run("CoordinatorSchedulerTick", func(t *testing.T) { testCoordinatorSchedulerTick(t, factory(t)) })
       t.Run("ForeignKeyCascade", func(t *testing.T) { testForeignKeyCascade(t, factory(t)) })
       t.Run("RegionByteEquality", func(t *testing.T) { testRegionByteEquality(t, factory(t)) })
       t.Run("OrphanCutoffTime", func(t *testing.T) { testOrphanCutoffTime(t, factory(t)) })
       t.Run("TxAtomicity", func(t *testing.T) { testTxAtomicity(t, factory(t)) })
       t.Run("AcquisitionTxAtomicity", func(t *testing.T) { testAcquisitionTxAtomicity(t, factory(t)) })
       t.Run("HeldClaimAutoTerminalSerialization", func(t *testing.T) { testHeldClaimAutoTerminalSerialization(t, factory(t)) })
       t.Run("SortOrderCoordination", func(t *testing.T) { testSortOrderCoordination(t, factory(t)) })
   }

   // Each test*(t, driver) function lives in a per-area file:
   // dispatch.go, verify.go, migrations.go, coordinator.go, fk.go,
   // region.go, orphan.go, tx.go, acquisition.go, auto_terminal.go,
   // sort_order.go. Each is filled in by Task 38.
   ```

2. Create `conformance_test.go`:

   ```go
   package conformance

   import (
       "context"
       "testing"

       "github.com/rimsky-ai/rimsky-core/core/persistence"
       "github.com/rimsky-ai/rimsky-core/core/internal/pgtest"

       _ "github.com/rimsky-ai/rimsky-core/core/persistence/postgres"
       _ "github.com/rimsky-ai/rimsky-core/core/persistence/sqlite"
   )

   func TestConformancePostgres(t *testing.T) {
       Suite(t, func(t *testing.T) persistence.Driver {
           // pgtest.OpenDriver returns a fresh testcontainers Postgres
           // wrapped in persistence.Driver, with migrations already applied.
           // (Verify this in pgtest.go from Task 15; if migrations aren't
           // run there, call d.Migrate(ctx) here before returning.)
           return pgtest.OpenDriver(context.Background(), t)
       })
   }

   func TestConformanceSQLite(t *testing.T) {
       Suite(t, func(t *testing.T) persistence.Driver {
           dir := t.TempDir()
           cfg := persistence.Config{
               Driver: "sqlite",
               SQLite: &persistence.SQLiteConfig{Path: dir + "/state.db"},
           }
           d, err := persistence.Open(context.Background(), cfg)
           if err != nil { t.Fatalf("open sqlite: %v", err) }
           t.Cleanup(func() { d.Close() })
           if err := d.Migrate(context.Background()); err != nil {
               t.Fatalf("migrate: %v", err)
           }
           return d
       })
   }
   ```

3. Add per-area placeholder files (`dispatch.go`, etc.) with empty test functions that pass — they'll be filled in in Task 38.

4. Verify: `go test ./core/persistence/conformance/... -count=1`

**Verification:** Suite runs (everything passes because tests are empty); both Postgres and SQLite get exercised.

---

### Task 38 — Conformance tests (fill in each area)

**Goal:** Implement the conformance test bodies per spec §9.1's coverage list.

**Files:** the placeholder per-area files from Task 37.

For each area, write a test that exercises the underlying invariant against the driver. Specific tests:

1. **DispatchClaimRelease** (`dispatch.go`):
   - Enqueue a dispatch row.
   - Two concurrent goroutines call `SelectCandidates` + `ClaimDispatchRow` inside a `Transaction`. Exactly one wins (`claimed=true`); the other gets `claimed=false`.
   - `ReleaseClaim` with the wrong supervisorID is a no-op (claimant guard).
   - `ListOrphanedClaims(ctx, cutoff)` returns rows with `last_heartbeat_at < cutoff` and not before.
   - Inv 4, inv 6.

2. **VerifyBeforeRunRead** (`verify.go`):
   - Claim a dispatch. Re-read via `GetClaimedBy` — returns current owner.
   - Manually clear the claim. Re-read via `GetClaimedBy` — returns "unclaimed".
   - Inv 5.

3. **MigrationIdempotency** (`migrations.go`):
   - Run `driver.Migrate(ctx)` twice — second run is a no-op (no rows added to `rimsky_migrations` beyond the first).
   - Two concurrent `Migrate(ctx)` calls in separate goroutines — both succeed; rows applied at most once.
   - Inv 8.

4. **CoordinatorSchedulerTick** (`coordinator.go`):
   - Two concurrent `TrySchedulerTick` in the same process: exactly one returns `held=true`.
   - Caveat for SQLite (per spec §9.1): documented in a comment that this only verifies same-process semantics.
   - Inv 7.

5. **ForeignKeyCascade** (`fk.go`):
   - Insert a `rimsky_lock_holders` row and a `rimsky_claim_holders` row referencing it.
   - Delete the lock-holder row.
   - Verify the claim-holder row is also gone.
   - Inv 13 (auto-terminal cleanup); also exercises `_foreign_keys=ON` under SQLite (without it, this test would fail).

6. **RegionByteEquality** (`region.go`):
   - Insert two `rimsky_lock_holders` rows with byte-identical `region_data`. The conflict predicate fires.
   - Insert two with byte-different `region_data`. No conflict.
   - Inv 14.

7. **OrphanCutoffTime** (`orphan.go`):
   - Insert a lock-holder row with `expires_at` in the past.
   - `LockHoldersStore.ListExpired(ctx, nil)` returns it.
   - Insert one with `expires_at` in the future. Not returned.

8. **TxAtomicity** (`tx.go`):
   - Inside a `Transaction(ctx, fn)`, do two INSERTs and have `fn` return an error. After return, neither row exists.
   - Same with successful return: both rows exist.

9. **AcquisitionTxAtomicity** (`acquisition.go`):
   - Inside a `Transaction`, claim a dispatch + insert a lock-holder + update its address. Roll back. Verify nothing landed.
   - Repeat with commit: everything landed.
   - Inv 10.

10. **HeldClaimAutoTerminalSerialization** (`auto_terminal.go`):
    - Insert a lock-holder + two claim-holder rows.
    - Two concurrent `Transaction`s call `LockHolders().LockForUpdate(ctx, id, tx)`. Verify they serialize (the second blocks until the first commits).
    - Inv 13.

11. **SortOrderCoordination** (`sort_order.go`):
    - In a `Transaction`, take three named locks via `Coordinator.TakeNamedLockInTx` in sorted order: a < b < c. Two concurrent goroutines do the same. Neither deadlocks.
    - Repeat with three region locks via `TakeRegionLockInTx`.
    - Inv 3, inv 10.

12. Verify: `go test ./core/persistence/conformance/... -count=1`

**Verification:** All conformance tests pass against both drivers.

---

### Task 39 — `RIMSKY_LOG_BINARY` plumbing

**Goal:** Each runtime binary reads `RIMSKY_LOG_BINARY` from the environment and adds it as a structured slog field. Used by the unified-image entrypoint to discriminate child logs.

**Files:**
- `core/cmd/rimsky-scheduler/main.go`
- `core/cmd/rimsky-supervisor/main.go`
- `core/cmd/rimsky-control-api/main.go`
- `core/cmd/rimsky-migrate/main.go`

For each `main.go`, find the existing `slog.SetDefault(slog.New(slog.NewJSONHandler(...)))` line and update:

```go
handler := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: parseLogLevel(...)})
logger := slog.New(handler)
if name := os.Getenv("RIMSKY_LOG_BINARY"); name != "" {
    logger = logger.With("binary", name)
}
slog.SetDefault(logger)
```

Verify: `go build ./core/cmd/...`

**Verification:** Build passes.

---

### Task 40 — `rimsky-entrypoint` binary

**Goal:** Create the in-tree process supervisor that runs `rimsky-migrate` then spawns the three runtime binaries with proper signal forwarding.

**Files:**
- `core/cmd/rimsky-entrypoint/main.go` (new)
- `core/cmd/rimsky-entrypoint/main_test.go` (new)

1. Create `main.go`:

   ```go
   // rimsky-entrypoint is the unified-image PID-1. Runs rimsky-migrate
   // synchronously, then spawns the three runtime binaries; forwards
   // SIGTERM/SIGINT; exits when any child exits or all clean up.
   //
   // Per spec §7.3.
   package main

   import (
       "fmt"
       "log/slog"
       "os"
       "os/exec"
       "os/signal"
       "sync"
       "syscall"
       "time"
   )

   const shutdownDeadline = 30 * time.Second

   var children = []string{"rimsky-scheduler", "rimsky-supervisor", "rimsky-control-api"}

   func main() {
       slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)).With("binary", "entrypoint"))

       // Step 1: migrate synchronously.
       slog.Info("running migrations")
       if err := runOnce("rimsky-migrate"); err != nil {
           slog.Error("migrate failed", "err", err)
           os.Exit(1)
       }
       slog.Info("migrations complete")

       // Step 2: spawn children.
       cmds := make([]*exec.Cmd, 0, len(children))
       exitCh := make(chan childExit, len(children))
       for _, name := range children {
           c := exec.Command("/usr/local/bin/" + name)
           c.Env = append(os.Environ(), "RIMSKY_LOG_BINARY="+nameOf(name))
           c.Stdout = os.Stdout
           c.Stderr = os.Stderr
           if err := c.Start(); err != nil {
               slog.Error("spawn failed", "binary", name, "err", err)
               killAll(cmds)
               os.Exit(1)
           }
           cmds = append(cmds, c)
           go func(c *exec.Cmd, name string) {
               err := c.Wait()
               exitCh <- childExit{name: name, err: err}
           }(c, name)
       }

       // Step 3: forward signals; wait for first exit.
       sigCh := make(chan os.Signal, 1)
       signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

       select {
       case sig := <-sigCh:
           slog.Info("received signal; shutting down", "signal", sig)
           shutdown(cmds, exitCh)
           os.Exit(0)
       case ce := <-exitCh:
           slog.Error("child exited unexpectedly", "binary", ce.name, "err", ce.err)
           shutdown(cmds, exitCh)
           os.Exit(exitCode(ce.err))
       }
   }

   func runOnce(binary string) error {
       c := exec.Command("/usr/local/bin/" + binary)
       c.Env = append(os.Environ(), "RIMSKY_LOG_BINARY="+binary)
       c.Stdout = os.Stdout
       c.Stderr = os.Stderr
       return c.Run()
   }

   func shutdown(cmds []*exec.Cmd, exitCh chan childExit) {
       var wg sync.WaitGroup
       for _, c := range cmds {
           if c.Process == nil { continue }
           _ = c.Process.Signal(syscall.SIGTERM)
       }
       deadline := time.After(shutdownDeadline)
       remaining := len(cmds)
       for remaining > 0 {
           select {
           case <-exitCh:
               remaining--
           case <-deadline:
               for _, c := range cmds { if c.Process != nil { _ = c.Process.Kill() } }
               wg.Wait()
               return
           }
       }
   }

   func killAll(cmds []*exec.Cmd) {
       for _, c := range cmds { if c.Process != nil { _ = c.Process.Kill() } }
   }

   func nameOf(binary string) string {
       // Strips "rimsky-" prefix; "rimsky-scheduler" → "scheduler"
       if len(binary) > 7 { return binary[7:] }
       return binary
   }

   func exitCode(err error) int {
       if err == nil { return 0 }
       if ee, ok := err.(*exec.ExitError); ok { return ee.ExitCode() }
       return 1
   }

   type childExit struct {
       name string
       err  error
   }
   ```

2. Create `main_test.go` with three tests:
   - **TestMigrateThenSpawn**: stub the binaries with `t.TempDir()` shell scripts that just `sleep`; verify the migrate stub runs first.
   - **TestSignalForwarding**: spawn the entrypoint as a subprocess, send SIGTERM, verify all children receive SIGTERM.
   - **TestChildCrashPropagation**: child exits non-zero before signal; entrypoint kills others and exits with same code.

   These need a small fixture-binary harness; use `os/exec` and `t.TempDir()` for the binaries.

3. Verify: `go test ./core/cmd/rimsky-entrypoint/... -count=1`

**Verification:** Build + tests pass.

---

### Task 41 — `Dockerfile.all` + default `rimsky-all.yml`

**Goal:** Build the unified image per spec §7.

**Files:**
- `deploy/Dockerfile.all` (new)
- `deploy/rimsky-all.yml` (new)

1. Create `deploy/Dockerfile.all`:

   ```dockerfile
   # Multi-stage build of rimsky/all — the unified development image.
   # Bundles all four binaries plus rimsky-entrypoint under a single PID 1.
   # Defaults to driver: sqlite with state at /var/lib/rimsky/state.db.
   #
   # Per spec §7.

   FROM golang:1.22 AS build
   WORKDIR /src
   COPY go.mod go.sum ./
   RUN go mod download
   COPY . .
   RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" \
         -o /out/rimsky-scheduler   ./core/cmd/rimsky-scheduler && \
       CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" \
         -o /out/rimsky-supervisor  ./core/cmd/rimsky-supervisor && \
       CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" \
         -o /out/rimsky-control-api ./core/cmd/rimsky-control-api && \
       CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" \
         -o /out/rimsky-migrate     ./core/cmd/rimsky-migrate && \
       CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" \
         -o /out/rimsky-entrypoint  ./core/cmd/rimsky-entrypoint

   FROM gcr.io/distroless/static-debian12:nonroot
   LABEL org.opencontainers.image.title="rimsky/all"
   LABEL org.opencontainers.image.description="Unified rimsky orchestrator (development convenience image; defaults to SQLite — not for production)"

   COPY --from=build /out/* /usr/local/bin/
   COPY deploy/rimsky-all.yml /etc/rimsky/rimsky.yml
   COPY deploy/supervisor-config.yml /etc/rimsky/supervisor-config.yml

   USER nonroot:nonroot
   EXPOSE 8080
   VOLUME /var/lib/rimsky
   ENV RIMSKY_CONFIG=/etc/rimsky/rimsky.yml \
       RIMSKY_SUPERVISOR_CONFIG=/etc/rimsky/supervisor-config.yml

   ENTRYPOINT ["/usr/local/bin/rimsky-entrypoint"]
   ```

2. Create `deploy/rimsky-all.yml`:

   ```yaml
   # Default rimsky.yml baked into rimsky/all. Operators override via:
   #   docker run -v ./my.yml:/etc/rimsky/rimsky.yml:ro rimsky/all
   #
   # Per spec §7.4.
   persistence:
     driver: sqlite
     sqlite:
       path: /var/lib/rimsky/state.db

   stores: {}
   named_locks: {}
   executors: {}
   ```

3. Update `deploy/build-images.sh` to include `Dockerfile.all`. Add a Makefile target `build-image-all`.

4. Verify (only if Docker available):
   ```sh
   docker build -f deploy/Dockerfile.all -t rimsky/all:test .
   docker run --rm -d --name rimsky-test -p 18080:8080 -v rimsky-test-state:/var/lib/rimsky rimsky/all:test
   sleep 3
   curl -f http://localhost:18080/health
   docker stop rimsky-test
   docker volume rm rimsky-test-state
   ```

**Verification:** `/health` returns 200 inside the container.

---

### Task 42 — Unified-image smoke test

**Goal:** Add `test/smoke/all/smoke_test.go` per spec §9.6, gated by `//go:build smoke`.

**Files:** `test/smoke/all/smoke_test.go` (new)

1. The smoke test verifies boot + health + clean shutdown only. End-to-end work execution (template register → deploy → instantiate → terminal) is exercised by the existing scenario suite in `test/scenarios/`; the unified image's job is "orchestrator-only with sensible defaults," not "end-to-end dev environment." Per spec §7.4, `stores: {}` and `executors: {}` are intentionally empty in the default config, so end-to-end work isn't possible without overriding the config.

2. Create:

   ```go
   //go:build smoke

   package all

   import (
       "fmt"
       "net/http"
       "os/exec"
       "strings"
       "testing"
       "time"
   )

   func TestUnifiedImage(t *testing.T) {
       if _, err := exec.LookPath("docker"); err != nil {
           t.Skip("docker unavailable")
       }

       // Build.
       buildCmd := exec.Command("docker", "build", "-f", "deploy/Dockerfile.all", "-t", "rimsky-all:smoke", "../../..")
       if out, err := buildCmd.CombinedOutput(); err != nil {
           t.Fatalf("docker build: %v\n%s", err, out)
       }

       runID := "rimsky-smoke-" + strings.ReplaceAll(time.Now().Format("150405.000"), ".", "")
       volID := runID + "-state"
       t.Cleanup(func() {
           _ = exec.Command("docker", "rm", "-f", runID).Run()
           _ = exec.Command("docker", "volume", "rm", volID).Run()
       })

       // Run with -p 0:8080 (random host port).
       runCmd := exec.Command("docker", "run", "--rm", "-d", "--name", runID,
           "-p", "0:8080", "-v", volID+":/var/lib/rimsky",
           "rimsky-all:smoke")
       if out, err := runCmd.CombinedOutput(); err != nil {
           t.Fatalf("docker run: %v\n%s", err, out)
       }

       // Find the host port.
       portCmd := exec.Command("docker", "port", runID, "8080/tcp")
       portOut, err := portCmd.Output()
       if err != nil { t.Fatalf("docker port: %v", err) }
       // Parse "0.0.0.0:54321" from the first line.
       firstLine := strings.SplitN(strings.TrimSpace(string(portOut)), "\n", 2)[0]
       parts := strings.Split(firstLine, ":")
       port := parts[len(parts)-1]

       // Poll /health.
       url := fmt.Sprintf("http://localhost:%s/health", port)
       deadline := time.Now().Add(30 * time.Second)
       for {
           resp, err := http.Get(url)
           if err == nil && resp.StatusCode == 200 {
               resp.Body.Close()
               break
           }
           if resp != nil { resp.Body.Close() }
           if time.Now().After(deadline) {
               logsOut, _ := exec.Command("docker", "logs", runID).CombinedOutput()
               t.Fatalf("/health did not return 200 within deadline\nlast err: %v\nlogs:\n%s", err, logsOut)
           }
           time.Sleep(500 * time.Millisecond)
       }

       // Verify the SQLite startup banner appears in container logs.
       logsOut, err := exec.Command("docker", "logs", runID).CombinedOutput()
       if err != nil { t.Fatalf("docker logs: %v", err) }
       if !strings.Contains(string(logsOut), "SQLite driver is for local development only") {
           t.Fatalf("startup banner missing from container logs:\n%s", logsOut)
       }

       // Stop and verify clean exit.
       if out, err := exec.Command("docker", "stop", runID).CombinedOutput(); err != nil {
           t.Fatalf("docker stop: %v\n%s", err, out)
       }
   }
   ```

3. Verify:
   ```sh
   go test -tags=smoke ./test/smoke/all/... -count=1
   ```
   (Skip is automatic if Docker isn't available.)

**Verification:** Smoke passes locally with Docker available.

---

### Task 43 — CLAUDE.md updates

**Goal:** Reflect the new package layout, invariant annotation paths, and gotchas per spec §10.5.

**Files:**
- `CLAUDE.md`
- `core/store/interface.go` (annotation text update; the file itself stays — `core/store/` is unaffected by this spec)

1. **Package import rules section.** Replace:
   - Delete the lines for `core/queue/`, `core/storage/`, `core/migrations/`.
   - Add: `core/persistence/` — driver protocol (interfaces, types, Tx) plus per-driver impls. Stdlib + `shared/` + `node/` for the protocol; `pgx/v5` allowed only inside `postgres/` subpackage; `modernc.org/sqlite` allowed only inside `sqlite/` subpackage.
   - Update note: `core/scheduler/`, `core/supervisor/`, `core/controlapi/`, `core/frame/` — pure logic; no `pgx`, `pgxpool`, or `pgconn` imports allowed.

2. **Blessed invariants section.** Update annotation paths per §10.5 of the spec. Specifically:
   - Inv 1 — change `core/storage/postgres/nodes.go:5,296` to `core/persistence/postgres/nodes.go`.
   - Inv 2 — change `core/queue/postgres/queue.go` to `core/persistence/postgres/queue.go`.
   - Inv 4 — change `core/queue/postgres/queue.go` to `core/persistence/postgres/queue.go`; the `core/supervisor/runner.go` and `core/scheduler/scheduler.go` annotations stay where they are.
   - Inv 7 — `core/scheduler/scheduler.go` annotation stays in place; the lock-acquisition site is now `coord.TrySchedulerTick`.
   - Inv 8 — change `core/migrations/runner.go` to `core/persistence/migrations.go`.
   - Inv 9a — change wording: "Lock state lives only in the persistence layer; `rimsky_lock_holders` is the sole authority."
   - Inv 19 — `Candidate.FrameID` annotation moves from `core/queue/interface.go` to `core/persistence/queue.go`.
   - Other invariants (10, 13, 14): annotation files unchanged; sites where the annotation lives didn't move.

3. **Gotchas section.** Add two new entries:
   - "SQLite is the dev-only driver. Multi-process / multi-host SQLite is not supported. The startup banner and operator-guide say so; do not 'fix' the banner to be quieter."
   - "The unified image (`rimsky/all`) bundles the three runtime processes under a single PID-1 entrypoint (`rimsky-entrypoint`). Running it with replicas > 1 creates independent SQLite databases — broken. Use the per-process images for multi-replica deployments."

4. **`RIMSKY_DB_URL` removal.** Find and update any reference to this env var (it no longer exists; replaced by `persistence.postgres.dsn` in `RIMSKY_CONFIG`).

5. Also update the `@blessed-invariant 9a` source-code annotation in `core/store/interface.go:29` to match the new wording (the file stays; just the annotation text changes).

6. Verify: visually inspect; run `make lint` to verify no broken cross-references.

**Verification:** `make lint` passes; CLAUDE.md text is internally consistent.

---

### Task 44 — `docs/architecture.md` and `docs/operator-guide.md` updates

**Goal:** Update architecture and operator docs per spec §10.6.

**Files:**
- `docs/architecture.md`
- `docs/operator-guide.md`

1. **`docs/architecture.md`:**
   - Find the "core/queue/, core/storage/" section. Replace with the new `core/persistence/` package layout.
   - Add a section "Persistence drivers" describing the driver protocol and the SQLite-as-dev-only posture.
   - Remove references to the old `core/migrations/` package.

2. **`docs/operator-guide.md`:**
   - Add a new "Persistence drivers" section:
     - Postgres for production; SQLite for development.
     - The `persistence:` block schema (full example).
     - The `RIMSKY_DB_URL` env var has been removed; use `persistence.postgres.dsn` in `rimsky.yml`.
   - Add a new "Unified Docker image" section:
     - `docker run --rm -p 8080:8080 -v rimsky-state:/var/lib/rimsky rimsky/all`.
     - Override paths (`-v` for config; `-e RIMSKY_CONFIG`; `--entrypoint` for single-binary mode).
     - Volume layout (`/var/lib/rimsky`).
     - SQLite limitations (single-host, dev-only).

3. Verify: visually inspect.

**Verification:** Docs read coherently; no internal contradictions.

---

### Task 45 — Final integration check

**Goal:** Top-to-bottom verification that the full system works.

**Files:** none new.

1. Run the full build: `go build ./...`
2. Run the full test suite: `go test ./... -count=1`
3. Run the lint: `make lint`
4. Run the testcontainers scenario suite: `go test ./test/scenarios/... -count=1`
5. Run the conformance suite: `go test ./core/persistence/conformance/... -count=1`
6. Bring up the existing docker-compose stack and verify `/health`:
   ```sh
   docker compose -f deploy/docker-compose.yml down -v
   docker compose -f deploy/docker-compose.yml up -d --build
   sleep 5
   curl -f http://localhost:8080/health
   docker compose -f deploy/docker-compose.yml down -v
   ```
7. Build and boot the unified image, verify `/health`:
   ```sh
   docker build -f deploy/Dockerfile.all -t rimsky/all:test .
   docker run --rm -d --name rimsky-all-test -p 18080:8080 -v rimsky-all-test:/var/lib/rimsky rimsky/all:test
   sleep 5
   curl -f http://localhost:18080/health
   docker stop rimsky-all-test
   docker volume rm rimsky-all-test
   ```
8. Verify the SQLite startup banner appears in the unified-image logs:
   ```sh
   docker run --rm -d --name rimsky-banner-test -v rimsky-banner-test:/var/lib/rimsky rimsky/all:test
   sleep 3
   docker logs rimsky-banner-test 2>&1 | grep -i "sqlite driver is for local development only"
   docker stop rimsky-banner-test
   docker volume rm rimsky-banner-test
   ```
9. Verify no stray pgx imports outside the sanctioned packages (matching the depguard exclusions from Task 28):
   ```sh
   grep -rln "github.com/jackc/pgx" --include='*.go' | grep -v _test.go | \
       grep -v "core/persistence/postgres/" | \
       grep -v "core/cmd/" | \
       grep -v "core/internal/pgtest/" | \
       grep -v "core/scenario/" | \
       grep -v "stores/" | \
       grep -v "test/smoke/"
   ```
   Expected output: nothing.

**Verification:** Every check passes.

---

## Manual checks after completion

These are not part of the automated run; run through them after the implementation and review are clean:

1. **Visual smoke of the unified-image container logs.** Confirm the structured slog output is interleaved cleanly across the three children with the `binary` field discriminating each.

2. **First-touch experience.** Run `docker run --rm -p 8080:8080 rimsky/all` from a clean shell with no other rimsky setup. Verify it boots, the SQLite banner is loud, `/health` works, and the container stops cleanly with `docker stop`.

3. **Operator-doc walkthrough.** Read `docs/operator-guide.md` "Persistence drivers" and "Unified Docker image" sections from the perspective of a new operator. Confirm the steps work as written.

4. **Helm chart status.** Note in passing whether the existing chart at `deploy/kubernetes/rimsky-chart/` still works against the changes; the spec defers a chart refresh, but if it's now actively broken, file a follow-up.
