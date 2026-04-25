# Architecture

Implementation shape of rimsky v1 (Go). This document covers how the code is organized, which processes run, how distribution works, and where the conceptual invariants are enforced in source. Conceptual model lives in `node-graph-design.md`; wire protocol lives in `protocol.md`.

---

## 1. Three collections

Rimsky is built from three architecturally-distinct collections. For v1 they ship in one repository, versioned and documented together, but each is designed to separate cleanly.

### 1.1 Orchestrator

The node-graph runtime. State machine, scheduler, supervisor, control API, dispatch queue, Postgres storage, migrations. Knows nothing about LLMs. Knows resources and executors only through their interfaces / protocol.

The orchestrator is a single Go module (`github.com/fallguy/rimsky/core`). Packages within it are subject to the import-graph rules in §3. The orchestrator is the only Go-importable collection; reference resources and the Go reference executor ship as `package main` binaries and Docker images, not as importable packages.

### 1.2 Resource library

Implementations of the `Resource` interface (declared in `core/resource/`). Each implementation decides how to store versions, how to roll back, how to evaluate quality rules. Implementations live in the repository under `resources/`, but the orchestrator's wire to them is the in-process Go interface, registered by the deployer's `main()` at process startup.

v1 ships two reference implementations:

- `resources/inlinejsonb/` — data stored in `rimsky_resource_versions.data` (JSONB column). Small, fast, schema-flexible. Fit for internal blobs, reports, intermediate artifacts.
- `resources/externalsql/` — data written to a consumer-owned SQL table via a declared connection. Uses a staging-table + atomic-swap pattern for double-buffering. Fit for tabular production data consumed by external services.

### 1.3 Executor library

Reference executor services that speak the node-executor protocol. Executors run as peer services; the orchestrator calls them over the wire. v1 ships:

- `executors/http-node/` — Go. Generic "POST userdata to URL, commit response." Protocol: gRPC + HTTP bridge.
- `executors/claude-agent/` — TypeScript. Spawns Claude CLI, hosts its own internal MCP callback for the agent, reports outcome back to rimsky via protocol callback (async handoff). Published as npm package `@rimsky/executor-claude-agent` and as a Docker image.

Because executors are peer services, a new executor in Python (or Rust, or any language that can speak gRPC or HTTP+JSON) requires no orchestrator changes — only adding an entry to a supervisor's config.

---

## 2. Repository layout

```
rimsky-go/                          # repo root (becomes "/" on OSS extraction)
├── core/                           # the single Go module
│   ├── go.mod                      # module github.com/fallguy/rimsky/core
│   ├── node/                       # state machine, policy evaluator, template types
│   ├── message/                    # invalidate / recalculate shapes and helpers
│   ├── queue/                      # DispatchQueue interface + Postgres impl
│   ├── scheduler/                  # tick loop, schedule firing, orphan sweep, ready sweep
│   ├── supervisor/                 # claim, executor client, outcome handling, heartbeat
│   ├── controlapi/                 # HTTP+JSON routes, schemas, error mapping, auth hook
│   ├── storage/                    # store interfaces + Postgres impls (all state tables)
│   ├── migrations/                 # numbered SQL files + runner
│   ├── resource/                   # Resource + QualityRule INTERFACES (not impls)
│   ├── qualityrule/                # builtin rule implementations
│   ├── executor/                   # protocol-client helpers (gRPC / HTTP bridge clients)
│   ├── config/                     # library entry points (StartScheduler, etc.)
│   ├── shared/                     # cross-package types, errors, clock, logger
│   ├── scenario/                   # scenario-test harness
│   ├── internal/                   # unexported helpers (pgtest, etc.)
│   └── cmd/                        # reference binaries (Docker entrypoints)
│       ├── rimsky-scheduler/
│       ├── rimsky-supervisor/
│       ├── rimsky-control-api/
│       ├── rimsky-migrate/
│       ├── rimsky-conformance/
│       └── rimsky-conformance-probe/
├── proto/                          # protocol source of truth
│   └── v1/
│       ├── node_executor.proto
│       └── events.proto
├── resources/                      # reference resource implementations (Go, in-process)
│   ├── inlinejsonb/
│   └── externalsql/
├── executors/                      # reference executors (peer services)
│   ├── http-node/                  # Go; its own `package main`; Docker image
│   └── claude-agent/               # TypeScript; npm package; Docker image
├── conformance/                    # protocol conformance test suite (Go binary)
├── deploy/
│   ├── docker-compose.yml          # reference deployment
│   ├── kubernetes/                 # Helm chart + manifests
│   └── Dockerfile.*                # per-component Dockerfiles
├── docs/
├── test/                           # cross-package scenario tests
└── examples/
```

