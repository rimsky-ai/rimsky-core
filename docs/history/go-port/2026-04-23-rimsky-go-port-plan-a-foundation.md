# Rimsky Go Port — Plan A — Foundation

**Goal:** Stand up the Go rimsky orchestrator as a working end-to-end system: the single Go module (`github.com/fallguy/rimsky/core`), the `proto/v1` protocol with generated Go code, migrations, all storage interfaces with Postgres implementations, the pure-logic layer (state machine, policy evaluator, template validator, quality rules, backoff, cron), the dispatch queue, the scheduler process, the supervisor process speaking the cell-executor protocol over gRPC+HTTP, the Resource interface with the `inline-jsonb` reference implementation, a stub executor for end-to-end validation, the HTTP control API, library entry points, reference env-var binaries, and a scenario-test suite demonstrating all load-bearing invariants.

**End state after this plan:** a developer can `cd rimsky-go && go test ./...` and see every scenario pass. `docker-compose`-equivalent stand-up is out of scope (that's Plan C), but the three reference binaries can be started manually with a Postgres URL and a stub-executor endpoint, and they talk to each other correctly.

**Architecture:** single Go module under `rimsky-go/core`, feature-first package layout enforced by import-graph rules (spec §4.1), Postgres via `pgx/v5`, gRPC via `google.golang.org/grpc`, HTTP via `chi`, logging via stdlib `log/slog`, config via `koanf`, testing via `testify` + `testcontainers-go`, schema validation via `gojsonschema`, cron via `robfig/cron/v3`.

**Tech stack:** Go 1.22+, Postgres 14+, `pgx/v5`, `grpc-go`, `protoc-gen-go`, `protoc-gen-go-grpc`, `chi/v5`, `koanf/v2`, `log/slog`, `xeipuuv/gojsonschema`, `robfig/cron/v3`, `stretchr/testify`, `testcontainers/testcontainers-go`, `uuid`.

**Reference documents:**
- Spec: `docs/specs/2026-04-23-rimsky-go-port-design.md`
- TS reference (unchanged; ported semantically, not syntactically): `rimsky/src/`
- Execution chain: `docs/plans/2026-04-23-rimsky-go-port-execution-chain.md`

**Key TS source files whose semantics this plan ports (file-by-file):**
- `rimsky/src/cell/state-machine.ts` → `rimsky-go/core/node/state.go` (rename cell→node; preserve no-same-state-short-circuit invariant)
- `rimsky/src/cell/policy-evaluator.ts` → `rimsky-go/core/node/policy.go` (pure port; rename; add quality-failed error class)
- `rimsky/src/cell/template.ts` + `template-validator.ts` → `rimsky-go/core/node/template.go` + `template_validator.go`
- `rimsky/src/queue/postgres-queue.ts` → `rimsky-go/core/queue/postgres.go` (preserve all blessed invariants)
- `rimsky/src/scheduler/*.ts` → `rimsky-go/core/scheduler/*.go`
- `rimsky/src/storage/postgres/*.ts` → `rimsky-go/core/storage/postgres/*.go` (8 stores; rename cells→nodes; replace timer_store with schedule_store)
- `rimsky/src/supervisor/supervisor.ts` + `deterministic-runner.ts` + `commit.ts` + `on-error.ts` + `terminal-outcome.ts` → `rimsky-go/core/supervisor/*.go` (the agentic-runner and callback-mcp do NOT port — they become the `claude-agent` executor in Plan B)
- `rimsky/src/control-api/*.ts` → `rimsky-go/core/controlapi/*.go`
- `rimsky/src/resource/quality-rules.ts` → `rimsky-go/core/qualityrule/rules.go`

---

## Module layout this plan produces

```
rimsky-go/
├── go.mod                                  # module github.com/fallguy/rimsky/core
├── go.sum
├── core/
│   ├── shared/
│   │   ├── types.go                        # UUID alias, NodeKind removed, NodeState, TransitionReason, etc.
│   │   ├── errors.go                       # RimskyError + typed subclasses
│   │   ├── clock.go                        # Clock interface, SystemClock, ControllableClock
│   │   ├── logger.go                       # Logger interface wrapping slog; Silent, Capturing impls
│   │   └── config.go                       # shared config shapes (AuthenticatorConfig, etc.)
│   ├── node/
│   │   ├── template.go                     # CellTemplateSpec → NodeTemplateSpec types
│   │   ├── template_validator.go
│   │   ├── template_validator_test.go
│   │   ├── state.go                        # state machine
│   │   ├── state_test.go
│   │   ├── policy.go                       # policy evaluator
│   │   └── policy_test.go
│   ├── qualityrule/
│   │   ├── spec.go                         # QualityRuleSpec, QualityRuleError
│   │   ├── rules.go                        # builtin evaluators
│   │   └── rules_test.go
│   ├── message/
│   │   └── types.go                        # Message type, params shapes
│   ├── resource/
│   │   ├── interface.go                    # Resource, Factory, Config, Registry
│   │   ├── errors.go                       # ErrRollbackUnsupported etc.
│   │   ├── register.go                     # factory registry helpers
│   │   └── inlinejsonb/
│   │       ├── resource.go
│   │       ├── factory.go
│   │       └── resource_test.go
│   ├── queue/
│   │   ├── interface.go                    # DispatchQueue
│   │   └── postgres/
│   │       ├── queue.go
│   │       └── queue_test.go
│   ├── storage/
│   │   ├── interfaces.go                   # StorageBackend + all sub-store interfaces
│   │   └── postgres/
│   │       ├── backend.go
│   │       ├── templates.go
│   │       ├── instances.go
│   │       ├── nodes.go                    # NodeStore
│   │       ├── resources.go                # ResourceRegistry (identity + version pointers)
│   │       ├── resource_data.go            # ResourceDataStore (inline-jsonb read/delete)
│   │       ├── events.go
│   │       ├── schedules.go                # replaces timer_store
│   │       ├── supervisors.go
│   │       └── postgres_test.go            # per-store integration tests (one file per store OK)
│   ├── executor/
│   │   ├── client.go                       # protocol-client helper (wraps generated gRPC client)
│   │   ├── client_http.go                  # HTTP+JSON bridge client
│   │   └── resolver.go                     # supervisor-config executor-name → endpoint resolver
│   ├── scheduler/
│   │   ├── scheduler.go                    # main loop
│   │   ├── scheduler_test.go
│   │   ├── schedule_ticker.go              # cron-driven invalidates (replaces timer-ticker)
│   │   ├── schedule_ticker_test.go
│   │   ├── pure_cascade.go                 # stale→fresh inline transition for no-executor nodes
│   │   ├── pure_cascade_test.go
│   │   ├── invalidate.go                   # invalidateNode helper
│   │   ├── recalculate.go                  # recalculateNode helper
│   │   ├── backoff.go
│   │   └── backoff_test.go
│   ├── supervisor/
│   │   ├── supervisor.go                   # main loop, claim → verify → dispatch → outcome → complete
│   │   ├── supervisor_test.go
│   │   ├── runner.go                       # one-function runner: call executor, map response to terminal outcome
│   │   ├── runner_test.go
│   │   ├── commit.go                       # commit flow (through Resource.Commit)
│   │   ├── commit_test.go
│   │   ├── on_error.go                     # policy-chain evaluation + action application
│   │   ├── on_error_test.go
│   │   └── terminal_outcome.go             # map Complete/Blocked/Errored/AsyncAccepted to state changes
│   ├── controlapi/
│   │   ├── app.go                          # chi router, middleware, error handler
│   │   ├── schemas.go                      # request/response shapes with go-playground/validator
│   │   ├── templates.go                    # POST /templates, etc.
│   │   ├── instances.go
│   │   ├── nodes.go                        # GET /nodes/:id, operator overrides
│   │   ├── events.go
│   │   ├── resources.go
│   │   ├── health.go
│   │   ├── auth.go                         # Authenticator interface (default no-op)
│   │   ├── redact.go                       # params_redact application
│   │   └── app_test.go                     # end-to-end HTTP tests
│   ├── migrations/
│   │   ├── embed.go                        # //go:embed migrations/*.sql
│   │   ├── runner.go                       # migration runner with advisory lock
│   │   ├── runner_test.go
│   │   └── 001-initial.sql
│   ├── config/
│   │   ├── scheduler.go                    # SchedulerConfig + StartScheduler
│   │   ├── supervisor.go                   # SupervisorConfig + StartSupervisor
│   │   └── controlapi.go                   # ControlAPIConfig + StartControlAPI
│   └── cmd/
│       ├── rimsky-scheduler/main.go
│       ├── rimsky-supervisor/main.go
│       ├── rimsky-control-api/main.go
│       └── rimsky-migrate/main.go
├── proto/
│   └── v1/
│       ├── node_executor.proto
│       ├── events.proto
│       ├── buf.yaml                        # if we use buf; else Makefile targets for protoc
│       ├── buf.gen.yaml
│       └── gen/                            # generated Go code committed to repo
│           ├── node_executor.pb.go
│           ├── node_executor_grpc.pb.go
│           └── events.pb.go
├── executors/
│   └── stub/                               # minimal in-process stub executor for Plan A scenario tests
│       ├── stub.go                         # implements cell-executor protocol server
│       └── stub_test.go
├── CHANGELOG.md
├── README.md
├── .golangci.yml
├── Makefile                                # convenience: proto-gen, lint, test, build
└── docs/                                   # placeholder directory; populated in Plan D
    └── .gitkeep
```

---

## Phase 1 — Module scaffold

### Task 1.1 — Create `rimsky-go/` with `go.mod` and top-level files

**Files:**
- `rimsky-go/go.mod`
- `rimsky-go/.gitignore`
- `rimsky-go/README.md`
- `rimsky-go/CHANGELOG.md`
- `rimsky-go/Makefile`
- `rimsky-go/.golangci.yml`

**Steps:**

1. Create `rimsky-go/` directory.
2. `cd rimsky-go && go mod init github.com/fallguy/rimsky/core`. This produces a minimal `go.mod`. Set Go version 1.22 explicitly: `go 1.22` line in `go.mod`.
3. Add dependencies (resolved in later tasks; add placeholders now so `go mod tidy` later picks them up):
   ```bash
   cd rimsky-go
   go get github.com/jackc/pgx/v5@latest
   go get google.golang.org/grpc@latest
   go get google.golang.org/protobuf@latest
   go get github.com/go-chi/chi/v5@latest
   go get github.com/knadh/koanf/v2@latest
   go get github.com/knadh/koanf/providers/env/v2@latest
   go get github.com/knadh/koanf/providers/file@latest
   go get github.com/knadh/koanf/parsers/yaml@latest
   go get github.com/xeipuuv/gojsonschema@latest
   go get github.com/robfig/cron/v3@latest
   go get github.com/google/uuid@latest
   go get github.com/stretchr/testify@latest
   go get github.com/testcontainers/testcontainers-go@latest
   go get github.com/testcontainers/testcontainers-go/modules/postgres@latest
   ```
4. Write `rimsky-go/.gitignore`:
   ```
   /bin/
   /tmp/
   *.test
   *.out
   vendor/
   .idea/
   .vscode/
   ```
5. Write `rimsky-go/README.md` (minimal; the full README is Plan D's concern):
   ```markdown
   # Rimsky (Go)

   Project-agnostic reactive node-graph orchestration platform.

   This is the Go port of the TypeScript prototype at `../rimsky/`. See `../docs/specs/2026-04-23-rimsky-go-port-design.md` for the full design.

   ## Development

       make proto-gen
       go test ./...
       go build ./cmd/...
   ```
6. Write `rimsky-go/CHANGELOG.md`:
   ```markdown
   # Changelog

   ## Unreleased

   - Initial Go port scaffold.
   ```
7. Write `rimsky-go/Makefile`:
   ```makefile
   .PHONY: proto-gen test build lint tidy

   proto-gen:
   	cd proto/v1 && protoc --go_out=gen --go_opt=paths=source_relative \
   	  --go-grpc_out=gen --go-grpc_opt=paths=source_relative \
   	  node_executor.proto events.proto

   test:
   	go test ./...

   build:
   	go build ./...

   lint:
   	golangci-lint run

   tidy:
   	go mod tidy
   ```
8. Write `rimsky-go/.golangci.yml`:
   ```yaml
   linters:
     enable:
       - gofmt
       - goimports
       - govet
       - staticcheck
       - unused
       - ineffassign
       - errcheck
       - revive
   linters-settings:
     revive:
       rules:
         - name: exported
           disabled: true   # we document packages elsewhere; not enforcing per-symbol doc comments
   ```

**Verification:**
- `cd rimsky-go && go mod tidy` exits 0.
- `ls rimsky-go/` shows all files above.

---

### Task 1.2 — Create directory skeleton under `core/`, `proto/v1/`, `executors/stub/`, `docs/`

**Files:**
- All directories from the module layout (empty directories via `.gitkeep` files where needed).

**Steps:**

1. Create each subdirectory under `rimsky-go/core/` listed in the module layout.
2. Create `rimsky-go/proto/v1/` and `rimsky-go/proto/v1/gen/`.
3. Create `rimsky-go/executors/stub/`.
4. Create `rimsky-go/docs/` with a `.gitkeep`.
5. Create a placeholder `rimsky-go/core/doc.go`:
   ```go
   // Package core is the rimsky orchestrator module root. Sub-packages
   // implement the node graph, dispatch queue, scheduler, supervisor,
   // control API, storage, migrations, and shared utilities. See the
   // design spec for package-level responsibilities and the import-graph
   // rules that enforce three-collection separation.
   package core
   ```

**Verification:**
- `cd rimsky-go && go build ./...` exits 0 (only `core/doc.go` exists; build produces nothing but shouldn't error).
- `find rimsky-go -type d | sort` shows the full tree.

---

## Phase 2 — Shared utilities

### Task 2.1 — `core/shared/types.go`

**Files:** `rimsky-go/core/shared/types.go`

**Steps:**

1. Port shared type definitions from `rimsky/src/shared/types.ts`. Rename cell→node throughout.
2. Define:
   ```go
   package shared

   import "github.com/google/uuid"

   type UUID = uuid.UUID

   // NodeState: fresh | stale | running | failed
   type NodeState string
   const (
       NodeStateFresh   NodeState = "fresh"
       NodeStateStale   NodeState = "stale"
       NodeStateRunning NodeState = "running"
       NodeStateFailed  NodeState = "failed"
   )

   type Severity string
   const (
       SeverityError   Severity = "error"
       SeverityWarning Severity = "warning"
   )

   type BackoffKind string
   const (
       BackoffLinear      BackoffKind = "linear"
       BackoffExponential BackoffKind = "exponential"
   )

   type JitterKind string
   const (
       JitterNone      JitterKind = "none"
       JitterPlusMinus JitterKind = "plus_minus"
   )

   type AccessKind string
   const (
       AccessInline AccessKind = "inline"
       AccessSQL    AccessKind = "sql"
       AccessMCP    AccessKind = "mcp"
       AccessREST   AccessKind = "rest"
   )

   type MessageType string
   const (
       MessageInvalidate  MessageType = "invalidate"
       MessageRecalculate MessageType = "recalculate"
   )

   // DispatchRow is the claimable unit of work (see spec §11.1).
   type DispatchRow struct {
       ID              UUID
       NodeID          UUID
       ExecutorName    string   // empty for pure-cascade (but those never enqueue; present for defensive parity)
       ConcurrencyTags []string
       EnqueuedAt      time.Time
       ClaimedBy       *string
       ClaimedAt       *time.Time
   }

   // ResourcePath renders as "a:b:c" for display.
   func RenderResourcePath(segs []string) string { return strings.Join(segs, ":") }
   ```
3. Note: the Go port has no `NodeKind` concept (spec §5.1 eliminated the discriminator). Do not port `CellKind`.

**Verification:**
- `cd rimsky-go && go build ./core/shared/...` exits 0.

---

### Task 2.2 — `core/shared/errors.go`

**Files:** `rimsky-go/core/shared/errors.go`

**Steps:**

1. Port the typed error hierarchy from `rimsky/src/shared/errors.ts`. Use Go idiom (sentinel errors + typed wrappers via `fmt.Errorf`-style).
2. Define:
   ```go
   package shared

   import "errors"

   var (
       ErrTemplateValidation   = errors.New("template validation failed")
       ErrTemplateNotFound     = errors.New("template not found")
       ErrInstanceNotFound     = errors.New("instance not found")
       ErrNodeNotFound         = errors.New("node not found")
       ErrConsumerKeyConflict  = errors.New("consumer_key already exists for template")
       ErrTemplateInUse        = errors.New("template has live instances")
       ErrNodeRunning          = errors.New("node is currently running")
       ErrNodeApplication      = errors.New("node application error")   // application-level error class
       ErrIllegalTransition    = errors.New("illegal state transition") // blessed-invariant (§17)
       ErrRollbackUnsupported  = errors.New("rollback unsupported by resource implementation")
       ErrExecutorNotFound     = errors.New("executor not found in supervisor config")
   )

   // RimskyError wraps a sentinel with context fields for structured logging.
   type RimskyError struct {
       Err     error
       Message string
       Fields  map[string]any
   }
   func (e *RimskyError) Error() string { return e.Message + ": " + e.Err.Error() }
   func (e *RimskyError) Unwrap() error { return e.Err }

   func Wrap(err error, message string, fields map[string]any) *RimskyError {
       return &RimskyError{Err: err, Message: message, Fields: fields}
   }
   ```

**Verification:** `go build ./core/shared/...` exits 0.

---

### Task 2.3 — `core/shared/clock.go` with unit tests

**Files:**
- `rimsky-go/core/shared/clock.go`
- `rimsky-go/core/shared/clock_test.go`

**Steps:**

1. Port `rimsky/src/shared/clock.ts`. Implement:
   ```go
   type Clock interface {
       Now() time.Time
       Sleep(ctx context.Context, d time.Duration) error
   }

   type SystemClock struct{}
   func (SystemClock) Now() time.Time { return time.Now() }
   func (SystemClock) Sleep(ctx context.Context, d time.Duration) error {
       t := time.NewTimer(d)
       defer t.Stop()
       select {
       case <-ctx.Done(): return ctx.Err()
       case <-t.C: return nil
       }
   }

   // ControllableClock: test clock that can be advanced deterministically.
   // Pending sleeps resolve in order of their deadline when Advance crosses them.
   type ControllableClock struct {
       mu       sync.Mutex
       t        time.Time
       pending  []pendingSleep
   }
   type pendingSleep struct {
       due  time.Time
       done chan struct{}
   }
   func NewControllableClock(start time.Time) *ControllableClock { ... }
   func (c *ControllableClock) Now() time.Time { ... }
   func (c *ControllableClock) Sleep(ctx context.Context, d time.Duration) error { ... }
   func (c *ControllableClock) Advance(d time.Duration) { ... }
   func (c *ControllableClock) SetNow(t time.Time) { ... }
   ```
   Port the TS "step forward through due deadlines, flush, yield microtasks, finalize" semantics to Go using channel closes + runtime.Gosched().
2. Write unit tests covering: Now() returns set time; Sleep blocks until Advance crosses the deadline; multiple Sleepers resolve in deadline order; ctx cancellation wakes Sleep early.

**Verification:**
- `go test ./core/shared/... -run TestClock` exits 0 with all tests passing.

---

### Task 2.4 — `core/shared/logger.go`

**Files:** `rimsky-go/core/shared/logger.go`

**Steps:**

1. Port `rimsky/src/shared/logger.ts`. Wrap stdlib `log/slog` so the Logger interface matches TS shape (Debug/Info/Warn/Error + Child).
2. Define:
   ```go
   type Logger interface {
       Debug(msg string, fields ...any)
       Info(msg string, fields ...any)
       Warn(msg string, fields ...any)
       Error(msg string, fields ...any)
       With(fields ...any) Logger
   }

   type slogLogger struct { l *slog.Logger }
   func NewSlogLogger(l *slog.Logger) Logger { return &slogLogger{l} }
   // ...methods delegate to l.Info/Debug/etc. and With() returns slogLogger{l.With(...)}.

   type SilentLogger struct{}
   func (SilentLogger) Debug(string, ...any) {}
   // ...all no-ops.

   type CapturingLogger struct {
       mu      sync.Mutex
       records []Record
   }
   type Record struct {
       Level  string
       Msg    string
       Fields map[string]any
   }
   func (c *CapturingLogger) Records() []Record { ... }
   func (c *CapturingLogger) Clear() { ... }
   ```
3. No unit tests — simple delegation; exercised indirectly by downstream tests.

**Verification:** `go build ./core/shared/...` exits 0.

---

## Phase 3 — Migrations

### Task 3.1 — Write `core/migrations/001-initial.sql`

**Files:** `rimsky-go/core/migrations/001-initial.sql`

**Steps:**

1. Port the TS v1 schema from `rimsky/src/migrations/001-initial.sql` with the Go-port schema deltas from spec §11.1:
   - `rimsky_cells` → `rimsky_nodes`. Drop `kind TEXT`. Replace with `executor TEXT` (nullable) + `schedule_cron TEXT` (nullable).
   - Rename `rimsky_timers` → `rimsky_schedules` with columns `(node_id UUID PRIMARY KEY, cron_expr TEXT NOT NULL, next_fire_at TIMESTAMPTZ NOT NULL, last_fired_at TIMESTAMPTZ)`.
   - `rimsky_dispatch.cell_kind TEXT` → `rimsky_dispatch.executor_name TEXT NOT NULL`.
   - `rimsky_supervisors.accepts TEXT[]` → `rimsky_supervisors.accepted_executors TEXT[] NOT NULL`.
   - All column/index names with `cell` become `node`.
   - Keep the `rimsky_migrations` tracker table.
   - Keep advisory-lock + idempotent-IF-NOT-EXISTS style (the TS migration is a good model).
2. Include all 8 tables: `rimsky_templates`, `rimsky_instances`, `rimsky_nodes`, `rimsky_resources`, `rimsky_resource_versions`, `rimsky_supervisors`, `rimsky_dispatch`, `rimsky_events`, `rimsky_schedules`. (That's 9 tables plus `rimsky_migrations` = 10 total.)
3. Include the `rimsky_resource_versions.produced_by` `ON DELETE SET NULL` from TS 002-produced-by-on-delete-set-null.sql — fold into 001.

**Verification:**
- Not verifiable yet (runner comes next). Lint the SQL for obvious syntax errors by eye.

---

### Task 3.2 — Write `core/migrations/runner.go` + `embed.go` + unit test

**Files:**
- `rimsky-go/core/migrations/embed.go`
- `rimsky-go/core/migrations/runner.go`
- `rimsky-go/core/migrations/runner_test.go`

**Steps:**

1. `embed.go`:
   ```go
   package migrations
   import "embed"
   //go:embed *.sql
   var FS embed.FS
   ```
2. `runner.go`: port `rimsky/src/migrations/runner.ts` to Go. Use `pgx/v5`.
   ```go
   func Run(ctx context.Context, pool *pgxpool.Pool, log shared.Logger) error {
       // Acquire session advisory lock (key = some fixed int64 for rimsky migrations).
       // Create rimsky_migrations table if not exists.
       // Read all *.sql from FS in filename order.
       // For each unapplied: execute in transaction, record in rimsky_migrations.
       // Release lock, return.
   }
   ```
   Advisory lock key: pick a fixed `int64`, document in a comment as "rimsky migration lock" and never reuse.
3. `runner_test.go`: spin up testcontainers Postgres, run migrations, verify `rimsky_nodes` exists, re-run, verify "no migrations to apply" (idempotent). Use the harness helpers from Task 6.1 (written next phase) — if this runs first, write a minimal inline Postgres setup here and refactor later.

**Verification:**
- `go test ./core/migrations/...` exits 0; the integration test successfully applies the migration and is idempotent on re-run.

---

### Task 3.3 — Add `cmd/rimsky-migrate/main.go`

**Files:** `rimsky-go/core/cmd/rimsky-migrate/main.go`

**Steps:**

1. Write a tiny CLI that reads `RIMSKY_DB_URL` and calls `migrations.Run`. Exit codes: 0 on success, 1 on any failure.

**Verification:**
- `go build ./core/cmd/rimsky-migrate/` produces `./rimsky-migrate` binary.
- `./rimsky-migrate` with a blank env errors cleanly with a message about missing `RIMSKY_DB_URL`.

---

## Phase 4 — Protocol generation

### Task 4.1 — Write `proto/v1/node_executor.proto`

**Files:** `rimsky-go/proto/v1/node_executor.proto`

**Steps:**

1. Author the full `.proto` from spec §7.2. Use `package rimsky.v1;`. Use `option go_package = "github.com/fallguy/rimsky/core/proto/v1/gen;genv1";` so the generated Go package lives inside the module.
2. Define all messages: `ExecuteRequest`, `ExecuteEvent` (oneof with `Heartbeat | Complete | Blocked | Errored | AsyncAccepted`), each message body, plus supporting imports (`google/protobuf/struct.proto`, `google/protobuf/timestamp.proto`).
3. Define the `CellExecutor` service — but rename to `NodeExecutor` per §1.5 rename. Full service:
   ```protobuf
   service NodeExecutor {
     // See spec §7.2 for semantics. The stream carries zero or more
     // Heartbeat events followed by EXACTLY ONE terminal event
     // (Complete | Blocked | Errored | AsyncAccepted); executor MUST
     // close the stream immediately after the terminal event.
     rpc Execute(ExecuteRequest) returns (stream ExecuteEvent);
   }
   ```
4. Add google.api.http annotations for the HTTP+JSON bridge? v1 scope: no grpc-gateway dependency. Instead, the HTTP+JSON bridge is a hand-written chi route in the executor's own package (Plan B's http-node example), and the `executor/client_http.go` in Plan A speaks the HTTP-JSON form directly. The `.proto` itself carries no HTTP annotations for v1.

**Verification:**
- `protoc --version` works on dev machine.
- Not yet verifiable; next task runs generation.

---

### Task 4.2 — Write `proto/v1/events.proto`

**Files:** `rimsky-go/proto/v1/events.proto`

**Steps:**

1. Enumerate every event kind from spec §11.2 ("Kept (core)" plus the additions `schedule_fired`, `unresolved_executor`, `pure_cascade_commit`). For each, define a message with the payload fields from spec §11.2.
2. Define a top-level `Event` message with a `oneof` of kind-specific payloads plus common fields (`id`, `instance_id`, `node_id`, `occurred_at`).
3. Scope: these are for consumers who want typed event payloads. Rimsky's internal code uses `map[string]any` against `rimsky_events.payload` JSONB; the proto is for external readers.

**Verification:** same as 4.1.

---

### Task 4.3 — Generate Go code + `buf.gen.yaml`

**Files:**
- `rimsky-go/proto/v1/buf.yaml` (optional — if we're using buf)
- `rimsky-go/proto/v1/buf.gen.yaml` (optional)
- `rimsky-go/proto/v1/gen/*.pb.go` (generated; committed)

**Steps:**

1. Install required generators:
   ```bash
   go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
   go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
   ```
2. Run `make proto-gen` from `rimsky-go/`. Commit the generated `*.pb.go` and `*_grpc.pb.go` files under `proto/v1/gen/`.
3. Add a one-line comment at the top of each generated file (post-generation; do not hand-edit the body):
   ```go
   // Code generated by protoc. DO NOT EDIT. Regenerate with `make proto-gen`.
   ```
   (protoc already adds this; verify it's there.)

**Verification:**
- `go build ./proto/v1/gen/...` exits 0.
- `ls proto/v1/gen/` shows `node_executor.pb.go`, `node_executor_grpc.pb.go`, `events.pb.go`.
- Committing generated code is intentional (so consumers importing the module don't need protoc locally).

---

## Phase 5 — Pure logic

### Task 5.1 — `core/node/state.go` + tests

**Files:**
- `rimsky-go/core/node/state.go`
- `rimsky-go/core/node/state_test.go`

**Steps:**

1. Port `rimsky/src/cell/state-machine.ts` semantically. Rename cell→node. Keep all transition-reason kinds.
2. Define:
   ```go
   package node

   type TransitionReason struct {
       Kind string
       // optional context fields per spec §4.1 of TS v1; match to the Go port's §5.3
   }
   var (
       ReasonInvalidateReceived = TransitionReason{Kind: "invalidate_received"}
       ReasonDispatchClaimed    = TransitionReason{Kind: "dispatch_claimed"}
       ReasonWorkCompleted      = TransitionReason{Kind: "work_completed"}
       ReasonPolicyRetry        = TransitionReason{Kind: "policy_retry"}
       ReasonPolicyInvalidate   = TransitionReason{Kind: "policy_invalidate"}
       ReasonPolicyGiveUp       = TransitionReason{Kind: "policy_give_up"}
       ReasonOperatorReset      = TransitionReason{Kind: "operator_reset"}
       ReasonOperatorInvalidate = TransitionReason{Kind: "operator_invalidate"}
       ReasonHeartbeatLost      = TransitionReason{Kind: "heartbeat_lost"}
       ReasonRestoreVersion     = TransitionReason{Kind: "restore_version"}
       ReasonPureCascade        = TransitionReason{Kind: "pure_cascade"}       // NEW: stale→fresh inline
   )

   // NextState returns the new state for a transition. It NEVER short-circuits
   // when `current == requested`. Specifically `running → running` under reason
   // `dispatch_claimed` returns ErrIllegalTransition. See spec §5.3 and §17.
   func NextState(current shared.NodeState, reason TransitionReason) (shared.NodeState, error) {
       if reason.Kind == "restore_version" { return shared.NodeStateFresh, nil }
       switch current {
       case shared.NodeStateFresh:
           if reason.Kind == "invalidate_received" || reason.Kind == "operator_invalidate" {
               return shared.NodeStateStale, nil
           }
       case shared.NodeStateStale:
           if reason.Kind == "dispatch_claimed" { return shared.NodeStateRunning, nil }
           if reason.Kind == "pure_cascade"    { return shared.NodeStateFresh, nil }
       case shared.NodeStateRunning:
           if reason.Kind == "work_completed" { return shared.NodeStateFresh, nil }
           if reason.Kind == "policy_retry" || reason.Kind == "policy_invalidate" || reason.Kind == "heartbeat_lost" {
               return shared.NodeStateStale, nil
           }
           if reason.Kind == "policy_give_up" { return shared.NodeStateFailed, nil }
       case shared.NodeStateFailed:
           if reason.Kind == "operator_reset" || reason.Kind == "operator_invalidate" {
               return shared.NodeStateStale, nil
           }
       }
       return "", fmt.Errorf("%w: from %s reason %s", shared.ErrIllegalTransition, current, reason.Kind)
   }
   ```
3. Tests: full transition-table coverage plus:
   - `TestRunningToRunningUnderDispatchClaimedIsRejected` — the blessed invariant.
   - `TestRestoreVersionFromEverywhereReturnsFresh`.
   - `TestPureCascadeOnlyValidFromStale`.

**Verification:** `go test ./core/node/... -run TestState` passes.

---

### Task 5.2 — `core/node/policy.go` + tests

**Files:**
- `rimsky-go/core/node/policy.go`
- `rimsky-go/core/node/policy_test.go`

**Steps:**

1. Port `rimsky/src/cell/policy-evaluator.ts`. The algorithm ports 1:1; Go idiom differs.
2. Define:
   ```go
   type ErrorTypePolicy struct {
       Policy []PolicyAction
   }
   type PolicyAction struct {
       Action          string   // "retry" | "invalidate" | "give_up"
       Count           int
       Backoff         shared.BackoffKind
       Jitter          shared.JitterKind
       BaseDelayMs     int
       MaxDelayMs      int
       Targets         []string
       RestoreVersion  string   // "previous" | "" | version id
       ReasonTemplate  string
   }

   type EvaluatorState struct {
       ActionIndex        int
       RetryCounter       int
       CurrentErrorClass  string
   }

   type ResolvedAction struct {
       Kind          string       // "retry" | "invalidate" | "give_up"
       DelayMs       int
       Targets       []string
       RestoreVersion string
       Reason        string
       NewState      EvaluatorState
   }

   func Evaluate(
       policy *ErrorTypePolicy,         // nil → give_up("unknown_error_class")
       state EvaluatorState,
       errorClass string,
       rng func() float64,
   ) ResolvedAction { ... }
   ```
3. Port the step recursion, class-change reset, retry-exhaust advance, give_up terminal.
4. Tests: cover every branch in the TS policy-evaluator_test.ts. Include `TestInvalidateAdvancesActionIndexImmediately` (explicit test for the action_index-advances-on-invalidate behavior; matches TS behavior).

**Verification:** `go test ./core/node/... -run TestPolicy` passes.

---

### Task 5.3 — `core/scheduler/backoff.go` + tests

**Files:**
- `rimsky-go/core/scheduler/backoff.go`
- `rimsky-go/core/scheduler/backoff_test.go`

**Steps:**

1. Port `rimsky/src/scheduler/backoff.ts`. Pure math:
   ```go
   type BackoffConfig struct {
       Kind         shared.BackoffKind
       BaseDelayMs  int
       Jitter       shared.JitterKind
       MaxDelayMs   int    // 0 for unlimited
   }
   func ComputeDelay(cfg BackoffConfig, attemptIndex int, rng func() float64) int { ... }
   ```
2. Tests: linear vs. exponential growth, plus_minus jitter stays within [0.5x, 1.5x], max clamp.

**Verification:** `go test ./core/scheduler/... -run TestBackoff` passes.

---

### Task 5.4 — `core/node/template.go` + `template_validator.go` + tests

**Files:**
- `rimsky-go/core/node/template.go`
- `rimsky-go/core/node/template_validator.go`
- `rimsky-go/core/node/template_validator_test.go`

**Steps:**

1. Port `rimsky/src/cell/template.ts` type definitions. Apply spec §5.5 changes: drop `kind` field from NodeTemplateDef; add optional `Executor`, `Schedule`, `Userdata`; `OwnsResources[*]` gains `Implementation string` + `Config map[string]any`.
2. Template root type:
   ```go
   type TemplateSpec struct {
       Name          string
       Version       string
       Description   string
       Nodes         []TemplateNodeDef
       ParamsSchema  map[string]any
       ParamsRedact  []string
   }
   type TemplateNodeDef struct {
       Type              string
       Description       string
       Executor          string                        // optional; empty = pure-cascade
       Userdata          map[string]any                // opaque
       Schedule          string                        // cron expr; optional
       Dependencies      []string
       ConcurrencyTags   []string
       OwnsResources     []ResourceDef
       ReadsResources    []ReadResourceDef
       ErrorTypes        map[string]ErrorTypePolicy
   }
   type ResourceDef struct {
       Path            []string
       Implementation  string
       Config          map[string]any
       Retention       *Retention
       QualityRules    []qualityrule.Spec
   }
   ```
3. Write `template_validator.go`. Validation rules from spec §5.6:
   - All dependencies reference declared nodes.
   - All `error_types.*.policy[*].invalidate.targets` reference declared nodes.
   - No dependency cycles (DFS check).
   - Schedule (if present) is a valid cron expression (`cron.ParseStandard`).
   - If `Executor` is empty AND `OwnsResources` non-empty → error.
   - If `Executor` is empty AND `Userdata` non-empty → warning (return a `[]ValidationWarning` in the validator result, not an error).
   - `OwnsResources[*].Implementation` references a registered resource implementation (validator takes the registry as a parameter).
   - `OwnsResources[*].Config` validates against `Implementation.ConfigSchema()` (JSON Schema).
   - Placeholders in `OwnsResources[*].Path` and `.Config` well-formed (even though resolution is at instantiation, we can syntax-check them).
   - `ConcurrencyTags` placeholders well-formed.
4. Return a `ValidationResult` with `Errors []ValidationError` and `Warnings []ValidationWarning`.
5. Tests: positive and negative case for each rule above. Match the TS `template-validator_test.ts` coverage file-by-file.

**Verification:** `go test ./core/node/... -run TestTemplateValidator` passes.

---

### Task 5.5 — `core/qualityrule/spec.go` + `rules.go` + tests

**Files:**
- `rimsky-go/core/qualityrule/spec.go`
- `rimsky-go/core/qualityrule/rules.go`
- `rimsky-go/core/qualityrule/rules_test.go`

**Steps:**

1. Port `rimsky/src/resource/quality-rules.ts`. Define:
   ```go
   type Spec struct {
       Type     string
       Config   map[string]any
       Severity shared.Severity
   }
   type Failure struct {
       RuleType string
       Config   map[string]any
       Severity shared.Severity
       Details  string
   }
   type Evaluator interface {
       Evaluate(ctx context.Context, input EvalInput) (passed bool, details string, err error)
   }
   type EvalInput struct {
       NewData      any
       PreviousData any     // nil if no previous version
       Cfg          map[string]any
   }
   ```
2. Builtin rules:
   - `row_count_ratio` — assert len(new) >= ratio * len(previous).
   - `no_nulls` — assert configured fields are non-null in all records.
   - `nullable_fields_present` — assert configured fields exist (may be null).
   - `custom` — look up handler function from a registry.
3. Rule registry:
   ```go
   func Register(name string, ev Evaluator) { ... }
   func Get(name string) (Evaluator, bool) { ... }
   ```
4. `EvaluateAll` helper that runs a `[]Spec` over input, returning `[]Failure` partitioned by severity.
5. Tests: per-rule positive and negative cases.

**Verification:** `go test ./core/qualityrule/...` passes.

---

### Task 5.6 — `core/scheduler/schedule_ticker.go` + tests

**Files:**
- `rimsky-go/core/scheduler/schedule_ticker.go`
- `rimsky-go/core/scheduler/schedule_ticker_test.go`

**Steps:**

1. Port `rimsky/src/scheduler/timer-ticker.ts` semantics, renamed to schedule_ticker. Use `robfig/cron/v3` for expression parsing.
2. Define:
   ```go
   type MessageDispatcher interface {
       EmitInvalidate(ctx context.Context, req InvalidateRequest) error
   }
   type InvalidateRequest struct {
       SourceNodeID  *shared.UUID
       TargetNodeID  shared.UUID
       Reason        string
       RestoreVersion string
   }

   // NextFireAt returns the next cron fire time strictly after `after`.
   func NextFireAt(expr string, after time.Time) (time.Time, error) { ... }

   // ProcessSchedules finds rows with next_fire_at <= now, emits invalidate
   // for each target, and updates next_fire_at + last_fired_at atomically per row.
   func ProcessSchedules(ctx context.Context, sb storage.StorageBackend, disp MessageDispatcher, clock shared.Clock, log shared.Logger) error { ... }
   ```
3. Tests: cron parser correctness; multiple due schedules fire in one pass; errors in one row don't block others (log `schedule_dispatch_failed` — a Plan-A-added event kind that supplements the spec §11.2 list; add an entry to `rimsky-go/CHANGELOG.md` noting this additional event kind is introduced here, and flag for addition to spec §11.2 in a post-plan spec amendment).

**Verification:** `go test ./core/scheduler/... -run TestScheduleTicker` passes.

---

## Phase 6 — Storage interfaces and Postgres implementations

### Task 6.1 — `core/storage/interfaces.go`

**Files:** `rimsky-go/core/storage/interfaces.go`

**Steps:**

1. Port spec §8.1 + §12 interface definitions. One file, all interfaces:
   ```go
   type StorageBackend interface {
       Templates() TemplateStore
       Instances() InstanceStore
       Nodes() NodeStore
       Resources() ResourceRegistry
       ResourceData() ResourceDataStore
       Events() EventStore
       Schedules() ScheduleStore
       Supervisors() SupervisorStore
       Transaction(ctx context.Context, fn func(context.Context, Tx) error) error
   }
   type Tx interface{ _tx() }  // opaque brand
   ```
2. Define `TemplateStore`, `InstanceStore`, `NodeStore`, `ResourceRegistry`, `ResourceDataStore`, `EventStore`, `ScheduleStore`, `SupervisorStore` — each with methods matching the TS interfaces (§12.1 + TS `rimsky/src/storage/interfaces.ts`), renamed cell→node.
3. Row types: `NodeRow`, `InstanceRow`, `TemplateRow`, `EventRow`, `ScheduleRow`, `SupervisorRow`, `ResourceRow`, `ResourceVersionRow` — match the Postgres schema field-for-field.
4. Pagination helpers: `ListPagination`, `PaginatedListResult[T any]` (generic).

**Verification:** `go build ./core/storage/...` exits 0.

---

### Task 6.2 — testcontainers harness

**Files:**
- `rimsky-go/core/storage/postgres/harness_test.go` (test-only helper; use a `testmain` approach or a per-file helper)
- OR `rimsky-go/internal/testutil/harness.go` if we want a shared helper (Go discourages `test/` top-level; a package-scoped helper is more idiomatic)

**Steps:**

1. Decide: shared harness lives in `rimsky-go/core/internal/pgtest/pgtest.go` as a `package pgtest` helper. `internal/` keeps it un-importable from outside `core/`.
2. Implement:
   ```go
   package pgtest

   // StartPostgres spins up a testcontainers-go Postgres container, applies
   // all rimsky migrations, returns a connection pool + teardown function.
   // Uses Wait.ForLog("database system is ready") with N=2 plus a SELECT-1
   // loop (matches TS harness robustness per execution log).
   func StartPostgres(ctx context.Context, t *testing.T) (*pgxpool.Pool, func()) { ... }
   ```
3. Parallel-run safe: testcontainers creates unique container names automatically.
4. Reusable across all storage tests, queue tests, scenario tests.

**Verification:**
- Add a trivial test `TestHarnessStartsPostgres` in `core/internal/pgtest/pgtest_test.go`.
- `go test ./core/internal/pgtest/...` passes.

---

### Task 6.3 — Postgres store implementations (one task per store, in parallel subtasks or one after another)

**Files (each in `rimsky-go/core/storage/postgres/`):**
- `backend.go` — `PostgresStorageBackend` factory; holds `*pgxpool.Pool`; implements `Transaction`.
- `templates.go` — `TemplateStore`: `Deploy`, `Get`, `List`, `Delete`.
- `instances.go` — `InstanceStore`.
- `nodes.go` — `NodeStore`: port TS `cell-store.ts` methods, renamed, adapted for new columns (`executor`, `schedule_cron`).
- `resources.go` — `ResourceRegistry`.
- `resource_data.go` — `ResourceDataStore` (inline-JSONB read/delete only).
- `events.go` — `EventStore`: append, list with cursor pagination, tail.
- `schedules.go` — `ScheduleStore`: register, dueBefore, recordFired, listAll.
- `supervisors.go` — `SupervisorStore`.

**Steps:**

For each store file:
1. Port the TS implementation line-by-line, adapted for `pgx/v5` idiom. Use `pgx.Row.Scan`, `pgx.Rows.Scan`, `pool.Query`, `pool.Exec`, `Begin`/`Commit` patterns.
2. Preserve every `@blessed-invariant` comment as a Go doc comment with the same wording.
3. Preserve every indexed-by-state query; preserve the claim-query inside queue (that's Phase 7, but other stores feed into it).
4. Integration tests per store in `rimsky-go/core/storage/postgres/postgres_test.go`: use the `pgtest` harness, exercise CRUD and error paths.
5. For `NodeStore.UpdateState`: delegate to `node.NextState` (no idempotent short-circuit — blessed invariant). Write a dedicated test `TestUpdateStateRejectsRunningToRunningUnderDispatchClaimed`.

**Per-store steps are independent;** can be implemented in subagent-parallel dispatch. Suggested dispatch: 3 subtasks — backend+templates+instances together; nodes+resources+resource_data together; events+schedules+supervisors together.

**Verification:**
- `go test ./core/storage/postgres/...` passes.
- All blessed-invariant doc comments present (verify with `grep -r "@blessed-invariant" core/`).

---

## Phase 7 — Dispatch queue

### Task 7.1 — `core/queue/interface.go`

**Files:** `rimsky-go/core/queue/interface.go`

**Steps:**

1. Port spec §12.2 plus the full TS interface surface (`Enqueue`, `Claim`, `Complete`, `Fail`, `RemoveForNode`, `ListOrphanedClaims`, `ReleaseClaim`, `GetClaimedBy`).
2. Signatures:
   ```go
   type DispatchQueue interface {
       Enqueue(ctx context.Context, req DispatchRequest) error
       Claim(ctx context.Context, supervisorID string, accepts []string, limits map[string]int) (*shared.DispatchRow, error)
       Complete(ctx context.Context, dispatchID shared.UUID, expectedClaimedBy string) error
       Fail(ctx context.Context, dispatchID shared.UUID, reason string, expectedClaimedBy string) error
       RemoveForNode(ctx context.Context, nodeID shared.UUID, expectedClaimedBy string) error
       ListOrphanedClaims(ctx context.Context, cutoff time.Time) ([]shared.DispatchRow, error)
       ReleaseClaim(ctx context.Context, dispatchID shared.UUID, expectedClaimedBy string) error
       GetClaimedBy(ctx context.Context, dispatchID shared.UUID) (ClaimOwnership, error)
   }
   type DispatchRequest struct {
       NodeID          shared.UUID
       ExecutorName    string
       ConcurrencyTags []string
       EnqueuedAt      time.Time
   }
   type ClaimOwnership struct {
       Kind          string       // "not_found" | "unclaimed" | "claimed_by"
       SupervisorID  string       // populated when Kind=="claimed_by"
   }
   ```
3. `expectedClaimedBy` is "" to mean "unguarded" (matches TS undefined parameter).

**Verification:** `go build ./core/queue/...` exits 0.

---

### Task 7.2 — `core/queue/postgres/queue.go` + integration tests

**Files:**
- `rimsky-go/core/queue/postgres/queue.go`
- `rimsky-go/core/queue/postgres/queue_test.go`

**Steps:**

1. Port `rimsky/src/queue/postgres-queue.ts` **with all blessed-invariant comments preserved verbatim**. These are load-bearing.
2. Key invariants (verify preserved):
   - Tag-limit counts from `rimsky_dispatch.claimed_by IS NOT NULL` (not from node state).
   - Per-tag `pg_advisory_xact_lock` acquired in lexicographic order.
   - Claim limits `LIMIT 100` to bound the working set.
   - `Complete`, `Fail`, `RemoveForNode`, `ReleaseClaim` all accept `expected_claimed_by` parameter for claimant-guarded updates.
3. Use a `pgxpool.Pool` constructor. All transactional work via `pool.BeginTx(ctx, pgx.TxOptions{})` for the claim path.
4. Integration tests: port TS scenarios (claim with no rows returns nil; claim respects tag limits; concurrent claims serialized via advisory locks; orphan listing filters on `state='stale'`; releaseClaim with wrong expected_claimed_by is a no-op not an error; etc.).

**Verification:**
- `go test ./core/queue/postgres/...` passes.
- `grep -A 20 "@blessed-invariant" core/queue/postgres/queue.go` shows the full invariant block.

---

## Phase 8 — Resource library (interface + inline-jsonb)

### Task 8.1 — `core/resource/interface.go` + `errors.go` + `register.go`

**Files:**
- `rimsky-go/core/resource/interface.go`
- `rimsky-go/core/resource/errors.go`
- `rimsky-go/core/resource/register.go`

**Steps:**

1. `interface.go`: implement spec §8.1 + §8.2 exactly.
   ```go
   package resource

   type Config = map[string]any

   type Resource interface {
       Path() []string
       OwnerNodeID() shared.UUID
       CurrentVersion(ctx context.Context) (*Version, error)
       PreviousVersion(ctx context.Context) (*Version, error)
       ListVersions(ctx context.Context, limit int) ([]*Version, error)
       Commit(ctx context.Context, req CommitRequest) (*CommitResult, error)
       NoOpCommit(ctx context.Context) error
       RestoreVersion(ctx context.Context, target VersionRef) (*Version, error)
   }

   type Version struct {
       ID              shared.UUID
       ProducedByNode  *shared.UUID  // nullable
       Data            []byte        // JSON bytes for inline; nil for external
       DataRef         string
       ChangeSummary   string
       CommittedAt     time.Time
   }

   type CommitRequest struct {
       ProducedBy    shared.UUID
       Result        any            // marshaled to JSON by the inline-jsonb impl
       Changed       bool
       ChangeSummary string
   }

   type CommitResult struct {
       Accepted      bool
       Version       *Version
       QualityErrors []qualityrule.Failure
   }

   type VersionRef struct {
       Kind string       // "previous" | "id"
       ID   shared.UUID
   }

   type Factory interface {
       ConfigSchema() []byte   // JSON Schema bytes
       Create(cfg Config, rules []qualityrule.Spec, reg Registry) (Resource, error)
   }

   type Registry interface {
       // Allocate a resource row + first ownership-binding against a node.
       AllocResource(ctx context.Context, path []string, ownerNodeID shared.UUID, keepVersions int) (shared.UUID, error)
       // Version pointer ops (implemented by the resource-registry Postgres store; passed in).
       SetCurrentVersion(ctx context.Context, resourceID, versionID shared.UUID) error
       // ...
   }
   ```
2. `errors.go`: sentinel errors specific to resources.
3. `register.go`:
   ```go
   var factories = map[string]Factory{}
   func RegisterFactory(name string, f Factory) { factories[name] = f }
   func GetFactory(name string) (Factory, bool) { f, ok := factories[name]; return f, ok }
   func ListFactoryNames() []string { ... }
   ```
4. No tests — trivial registration; exercised by inline-jsonb tests next.

**Verification:** `go build ./core/resource/...` exits 0.

---

### Task 8.2 — `core/resource/inlinejsonb/*.go` + tests

**Files:**
- `rimsky-go/core/resource/inlinejsonb/factory.go`
- `rimsky-go/core/resource/inlinejsonb/resource.go`
- `rimsky-go/core/resource/inlinejsonb/resource_test.go`

**Steps:**

1. `factory.go`:
   ```go
   package inlinejsonb
   var configSchema = []byte(`{
     "type": "object",
     "properties": { "keep_versions": { "type": "integer", "minimum": 1 } },
     "additionalProperties": false
   }`)
   type Factory struct{
       Registry        resource.Registry
       DataStore       storage.ResourceDataStore   // for version-row writes
       StoreReg        storage.ResourceRegistry    // for pointer updates
   }
   func (f Factory) ConfigSchema() []byte { return configSchema }
   func (f Factory) Create(cfg resource.Config, rules []qualityrule.Spec, reg resource.Registry) (resource.Resource, error) { ... }
   ```
2. `resource.go`:
   - `Commit`: JSON-marshal `req.Result`; check for BigInt-equivalent / unserializable edge cases; run `qualityrule.EvaluateAll`; on error-severity failures return `CommitResult{Accepted: false, QualityErrors: ...}`; on success, insert new `rimsky_resource_versions` row, swap pointers, GC versions beyond `keep_versions`, return `Accepted: true`.
   - `NoOpCommit`: emit `no_op_commit` event (via an injected event-writer; or defer event-writing to the supervisor — see Phase 10 decision).
   - `RestoreVersion("previous")`: swap `current_version_id ↔ previous_version_id` atomically.
   - `RestoreVersion("id", id)`: look up the version; if it's already GC'd → error.
3. Register the factory at package init OR expose a `Register(backend storage.StorageBackend)` function for the consumer's main to call. Prefer explicit registration (spec §4.1 rules).
4. Tests: commit happy path; commit with quality-rule failure; NoOpCommit; RestoreVersion happy path; RestoreVersion on GC'd version → error; keep_versions GC behavior; unserializable result rejected.

**Verification:** `go test ./core/resource/inlinejsonb/...` passes.

---

## Phase 9 — Scheduler

### Task 9.1 — `core/scheduler/invalidate.go` + `recalculate.go`

**Files:**
- `rimsky-go/core/scheduler/invalidate.go`
- `rimsky-go/core/scheduler/recalculate.go`
- one combined test file

**Steps:**

1. Port `rimsky/src/scheduler/invalidate.ts` and `recalculate.ts`. These are pure functions over `StorageBackend + DispatchQueue + Clock + Logger`.
2. `invalidate.go`:
   ```go
   func InvalidateNode(ctx context.Context, args InvalidateArgs) error {
       // 1. Append message_emitted/received events.
       // 2. If restore_version is set and resource supports it, call resource.RestoreVersion;
       //    emit recalculate to dependents; set node state to fresh via restore_version reason;
       //    return.
       // 3. If node is already stale or running, no-op.
       // 4. Transition fresh→stale; emit invalidate to all dependents; schedule dispatch if
       //    dependencies are satisfied.
   }
   ```
3. `recalculate.go`: similar port.
4. Tests with testcontainers Postgres: cover each branch.

**Verification:** `go test ./core/scheduler/... -run TestInvalidate` and `TestRecalculate` pass.

---

### Task 9.2 — `core/scheduler/pure_cascade.go` + tests

**Files:**
- `rimsky-go/core/scheduler/pure_cascade.go`
- `rimsky-go/core/scheduler/pure_cascade_test.go`

**Steps:**

1. New to Go port (not in TS). Implement the stale→fresh inline transition per spec §6.1 step 3 / §6.4.
2. Function:
   ```go
   // ProcessPureCascade finds stale nodes with Executor=="" and all deps fresh;
   // transitions each to fresh inline; emits recalculate to dependents;
   // logs pure_cascade_commit event. Returns the count processed.
   func ProcessPureCascade(ctx context.Context, sb storage.StorageBackend, clock shared.Clock, log shared.Logger) (int, error) { ... }
   ```
3. Tests: pure-cascade node processed inline, not enqueued; pure-cascade with unsatisfied deps stays stale; `changed` verdict is always true; `non_resource_commit`-like event (named `pure_cascade_commit` in this port) is logged.

**Verification:** tests pass.

---

### Task 9.3 — `core/scheduler/scheduler.go` + tests

**Files:**
- `rimsky-go/core/scheduler/scheduler.go`
- `rimsky-go/core/scheduler/scheduler_test.go`

**Steps:**

1. Port `rimsky/src/scheduler/scheduler.ts`. Main loop per spec §6.1:
   1. Advisory-lock guard (`pg_try_advisory_lock`).
   2. Process schedules.
   3. Pure-cascade sweep (new).
   4. Stale-heartbeat sweep.
   5. Orphaned-claim sweep.
   6. Ready sweep for executor nodes.
2. Preserve the TS scheduler's `@blessed-invariant` on the advisory lock and the generous orphan-claim cutoff (`5 * heartbeatTimeoutMs`).
3. Tests: scheduler runs a tick, each subsystem is exercised in integration.

**Verification:** `go test ./core/scheduler/... -run TestScheduler` passes.

---

### Task 9.4 — `core/config/scheduler.go` (entry point)

**Files:** `rimsky-go/core/config/scheduler.go`

**Steps:**

1. Define `SchedulerConfig` per spec §10.4 / §11.1:
   ```go
   type SchedulerConfig struct {
       Storage                storage.StorageBackend
       Queue                  queue.DispatchQueue
       Clock                  shared.Clock
       Logger                 shared.Logger
       TickInterval           time.Duration      // default 1.5s
       HeartbeatTimeout       time.Duration      // default 15s
       OrphanedClaimTimeout   time.Duration      // default 5 * HeartbeatTimeout
       Pool                   *pgxpool.Pool      // for advisory lock
   }
   type SchedulerHandle interface {
       Shutdown(ctx context.Context) error
   }
   func StartScheduler(cfg SchedulerConfig) (SchedulerHandle, error) { ... }
   ```
2. Not tested directly — exercised by scenarios.

**Verification:** `go build ./core/config/...` exits 0.

---

## Phase 10 — Supervisor

### Task 10.1 — `core/supervisor/commit.go` + `on_error.go` + `terminal_outcome.go` + tests

**Files:**
- `rimsky-go/core/supervisor/commit.go`
- `rimsky-go/core/supervisor/on_error.go`
- `rimsky-go/core/supervisor/terminal_outcome.go`
- `rimsky-go/core/supervisor/commit_test.go`
- `rimsky-go/core/supervisor/on_error_test.go`

**Steps:**

1. `commit.go`: port `rimsky/src/supervisor/commit.ts`. The supervisor's commit helper calls `Resource.Commit(ctx, req)`; on accepted, emits `commit` event, calls `RecalculateNode` on dependents, transitions node to fresh. On rejected (quality errors), calls `on_error('quality_rule_failed', details)`.
2. `on_error.go`: port `rimsky/src/supervisor/on-error.ts`. Evaluates policy via `node.Evaluate`; applies the resolved action (retry → schedule re-enqueue with backoff; invalidate → emit invalidates and stay stale; give_up → transition to failed).
3. `terminal_outcome.go`: port `rimsky/src/supervisor/terminal-outcome.ts`. `ApplyTerminalOutcome(ctx, outcome)` routes based on `outcome.Kind`:
   - `run_succeeded` → commit flow.
   - `app_error` → on_error flow.
   - `infra_error` → re-enqueue without retry-counter bump.
4. Tests: each branch.

**Verification:** `go test ./core/supervisor/... -run 'TestCommit|TestOnError|TestTerminalOutcome'` passes.

---

### Task 10.2 — `core/executor/client.go` + `client_http.go` + `resolver.go`

**Files:**
- `rimsky-go/core/executor/client.go`
- `rimsky-go/core/executor/client_http.go`
- `rimsky-go/core/executor/resolver.go`

**Steps:**

1. `resolver.go`: per spec §9.3 / §10.2, map executor name → `Endpoint{Transport, URL}` from a static config.
   ```go
   type Endpoint struct {
       Transport string   // "grpc" | "http"
       URL       string
       TLS       string   // "off" | "optional" | "required"
   }
   type Resolver interface {
       Resolve(name string) (Endpoint, bool)
   }
   type StaticResolver struct { m map[string]Endpoint }
   ```
2. `client.go`: gRPC client wrapping `genv1.NodeExecutorClient`.
   ```go
   type Client interface {
       Execute(ctx context.Context, req *genv1.ExecuteRequest) (EventStream, error)
   }
   type EventStream interface {
       Recv() (*genv1.ExecuteEvent, error)
   }
   ```
   Construct a grpc.ClientConn per resolved endpoint; cache by endpoint URL; respect TLS mode.
3. `client_http.go`: HTTP+JSON bridge client. Speaks the mapping of `/v1/Execute` POST with JSON body returning chunked JSON events (line-delimited, one event per line). Define the wire format:
   - Request: `POST /v1/Execute` with `application/json` body = ExecuteRequest marshaled.
   - Response: `application/x-ndjson`, one `ExecuteEvent` per line as JSON.
4. Select transport based on `Endpoint.Transport`.
5. No direct tests (exercised by supervisor scenario tests).

**Verification:** `go build ./core/executor/...` exits 0.

---

### Task 10.3 — `core/supervisor/runner.go` + test

**Files:**
- `rimsky-go/core/supervisor/runner.go`
- `rimsky-go/core/supervisor/runner_test.go`

**Steps:**

1. New to Go port (collapses TS's deterministic-runner + agentic-runner into a single executor-protocol runner).
2. Function:
   ```go
   func RunNode(ctx context.Context, args RunArgs) (RunnerResult, error) {
       // 1. Verify claim ownership (blessed invariant — §17 / §6.2 step 4).
       //    On mismatch, log orphaned_claim_lost_race, return {Ran: false}.
       // 2. Resolve executor endpoint from config.
       //    On miss, emit unresolved_executor event and treat as
       //    on_error("unresolved_executor"); return {Ran: true}.
       // 3. Transition node stale→running; stamp heartbeat; emit work_started.
       // 4. Assemble ExecuteRequest (node_id, instance_id, userdata, deps_data,
       //    reads_data, instance_params, callback_url, cancel_token).
       // 5. Call client.Execute; stream events:
       //    - Heartbeat: update last_heartbeat_at.
       //    - Complete: terminal → applyTerminalOutcome(run_succeeded).
       //    - Blocked: terminal → applyTerminalOutcome(app_error, "executor_blocked").
       //    - Errored: terminal → applyTerminalOutcome(app_error, errorClass).
       //    - AsyncAccepted: non-terminal for node. Register pending async ID,
       //      return {Ran: true, Async: true}. The async callback arrives later
       //      at the supervisor's callback HTTP endpoint (Task 10.5).
       // 6. Stream close without terminal → applyTerminalOutcome(infra_error,
       //    "stream_closed_without_terminal").
   }
   ```
3. Test with an in-package test double that implements `executor.Client` (and `EventStream`) with scripted event sequences. This is NOT the same as the `executors/stub/` binary from Phase 12 — the Phase 12 stub is a real gRPC server used by scenario tests; the Task 10.3 test double is an in-process fake for runner unit tests, living alongside `runner_test.go` under `rimsky-go/core/supervisor/`.

**Verification:** `go test ./core/supervisor/... -run TestRunNode` passes.

---

### Task 10.4 — `core/supervisor/supervisor.go` main loop + test

**Files:**
- `rimsky-go/core/supervisor/supervisor.go`
- `rimsky-go/core/supervisor/supervisor_test.go`

**Steps:**

1. Port `rimsky/src/supervisor/supervisor.ts`. Main loop per spec §6.2.
2. Heartbeat tick updates supervisor's `last_heartbeat_at` and per-active-node heartbeats, polls `kill_requested` and cancels the active run via cancel-token/ctx.Cancel on change.
3. Concurrency-limit claim per supervisor config.
4. Graceful shutdown.
5. Test wiring: harness Postgres + fake executor client + simulated dispatch rows; assert state transitions.

**Verification:** `go test ./core/supervisor/... -run TestSupervisor` passes.

---

### Task 10.5 — Supervisor-hosted async-callback HTTP endpoint

**Files:**
- `rimsky-go/core/supervisor/callback.go`
- `rimsky-go/core/supervisor/callback_test.go`

**Steps:**

1. Implement the supervisor's callback endpoint per spec §7.2 async-handoff path.
2. Endpoint binds to `callback.host:callback.port` (from supervisor config).
3. Routes:
   - `POST /v1/callback/:async_ack_id` — body is the JSON form of `Complete | Blocked | Errored`. Validates that `async_ack_id` matches a pending async-handoff registered by this supervisor (in-memory registry); on hit, routes to `applyTerminalOutcome`. On miss, 404.
4. Test: post a valid Complete, verify it triggers commit flow; post with unknown ID, verify 404.

**Verification:** `go test ./core/supervisor/... -run TestCallback` passes.

---

### Task 10.6 — `core/config/supervisor.go` (entry point)

**Files:** `rimsky-go/core/config/supervisor.go`

**Steps:**

1. Define `SupervisorConfig` per spec §10.2 / §10.4:
   ```go
   type SupervisorConfig struct {
       SupervisorID        string
       Storage             storage.StorageBackend
       Queue               queue.DispatchQueue
       Clock               shared.Clock
       Logger              shared.Logger
       Concurrency         int
       HeartbeatInterval   time.Duration
       ClaimPollInterval   time.Duration
       ExecutorResolver    executor.Resolver
       ConcurrencyLimits   map[string]int
       SQLConnections      map[string]string   // name -> URL; referenced by external-sql resource (Plan C)
       Callback            CallbackConfig
   }
   type CallbackConfig struct { Host string; Port int }
   func StartSupervisor(cfg SupervisorConfig) (SupervisorHandle, error) { ... }
   ```

**Verification:** `go build ./core/config/...` exits 0.

---

## Phase 11 — Control API

### Task 11.1 — `core/controlapi/app.go` + middleware

**Files:**
- `rimsky-go/core/controlapi/app.go`
- `rimsky-go/core/controlapi/auth.go`
- `rimsky-go/core/controlapi/redact.go`

**Steps:**

1. `app.go`: chi router with middleware for request logging, error mapping (typed errors → HTTP status per `rimsky/src/control-api/errors.ts`), optional auth.
2. `auth.go`: `Authenticator interface { Authenticate(r *http.Request) (AuthContext, error) }`; default no-op wrapped in a "return anonymous" impl.
3. `redact.go`: apply `params_redact` to instance params on egress.

**Verification:** `go build ./core/controlapi/...` exits 0.

---

### Task 11.2 — Route handlers (one task per resource)

**Files:**
- `templates.go`, `instances.go`, `nodes.go`, `events.go`, `resources.go`, `health.go` (all in `core/controlapi/`)

**Steps:**

1. Port each route group from `rimsky/src/control-api/routes/*.ts` to chi handlers. Request/response schemas in `schemas.go` using `encoding/json` + `go-playground/validator` for basic shape checks.
2. `instances.go`: `POST /instances` handler calls `controlapi.createInstance` which walks the template, allocates IDs, resolves placeholders (per spec §5.8), provisions resources via registered factories, enqueues root executor nodes, inserts schedules.
3. `nodes.go`: operator overrides — `POST /nodes/:id/invalidate`, `POST /nodes/:id/reset`, `POST /nodes/:id/kill`.
4. `resources.go`: `GET /resources/:id/current`, etc.
5. `health.go`: aggregates scheduler + supervisor + executor-endpoint health.

Write one end-to-end test per route in `app_test.go` using `httptest.NewServer`.

**Verification:** `go test ./core/controlapi/...` passes.

---

### Task 11.3 — `core/config/controlapi.go` (entry point)

**Files:** `rimsky-go/core/config/controlapi.go`

**Steps:**

1. Define `ControlAPIConfig` and `StartControlAPI` per spec §10.4.

**Verification:** `go build ./core/config/...` exits 0.

---

## Phase 12 — Stub executor (for Plan A end-to-end scenarios)

### Task 12.1 — `executors/stub/stub.go`

**Files:**
- `rimsky-go/executors/stub/stub.go`
- `rimsky-go/executors/stub/stub_test.go`

**Steps:**

1. Implement an in-process `genv1.NodeExecutorServer` that accepts `ExecuteRequest` and emits events based on a preconfigured script.
2. Usable as:
   ```go
   stub := stub.New()
   stub.WhenType("my-node").Complete(map[string]any{"ok": true}, true, "done")
   stub.WhenType("fails").Error("my_class", map[string]any{})
   stub.WhenType("async").AsyncAccepted(stub.NextAck(), 0).Then(... register callback ...)
   srv, addr := stub.Listen(t)
   ```
3. Expose both gRPC and HTTP+JSON bridge modes (so supervisor's resolver with `transport: http` can be tested too).
4. This is not a reference executor per §9.1 — it's a test-only peer in `executors/stub/`. Never ships to users; documented as such.

**Verification:** `go test ./executors/stub/...` passes.

---

## Phase 13 — Scenario tests (end-to-end)

### Task 13.1 — Scenario harness

**Files:** `rimsky-go/core/internal/scenario/harness.go`

**Steps:**

1. Build on `pgtest.StartPostgres`. Add helpers to:
   - Spin up scheduler + supervisor + stub executor as in-process goroutines.
   - Deploy a template (inline YAML or Go struct).
   - Create an instance with params.
   - `WaitForState(nodeID, state, timeout)` polling helper.
   - Fetch events filtered by node or kind.

**Verification:** `go test ./core/internal/scenario/...` (minimal smoke test) passes.

---

### Task 13.2 — Write scenarios (one task per scenario, parallelizable)

**Files (one `*_test.go` each in `rimsky-go/test/scenarios/` — committed location; Plan B and Plan C both extend this directory):**

All 17 scenarios from spec §14.3:
- `happy_path_executor_test.go`
- `pure_cascade_test.go`
- `scheduled_node_test.go`
- `fan_out_pattern_test.go`
- `cascade_invalidate_test.go`
- `give_up_test.go`
- `double_buffering_test.go`
- `rollback_via_restore_version_test.go`
- `agentic_executor_async_handoff_test.go`  (uses stub executor in async-handoff mode)
- `executor_blocked_test.go`
- `unresolved_executor_test.go`
- `heartbeat_loss_reenqueue_test.go`
- `orphaned_claim_test.go`
- `verify_before_run_race_test.go`
- `state_machine_same_state_rejected_test.go`
- `concurrency_tag_limit_test.go`
- `no_op_commit_test.go`

**Steps per scenario:** fixture template YAML → deploy → instance → drive events → assert state and event log.

**Verification:** `go test ./test/scenarios/...` passes all 17.

---

## Phase 14 — Reference binaries

### Task 14.1 — `cmd/rimsky-scheduler/main.go`

**Files:** `rimsky-go/core/cmd/rimsky-scheduler/main.go`

**Steps:**

1. Read `RIMSKY_DB_URL`, `RIMSKY_SCHEDULER_TICK_MS`, `RIMSKY_HEARTBEAT_TIMEOUT_MS`, `RIMSKY_LOG_LEVEL`.
2. Construct a single `*pgxpool.Pool` from `RIMSKY_DB_URL`; pass to both `postgres.NewStorageBackend(pool)` and `postgresqueue.New(pool)`; pass the pool reference to `SchedulerConfig.Pool` (for advisory-lock use). Sharing one pool across storage + queue + advisory lock is intentional — simplifies lifecycle and matches the TS pattern.
3. `StartScheduler`, handle SIGTERM/SIGINT → graceful shutdown with 30s context.

**Env var note:** all reference binaries in this plan use `RIMSKY_DB_URL` as the Postgres DSN env var. Spec §10.2's example supervisor config references `${RIMSKY_PG_URL}` — the spec's example is illustrative only; the committed convention for v1 reference binaries is `RIMSKY_DB_URL`. A post-plan spec cleanup should align §10.2's example.

**Verification:** `go build ./core/cmd/rimsky-scheduler/` produces binary; running it with no env shows a readable error about missing `RIMSKY_DB_URL`.

---

### Task 14.2 — `cmd/rimsky-supervisor/main.go`

**Files:** `rimsky-go/core/cmd/rimsky-supervisor/main.go`

**Steps:**

1. Read a YAML config file path from `RIMSKY_SUPERVISOR_CONFIG`. Parse via koanf.
2. Construct a single `*pgxpool.Pool` from the config's `postgres_url`; pass to `postgres.NewStorageBackend(pool)` and `postgresqueue.New(pool)`.
3. Construct executor resolver from `config.executors` map (name → endpoint + transport).
4. For Plan A: `config.sql_connections` is empty (no resources need live SQL pools yet — external-sql lands in Plan C). Plan C Phase 0 Task 0.2 amends this step to open pools from the URL map.
5. `StartSupervisor`, handle SIGTERM/SIGINT.

**Verification:** `go build ./core/cmd/rimsky-supervisor/` produces binary.

---

### Task 14.3 — `cmd/rimsky-control-api/main.go`

**Files:** `rimsky-go/core/cmd/rimsky-control-api/main.go`

**Steps:**

1. Read `RIMSKY_DB_URL`, `RIMSKY_CONTROL_API_HOST` (default `127.0.0.1`), `RIMSKY_CONTROL_API_PORT` (default `8080`).
2. Construct one `*pgxpool.Pool` from `RIMSKY_DB_URL`; pass to `postgres.NewStorageBackend(pool)` and `postgresqueue.New(pool)`.
3. `StartControlAPI`, handle SIGTERM/SIGINT.

**Verification:** `go build ./core/cmd/rimsky-control-api/` produces binary.

---

## Phase 15 — Public exports

### Task 15.1 — Write `core/core.go` as public entry point

**Files:** `rimsky-go/core/core.go`

**Steps:**

1. Re-export key public types and constructors (Go doesn't have per-file re-export barrels, but consumers `import "github.com/fallguy/rimsky/core/config"` etc. — so `core.go` is primarily the package doc with a "start here" pointer and no actual exports.
2. Actually: the Go import model is "import the sub-package you need." So this task is: document in `core/doc.go` what each sub-package provides, serving as the library map.

**Verification:** `go doc ./core` and `go doc ./core/config` produce readable docs.

---

## Phase 16 — Definition of Done

### Task 16.1 — Gate verification

**Steps:**

1. `cd rimsky-go && go build ./...` → exit 0.
2. `go test ./... -count=1` → all tests passing.
3. `go vet ./...` → exit 0.
4. `golangci-lint run` → exit 0.
5. Live-DB migration: start a throwaway Postgres container; run `rimsky-migrate`; verify all 10 `rimsky_*` tables exist; re-run: output says nothing to apply.
6. Reference binaries start: run each of the three binaries in turn, verify they either run (given valid env) or fail with a readable missing-config message.
7. Append a Plan A entry to `rimsky-go/CHANGELOG.md`.
8. Append a `plan_completed` entry to the execution log (spec §execution-log format) with gate-check summary.

**Verification:** all gates green.

---

## Appendix — Subagent dispatch notes

**Parallelizable groups:**
- Phase 2 tasks 2.1–2.4 (shared utilities).
- Phase 4 tasks 4.1–4.3 (proto generation; 4.3 depends on 4.1+4.2).
- Phase 6 Task 6.3's per-store implementations (3 groups of 3 as suggested).
- Phase 11 Task 11.2's per-route handlers (can split into template-routes / instance-routes / node-routes / event-routes / resource-routes / health-routes).
- Phase 13 Task 13.2's 17 scenarios — all parallelizable once the harness lands.

**Serial dependencies:**
- Phase 1 → Phase 2 → Phase 3 → Phase 4 → Phase 5 → Phase 6 → Phase 7 → Phase 8 → Phase 9 → Phase 10 → Phase 11 → Phase 12 → Phase 13 → Phase 14 → Phase 15 → Phase 16.

**Critical-path tasks requiring extra care** (slower, higher-risk; subagent should plan for possible review rounds):
- Task 7.2 (PostgresDispatchQueue) — blessed invariants, concurrency correctness.
- Task 9.3 (Scheduler main loop) — advisory locks, orphan sweep.
- Task 10.4 (Supervisor main loop) — claim/verify/heartbeat interplay.
- Task 13.2's `verify_before_run_race` and `state_machine_same_state_rejected` scenarios — proving the blessed invariants.