During development in the originating monorepo, this layout lives under `rimsky-go/`. On extraction to its standalone OSS repo, the `rimsky-go/` prefix drops and this becomes the repo root.

---

## 3. Package import rules

The core module's internal package graph is enforced by `go vet` plus a custom lint in CI. Violations fail the build.

### 3.1 The rules

- `node/` — pure logic (state machine, policy evaluator, template types, backoff math). Imports `shared/` only.
- `message/` — types only. Imports `shared/` only.
- `queue/` — `DispatchQueue` interface + Postgres implementation. Imports `shared/` and `pgx`.
- `storage/` — store interfaces + Postgres implementations. Imports `shared/` and `pgx`.
- `resource/` — `Resource` and `Factory` interface declarations only. Imports `shared/` only.
- `qualityrule/` — builtin quality-rule implementations. Imports `shared/` and `resource/` (interface).
- `executor/` — protocol-client helpers (gRPC + HTTP bridge clients). Imports `shared/` and generated code from `proto/v1/`.
- `scheduler/` — imports `node/`, `message/`, `queue/`, `storage/`, `shared/`, `resource/` (interface only). **Does not import `supervisor/` or `controlapi/`.**
- `supervisor/` — imports `node/`, `message/`, `queue/`, `storage/`, `shared/`, `resource/` (interface only), `executor/`. **Does not import `scheduler/` or `controlapi/`.**
- `controlapi/` — imports `node/`, `message/`, `storage/`, `shared/`, `resource/` (interface only). **Does not import `scheduler/` or `supervisor/`.**
- `shared/` — depends on nothing except stdlib.
- `migrations/` — embeds SQL files via `embed.FS`; imports `shared/` only.
- `config/` — library entry points (§6). Imports the subsystem it starts (scheduler, supervisor, controlapi) and `shared/`.
- `cmd/*` — the only packages allowed to import everything needed to wire up a binary.

### 3.2 Why these rules matter

The scheduler and supervisor are independent processes. They communicate only through shared Postgres state (dispatch queue, node states, events, heartbeats). Forcing their packages to be unable to import each other prevents accidental coupling — a future change that has the scheduler directly calling supervisor code would fail to compile.

The control API has the same property: it never calls into scheduler or supervisor internals. It reads and writes shared state; the runtime subsystems observe those reads/writes through their own polling/query logic.

The `resource/` package declares interfaces only. Concrete resource implementations live outside `core/`, under `resources/`. The orchestrator never imports a specific resource; deployers wire them in at `main()` via `resource.Register(name, factory)`.

---

## 4. Three long-running processes

Rimsky ships as three independent long-running processes, each shipped as a standalone Go binary and a Docker image.

### 4.1 Scheduler

Binary: `cmd/rimsky-scheduler/`. Docker: `rimsky-scheduler`.

Responsibilities per tick (default interval 1.5s):

1. **Advisory-lock guard.** `pg_try_advisory_lock(SCHEDULER_TICK_KEY)`. If another replica holds it, skip this tick. This is the multi-replica double-tick safety invariant (§5.7).
2. **Schedule firing.** For each node whose cron indicates a fire is due: emit `invalidate`; compute and write next `next_fire_at`; log `schedule_fired`.
3. **Pure-cascade sweep.** For each `stale` node with no `executor` and all deps `fresh`: transition `stale → fresh` inline; emit `recalculate` to dependents; log `pure_cascade_commit`.
4. **Stale-heartbeat sweep.** For each `running` node whose `last_heartbeat_at` < `now - heartbeat_timeout`: log `heartbeat_lost`; clear supervisor assignment; transition `running → stale`; re-enqueue.
5. **Orphaned-claim sweep.** For each dispatch row with `claimed_by IS NOT NULL`, `claimed_at < now - orphaned_claim_timeout`, and node still `stale`: release the claim (claimant-guarded).
6. **Ready sweep.** For each `stale` executor node with all deps `fresh` and no pending dispatch row: enqueue a dispatch row.

Code location: `core/scheduler/scheduler.go` is the tick loop; `core/scheduler/schedule_ticker.go` handles cron firing; `core/scheduler/pure_cascade.go` handles the inline sweep; `core/scheduler/recalculate.go` and `core/scheduler/invalidate.go` handle message semantics.

### 4.2 Supervisor

Binary: `cmd/rimsky-supervisor/`. Docker: `rimsky-supervisor`.

Responsibilities:

1. **Register.** On startup, upsert into `rimsky_supervisors` with `id`, accepted-executor list, concurrency limit, callback host/port (if hosting callbacks for async handoff).
2. **Heartbeat tick.** Every `heartbeat_interval_ms`, update `last_heartbeat_at`, active-node count, and each active node's `last_heartbeat_at`. Poll `kill_requested` on each active node; if true, send cancel through the executor client.
3. **Claim.** While active < concurrency, query the dispatch queue for a claimable row matching this supervisor's accept list and respecting concurrency-tag limits. Claim via `SELECT ... FOR UPDATE SKIP LOCKED`.
4. **Verify.** Re-read `claimed_by` on the dispatch row IMMEDIATELY before any expensive work. If the row has been released or re-claimed, log `orphaned_claim_lost_race` and bail. Hard backstop against double-execute.
5. **Dispatch.** Resolve `node.executor` → endpoint + transport from static config. Construct `ExecuteRequest`. Call `Execute` on the executor client (gRPC or HTTP bridge, per config).
6. **Handle response.** Terminal events map to commit (Complete), error routing (Blocked/Errored), or async-hold (AsyncAccepted).
7. **Complete.** Delete dispatch row (claimant-guarded).

Code locations: `core/supervisor/supervisor.go` is the top-level loop; `core/supervisor/runner.go` handles per-dispatch execution including the verify-before-run step; `core/supervisor/callback.go` handles async-handoff callbacks; `core/supervisor/commit.go` drives the commit flow through resource implementations; `core/supervisor/on_error.go` dispatches through the policy chain; `core/supervisor/terminal_outcome.go` maps protocol terminal events into local outcomes.

### 4.3 Control API

Binary: `cmd/rimsky-control-api/`. Docker: `rimsky-control-api`.

HTTP+JSON endpoints:

- `POST /templates`, `GET /templates`, `GET /templates/:id`, `DELETE /templates/:id`.
- `POST /instances`, `GET /instances`, `GET /instances/:id_or_key`, `DELETE /instances/:id_or_key`.
- `GET /nodes/:id`, `GET /instances/:id_or_key/nodes`.
- `POST /nodes/:id/invalidate`, `POST /nodes/:id/reset`, `POST /nodes/:id/kill`.
- `GET /events` (paginated, filterable).
- `GET /resources/:id/current`, `GET /resources/:id/versions`, `GET /resources/:id/versions/:version_id`.
- `GET /health`, `GET /metrics` (Prometheus).

Auth is pluggable: `Authenticator` interface. Default: none; reference binary binds to localhost by default. Enterprise deployments provide their own auth module.

Code locations: `core/controlapi/app.go` is the route wiring; individual route handlers are in `templates.go`, `instances.go`, `nodes.go`, `events.go`, `resources.go`, `health.go`. Auth in `auth.go`. Redaction in `redact.go`.

### 4.4 Supporting binaries

- `cmd/rimsky-migrate/` — runs pending migrations. Session-level advisory lock held for the duration of the batch.
- `cmd/rimsky-conformance/` — the protocol-conformance test suite (Go binary). Run against any executor endpoint to validate conformance.
- `cmd/rimsky-conformance-probe/` — the stub-mode probe issued by `rimsky-conformance` at startup when `--require-stub-mode` is set.

---

## 5. Blessed invariants in source

Each invariant from `node-graph-design.md` §11 is annotated `@blessed-invariant` in the Go source and exercised by a scenario test. Pointers:

### 5.1 State machine rejects illegal transitions

- **File:** `core/node/state.go`
- **Context:** `NextState(current, requested, reason)` returns an error if the transition is illegal. In particular, `running → running` under reason `dispatch_claimed` is not silently idempotent.
- **Storage layer enforcement:** `core/storage/postgres/nodes.go` — the `UpdateState` method calls into the state machine; no short-circuit on `from == to`.
- **Scenario test:** `state-machine-same-state-rejected` (in `test/scenarios/`).

### 5.2 Dispatch claim brackets run

- **File:** `core/queue/postgres/queue.go`
- **Context:** tag-limit counts come from `rimsky_dispatch.claimed_by IS NOT NULL`. The claim window exactly brackets the node's `running` window. The invariant is annotated at the top of the claim query.
- **Interface contract:** `core/queue/interface.go` documents the invariant at the interface level so any alternate implementation must respect it.

### 5.3 Per-tag locks acquired in sorted order

- **File:** `core/queue/postgres/queue.go`
- **Context:** when a dispatch row's node has multiple concurrency tags, advisory-lock acquisition sorts them lexicographically before calling `pg_advisory_xact_lock` on each. Prevents deadlock between concurrent claims sharing a tag subset.

### 5.4 Claimant-guarded release

- **File:** `core/queue/postgres/queue.go`
- **Context:** `ReleaseClaim(dispatchID, expectedClaimedBy)` is a no-op if the row's current `claimed_by` does not match `expectedClaimedBy`. Prevents stale orphan sweeps from nulling live claims.

### 5.5 Verify-before-run

- **File:** `core/supervisor/runner.go`
- **Context:** the runner re-reads `claimed_by` on the dispatch row (`args.Queue.GetClaimedBy`) immediately after claim and before calling the executor. If ownership has moved, logs `orphaned_claim_lost_race` and bails.
- **Scenario test:** `verify-before-run-race` — orchestrates the race explicitly.

### 5.6 Generous orphan-claim cutoff

- **File:** `core/scheduler/scheduler.go`
- **Context:** the orphan-claim sweep uses `5 × heartbeat_timeout` as its default cutoff. Configurable, but the default is the safety-critical value.

### 5.7 Advisory lock on scheduler tick

- **File:** `core/scheduler/scheduler.go`
- **Context:** `pg_try_advisory_lock(SCHEDULER_TICK_KEY)` at the top of each tick. If another replica holds it, skip this tick. Prevents multi-replica double-ticks.

### 5.8 Session advisory lock on migrations

- **File:** `core/migrations/runner.go`
- **Context:** the migration runner acquires a session-level `pg_advisory_lock` for the duration of a migration batch. Released at session close; prevents concurrent-runner migration-table corruption.

---

## 6. Library entry points

Rimsky's runtime subsystems are library-first: the binaries in `cmd/` are thin wrappers around library calls in `core/config/`. Consumers who want to embed rimsky in their own process (custom wiring, custom auth, custom resource registration) call the library entry points directly.

```go
package rimsky

// StartScheduler starts a scheduler process and returns a handle for
// lifecycle control (Shutdown, Health).
func StartScheduler(cfg SchedulerConfig) (SchedulerHandle, error)

// StartSupervisor starts a supervisor process. SupervisorID must be unique
// across the deployment; Resolver maps executor names to endpoints+transports;
// GetResource is the resource-registry lookup.
func StartSupervisor(cfg SupervisorConfig) (SupervisorHandle, error)

// StartControlAPI binds host:port (port=0 for OS-assigned) and starts serving.
func StartControlAPI(cfg ControlAPIConfig) (ControlAPIHandle, error)
```

Each handle exposes:

- `Shutdown(ctx context.Context) error` — graceful shutdown with context timeout.
- `Health() HealthReport` — current health status with subsystem details.

Configuration structs:

- `SchedulerConfig` — Postgres URL, tick interval, heartbeat/orphan timeouts, advisory-lock key, logger.
- `SupervisorConfig` — `SupervisorID`, Postgres URL, concurrency limit, heartbeat interval, claim poll interval, `Resolver` for executor endpoints, `GetResource` for resource lookup, SQL connections map, callback host/port, logger.
- `ControlAPIConfig` — Postgres URL, bind host/port, optional `Authenticator`, logger.

Code locations: `core/config/scheduler.go`, `core/config/supervisor.go`, `core/config/controlapi.go`.

### 6.1 Reference binaries

The shipped binaries in `cmd/` are environment-variable readers that build the config structs and call the library entry points. Each handles SIGTERM / SIGINT with a context-bounded graceful shutdown.

Example, `cmd/rimsky-scheduler/main.go`:

```go
cfg := config.SchedulerConfig{
    PostgresURL: os.Getenv("RIMSKY_PG_URL"),
    TickInterval: parseDuration("RIMSKY_SCHED_TICK", 1500*time.Millisecond),
    // ...
}
handle, err := rimsky.StartScheduler(cfg)
// SIGTERM handler: handle.Shutdown(ctx)
```

Binaries are the convenient default. Libraries are the extensibility surface.

---

## 7. Where executors run

Executors do not run inside the orchestrator process. They are peer services — separate processes, possibly separate machines, possibly separate clusters. They register no runtime state with rimsky; they are pointed-at via supervisor configuration.

### 7.1 Executor resolution

A supervisor's YAML config declares a map from executor name → endpoint + transport:

```yaml
executors:
  claude-agent:
    transport: grpc
    endpoint: "${CLAUDE_AGENT_ENDPOINT:-http://claude-agent:9090}"
    tls: optional
  http-node:
    transport: http
    endpoint: "${HTTP_NODE_ENDPOINT:-http://http-node:9091}"
```

At dispatch time:

1. Supervisor claims a dispatch row for a node with `executor: "claude-agent"`.
2. Supervisor looks up `"claude-agent"` in its config → `{transport: grpc, endpoint: "http://claude-agent:9090"}`.
3. Supervisor constructs the RPC call via the resolver (`core/executor/resolver.go`) and the appropriate client (`core/executor/client.go` for gRPC, `core/executor/client_http.go` for HTTP).
4. If the executor name is not in this supervisor's config: the supervisor does not attempt the call. It logs `unresolved_executor`, routes through `on_error(unresolved_executor)`, and releases the claim.

The supervisor's configured executor list also serves as the dispatch-queue claim filter: a supervisor with config entries for `claude-agent` and `http-node` only claims dispatch rows whose node's executor is one of those. Different supervisor pools can specialize (e.g. a pool with only `claude-agent` that runs on high-memory nodes).

### 7.2 Why peer services

See `node-graph-design.md` §10.11. Summarized: peer-service executors can be in any language, can have any runtime needs (GPU, subprocess spawning, persistent internal state), can fail and restart independently of the orchestrator. The orchestrator never sees executor internals; only the protocol.

### 7.3 Async handoff

Executors with long-running internal work (e.g. `claude-agent` spawning Claude CLI subprocesses) use the protocol's async-handoff pattern: they respond to `Execute` with `AsyncAccepted` and later POST the terminal outcome to the supervisor's callback URL. The supervisor holds the dispatch claim and node state during the async period, with heartbeat-loss sweep as the backstop against executors that never report back. See `protocol.md` for the full contract.

The supervisor's callback endpoint runs alongside its main loop (`core/supervisor/callback.go`). Registration in `rimsky_supervisors` includes `callback_host` and `callback_port`, which the supervisor passes to executors as `ExecuteRequest.callback_url`.

---

## 8. Storage

### 8.1 Postgres-backed

All state lives in Postgres. The orchestrator uses `jackc/pgx/v5`, in `database/sql`-compat mode for most queries and native mode for hot paths (dispatch-queue claim with `FOR UPDATE SKIP LOCKED`).

### 8.2 Core tables

- `rimsky_templates` — stored templates with validated schema.
- `rimsky_instances` — template instantiations.
- `rimsky_nodes` — per-instance nodes (state, metadata, executor name, schedule ref).
- `rimsky_dispatch` — work queue; one row per outstanding dispatch.
- `rimsky_schedules` — cron + next-fire time for scheduled nodes.
- `rimsky_supervisors` — supervisor registry with heartbeats, callback endpoints.
- `rimsky_resources` — resource registry (path, implementation, config).
- `rimsky_resource_versions` — versioned resource data (inline-jsonb uses `data`; external-sql uses `data_ref`).
- `rimsky_events` — append-only log; primary audit trail.
- `rimsky_migrations` — migration bookkeeping.

Code location: `core/storage/postgres/` has one file per table cluster. `core/storage/interfaces.go` declares the `StoreBackend` aggregation.

### 8.3 Migrations

`core/migrations/*.sql`, numbered, applied in order, tracked in `rimsky_migrations`. Embedded into the binary via `//go:embed`. The migration runner (`core/migrations/runner.go`) holds a session-level advisory lock to prevent concurrent-runner corruption.

v1 owns its own migration line. The first migration (`001-initial.sql`) defines the full Go v1 schema — it is not a diff against any predecessor.

---

## 9. Distribution model

### 9.1 Go module

One Go module: `github.com/fallguy/rimsky/core`. Published via standard Go module proxy at v1.0.0 when the OSS extraction completes. Consumers who want to embed rimsky (custom wiring, custom auth, custom-registered resources) import this module.

### 9.2 Reference binaries

Each binary in `core/cmd/` is buildable standalone via `go build ./cmd/rimsky-scheduler` etc. CI produces reproducible builds tagged with the release version.

### 9.3 Docker images

One image per binary, plus one per reference executor:

- `rimsky-scheduler` (from `core/cmd/rimsky-scheduler/`)
- `rimsky-supervisor` (from `core/cmd/rimsky-supervisor/`)
- `rimsky-control-api` (from `core/cmd/rimsky-control-api/`)
- `rimsky-migrate` (from `core/cmd/rimsky-migrate/`)
- `rimsky-conformance` (from `core/cmd/rimsky-conformance/`)
- `rimsky-http-node` (from `executors/http-node/`)
- `rimsky-claude-agent` (from `executors/claude-agent/`)

Per-component Dockerfiles live in `deploy/Dockerfile.*`. Images published to a public registry on release.

### 9.4 Docker Compose reference deployment

`deploy/docker-compose.yml` stands up a working rimsky deployment on a laptop: Postgres + scheduler + supervisor + control-api + one or more executors. Target: `docker compose up` to a working deployment in under 60 seconds. Used for local development, conformance-suite runs, and quickstart docs.

### 9.5 Helm chart and Kubernetes manifests

`deploy/kubernetes/` contains a Helm chart and plain manifests for production deployments. Chart supports horizontal scaling of supervisors, multi-replica scheduler (with advisory-lock tick guard), and external-Postgres configuration.

### 9.6 Polyglot artifacts

The `claude-agent` executor is published to npm as `@rimsky/executor-claude-agent` (TypeScript consumers) and as a Docker image (any consumer). Its npm package is the only non-Go artifact in v1. Future executors in other languages follow the same shape: their own language's package registry + a Docker image.

---

## 10. Observability

### 10.1 Logging

Stdlib `log/slog` throughout. JSON output by default, field-structured. Request-scoped correlation IDs through context. No Zap, no Zerolog.

### 10.2 Metrics

Prometheus format at `/metrics` on each process. Core metrics:

- Dispatch queue depth.
- Claim latency.
- Node state distribution.
- Executor RPC latency by executor name.
- Scheduler tick latency.
- Postgres connection pool stats.

### 10.3 Events

`rimsky_events` is the primary audit trail. Metrics and logs are derived; events are the source of truth. See `node-graph-design.md` §9.5 for the event kinds.

### 10.4 Health

`/health` on each process returns `{status: ok | degraded, details}`. The control API's `/health` aggregates all supervisors and (where configured) probes executor health endpoints.

---

## 11. Testing strategy

### 11.1 Unit tests (`*_test.go` alongside source)

- State machine transition table coverage (`core/node/state_test.go`).
- Policy evaluator (`core/node/policy_test.go`).
- Template validator (`core/node/template_validator_test.go`).
- Backoff/jitter math (`core/node/backoff_test.go`).
- Quality-rule evaluators (`core/qualityrule/rules_test.go`).
- Storage interface methods (`core/storage/postgres/postgres_test.go`).

### 11.2 Integration tests (testcontainers-go + Postgres)

Every store interface method, dispatch-queue claim contention, orphan-claim sweep, migration runner idempotency and concurrent-run safety.

### 11.3 Scenario tests

`test/scenarios/` — full-stack end-to-end via the scenario harness (`core/scenario/harness.go`). Covers happy-path executor runs, pure-cascade propagation, scheduled nodes, fan-out, cascade invalidate, give-up, double-buffering, rollback via `restore_version`, agentic async handoff, executor-blocked, unresolved-executor, heartbeat-loss reclaim, orphaned-claim cleanup, the `verify-before-run-race` invariant check, the `state-machine-same-state-rejected` invariant check, concurrency-tag limits, no-op commits.

All scenarios use `SystemClock` against testcontainers Postgres (real `NOW()` governs claim eligibility).

### 11.4 Protocol conformance

`cmd/rimsky-conformance/` validates any executor endpoint against the protocol. Shipped as a Docker image for executor authors to run in CI. See `protocol.md` for contract details. Stub-mode requirement (§14.4 of the spec) keeps conformance deterministic for LLM-calling executors.

---

## 12. Go library choices

- **Postgres:** `jackc/pgx/v5`.
- **HTTP router:** `go-chi/chi`.
- **gRPC:** `google.golang.org/grpc`.
- **Config parsing:** `github.com/knadh/koanf` with YAML + env providers.
- **CLI (binaries):** stdlib `flag`.
- **Structured logging:** stdlib `log/slog`.
- **Validation (JSON Schema):** `github.com/xeipuuv/gojsonschema`.
- **Cron parsing:** `github.com/robfig/cron/v3`.
- **Testing:** `testify/require` + `testcontainers-go`.

Deliberate omissions: no Viper, no Cobra, no Zap, no Gin, no Echo. Lighter stack, fewer surprises.
