# Architecture

Implementation shape of Rimsky after the 2026-05-04 layer-crystallization refactor (Go). This document covers how the code is organized into four layers, which processes run, which Go modules carry which code, and where the conceptual invariants are enforced in source. Conceptual model lives in `node-graph-design.md`. Vocabulary lives in `glossary.md`.

The three contract documents in `docs/specs/` are the authoritative sources for layer responsibilities; this document explains how those contracts are realized in code.

- `docs/specs/2026-05-04-foundation-contract.md` — what foundation owns.
- `docs/specs/2026-05-04-modeling-layer-contract.md` — what modeling owns.
- `docs/specs/2026-05-04-service-protocol-contract.md` — the three peer-service protocols.

---

## 1. Four-layer model

Rimsky is structured as four conceptual layers. Higher layers depend on lower layers; lower layers MUST NOT reach into higher layers.

```
┌──────────────────────────────────────────────────────────────┐
│ Layer 4: Bundled services + examples                         │
│   stores/{filesystem,postgres,stub}/                         │
│   executors/{http-node,claude-agent,stub}/                   │
│   compose recipes, agentic-workflow examples                 │
└──────────────────────────────────────────────────────────────┘
              │ implements
              ▼
┌──────────────────────────────────────────────────────────────┐
│ Layer 3: Service protocols (cross-cutting)                   │
│   ClaimProducer, Executor, LifecycleSubscriber               │
│   gRPC services + Go interface types in protocols/ module    │
└──────────────────────────────────────────────────────────────┘
              ▲                                ▲
              │ calls                          │ calls
              │                                │
┌─────────────┴────────────────┐  ┌────────────┴─────────────────┐
│ Layer 2: Modeling            │  │ Layer 1: Foundation          │
│   Templates, instances,      │  │   cascade engine             │
│   frames, schedules,         │  │   lock manager               │
│   attributes, control-plane  │  │   integration                │
│   API, YAML config shape,    │◀─┤   (foundation/ module)       │
│   public vocabularies.       │  │                              │
│   In root module.            │  │                              │
└──────────────────────────────┘  └──────────────────────────────┘
```

Service protocols are cross-cutting: foundation calls a subset (claim verbs at acquisition/terminal; executor dispatch at worker-request issue), modeling calls a different subset (lifecycle hooks at control-plane state transitions).

---

## 2. Three Go modules

The four layers are realized as three Go modules tied together by `go.work`:

| Module | Path | Layer | Depends on |
|---|---|---|---|
| `github.com/fallguy/rimsky/foundation` | `foundation/` | Layer 1 | stdlib + `protocols` + minimal third-party (`pgx`, `uuid`, `modernc.org/sqlite`) |
| `github.com/fallguy/rimsky/protocols` | `protocols/` | Layer 3 | stdlib + grpc + protobuf only |
| `github.com/fallguy/rimsky` (root) | `.` | Layers 2 + 4 | `foundation` + `protocols` + stdlib |

External service authors who write a custom claim-producer or executor in Go import `github.com/fallguy/rimsky/protocols` only — no transitive dependency on rimsky's persistence drivers, gRPC server scaffolding, or modeling code.

`go.work` at the repo root coordinates the three modules so `go test ./...`-style operations work across the workspace during development. CI publishes each module independently.

---

## 3. Repository layout

```
rimsky/                              # repo root
├── foundation/                      # Layer 1 — Go module github.com/fallguy/rimsky/foundation
│   ├── go.mod
│   ├── cascade/                     # node-state machine + cascade signal
│   ├── locks/                       # ClaimProducer interface, ClaimSpec/NamedLockSpec/ClaimResult,
│   │                                # ModeCoexists + ScopesByteEqual helpers, in-Go storetest fake.
│   ├── integration/                 # Conductor (per-tick supervisor + sweeps), atomic acquisition,
│   │                                # auto-terminal mechanism, unified terminal-decision engine,
│   │                                # callback server, orphan reaper. remote/ holds the only concrete
│   │                                # gRPC client to ClaimProducer impls.
│   ├── persistence/                 # Driver protocol (Driver, AdvisoryLocker, Queue, Store umbrella,
│   │   ├── postgres/                # LockHoldersStore, ClaimHoldersStore, FrameStore, per-feature
│   │   ├── sqlite/                  # interfaces). Postgres + SQLite implementations.
│   │   └── conformance/             # Cross-driver conformance tests.
│   └── internal/                    # foundation-private helpers; depguard forbids modeling import.
│
├── protocols/                       # Layer 3 — Go module github.com/fallguy/rimsky/protocols
│   ├── go.mod
│   ├── claimproducer/               # ClaimProducer Go interface + types
│   ├── lifecycle/                   # LifecycleSubscriber Go interface + types
│   ├── executor/                    # Executor Go interface + types
│   └── proto/v1/                    # Proto sources + generated bindings (gen/).
│       ├── claim_producer.proto
│       ├── lifecycle.proto
│       ├── executor.proto
│       └── events.proto
│
├── modeling/                        # Layer 2 — root Go module github.com/fallguy/rimsky
│   ├── attribute/                   # Substitution engine, JSON Schema validation.
│   ├── controlapi/                  # HTTP+JSON routes, lifecycle fan-out, instance terminator.
│   ├── frame/                       # Frame engine (coalesce / serial_queue resolution).
│   ├── observability/               # Public observability API mounted at /v1/observability/* on control-api.
│   ├── qualityrule/                 # Built-in quality rule implementations.
│   ├── executor/                    # Protocol-client helpers (gRPC + HTTP bridge).
│   ├── cli/                         # rimsky-cli library (HTTP client + verb handlers + compose/).
│   ├── config/                      # rimsky.yml parsing; library entry points (StartScheduler, etc.).
│   ├── scheduler/                   # Modeling-side schedule + pure-cascade tick orchestration.
│   ├── shared/                      # Cross-package modeling types.
│   ├── node/                        # Modeling-side node policy (backoff, inheritance, template).
│   ├── scenario/                    # Scenario-test harness.
│   ├── template/canonical/          # RFC 8785 JCS canonicalization for template content addressing.
│   └── internal/pgtest/             # testcontainers-go helpers for modeling-side integration tests.
│
├── stores/                          # Layer 4 — bundled claim-producer reference impls
│   ├── filesystem/                  # Concrete-paths-only filesystem store.
│   ├── postgres/                    # Items-table-backed postgres store with pick policies.
│   └── stub/                        # In-memory stub for scenario tests.
│
├── executors/                       # Layer 4 — bundled executor reference impls
│   ├── http-node/                   # Go; "POST userdata to URL" generic executor.
│   ├── claude-agent/                # TypeScript; spawns Claude CLI; npm package.
│   └── stub/                        # Go; deterministic test fixture.
│
├── cmd/                             # Reference binaries (each is a `package main`)
│   ├── rimsky-scheduler/
│   ├── rimsky-supervisor/
│   ├── rimsky-control-api/
│   ├── rimsky-migrate/
│   ├── rimsky-cli/
│   ├── rimsky-conformance/
│   ├── rimsky-conformance-probe/
│   ├── rimsky-claim-producer-conformance/
│   └── rimsky-entrypoint/           # Unified-image PID-1 entrypoint.
│
├── conformance/                     # Cross-process scenario harness for conformance binaries.
├── deploy/                          # Dockerfiles, docker-compose, helm chart.
├── dashboards/                      # Reference dashboards consuming the observability protocol.
├── test/                            # Cross-layer scenario tests + smoke fixture.
├── docs/                            # Architecture docs, contracts, glossary, history.
├── go.work                          # Workspace tying foundation + protocols + root modules.
└── go.mod                           # Root module github.com/fallguy/rimsky
```

The repo's three Go modules are tied together by `go.work`. The pre-Phase-2 `core/` directory is gone; everything that used to live under it has migrated to `foundation/`, `modeling/`, `stores/`, `executors/`, `cmd/`, or `protocols/proto/v1/`.

---

## 4. Package import rules (enforced)

`.golangci.yml` `depguard` enforces two non-negotiable rules; violations break the build:

- **`pgx-isolation`** — `github.com/jackc/pgx/v5` is allowed only in `foundation/persistence/postgres/`, `foundation/internal/pgtest/`, `cmd/`, `modeling/internal/pgtest/`, `modeling/scenario/`, `stores/`, and `test/smoke/`. Modeling business logic, foundation/integration, foundation/locks, foundation/cascade, and protocols MUST NOT import pgx directly.
- **`foundation-internal-isolation`** — only packages under `foundation/` may import packages under `foundation/internal/`. The modeling layer and bundled services cannot reach into foundation internals.

In addition to the depguard rules, the layer rules of §1 apply:

- Foundation MUST NOT import modeling.
- Foundation MUST NOT import bundled services.
- Modeling MAY import foundation and protocols.
- Bundled services (`stores/*`, `executors/*`, `cmd/*`) MAY import foundation, protocols, and modeling.
- Protocols MUST NOT import foundation or modeling. (Protos + Go interfaces only.)

If you need to share logic between modeling subsystems, put it in `modeling/shared/`. If the logic is strictly foundation-level (cascade or lock-manager primitives), it goes in `foundation/cascade/` or `foundation/locks/`. Foundation never imports modeling.

The three runtime processes (`rimsky-scheduler`, `rimsky-supervisor`, `rimsky-control-api`) communicate **only through Postgres** — they cannot import each other's `cmd/` packages either. Cross-process coupling lives in shared SQL state.

---

## 5. Three long-running processes

Rimsky ships as three independent long-running processes plus a handful of supporting binaries.

### 5.1 Scheduler

Binary: `cmd/rimsky-scheduler/`. Responsibilities per tick (default interval 1.5s):

1. **Advisory-lock guard.** `pg_try_advisory_lock(SCHEDULER_TICK_KEY)` (Postgres) or `sync.Mutex` (SQLite). Skip the tick if another replica holds it.
2. **Schedule firing.** For each scheduled node whose cron indicates a fire is due: emit `invalidate`; advance `next_fire_at`.
3. **Pure-cascade sweep.** For each `stale` non-executor node with all dependencies `fresh`: transition `stale → fresh` inline; emit `recalculate` to dependents.
4. **Foundation sweeps** (in `foundation/integration/conductor.go`): stale-heartbeat sweep, orphaned-claim sweep (5×heartbeat cutoff on `phase='active'` rows), claim-handle orphan sweep, ready sweep.
5. **Frame engine tick.** `modeling/frame/engine.go::RunTick` runs under the same advisory lock.

The scheduler's tick loop lives in `modeling/scheduler/scheduler.go`; foundation primitives it calls (`SweepStaleHeartbeats`, `SweepOrphanedClaims`, `SweepReady`, `SweepLockHolders`) live in `foundation/integration/`.

### 5.2 Supervisor

Binary: `cmd/rimsky-supervisor/`. Responsibilities:

1. **Register.** Upsert into `rimsky_supervisors`.
2. **Heartbeat tick.** Every `heartbeat_interval_ms`, refresh ownership timestamps.
3. **Claim.** Atomic acquisition transaction in `foundation/integration/runner_acquire.go` — claims `rimsky_worker_request`, INSERTs `rimsky_claim_handle` rows, calls `ClaimProducer.Open` over gRPC, records the producer's `address` + `realized_write_semantics`, INSERTs held-claim rows for the holding subgraph, COMMITs.
4. **Verify-before-run.** Re-read `claimed_by` immediately before dispatching the executor; bail as `orphaned_claim_lost_race` if ownership moved.
5. **Dispatch.** Resolve executor name → endpoint; call `Executor.Execute` over gRPC (or HTTP+JSON bridge).
6. **Handle terminal.** Sync terminal: process `ExecuteResponse`. Async terminal: hold the claim; receive `POST /v1/callback/{async_ack_id}` later.
7. **Apply terminal outcome.** For non-held claims: `foundation/integration/terminal_decision.go::ResolveClaimHandleTerminal` fires the producer verb and deletes the claim_handle row. For held claims: leave bookkeeping in place; auto-terminal will resolve.
8. **Auto-terminal.** `foundation/integration/auto_terminal.go::CheckAndFireResolution` — at every node terminal in a held subgraph, lock the claim_handle row, check whether all `rimsky_claim_holders` rows are non-active, and if so route through `ResolveClaimHandleTerminal` with the aggregate outcome (any-failed → Abandon, else Commit).

### 5.3 Control API

Binary: `cmd/rimsky-control-api/`. HTTP+JSON endpoints (per `modeling/controlapi/`):

- `POST /templates`, `GET /templates`, `GET /templates/{hash}`.
- `GET /tags`, `POST /tags`, `GET /tags/{tag}`, `DELETE /tags/{tag}`.
- `POST /instances`, `GET /instances`, `GET /instances/{id_or_key}`, `POST /instances/{id}/terminate`.
- `GET /nodes/{id}`, `GET /instances/{id_or_key}/nodes`.
- `POST /nodes/{id}/invalidate`, `POST /nodes/{id}/reset`.
- `GET /events` (paginated, filterable).
- `GET /v1/observability/*` (existing observability surface).
- `POST /admin/scheduled-nodes/{node_id}/force-fire` (admin-only).
- `GET /health`, `GET /metrics`.

Lifecycle events fire from `modeling/controlapi/lifecycle.go` and `modeling/controlapi/instance_terminator.go`: synchronously RPCed to subscribed `LifecycleSubscriber` peers at template/instance state transitions; idempotency tracked in `rimsky_lifecycle_idempotency`.

### 5.4 Supporting binaries

- `cmd/rimsky-migrate/` — runs pending migrations under a session-level advisory lock.
- `cmd/rimsky-conformance/` — covers Executor + LifecycleSubscriber conformance suite. Flags: `--check-executor`, `--check-lifecycle`, `--require-stub-mode`.
- `cmd/rimsky-conformance-probe/` — utility, used by `rimsky-conformance --require-stub-mode` to verify executor stub-mode at startup.
- `cmd/rimsky-claim-producer-conformance/` — covers ClaimProducer conformance suite (renamed from `rimsky-store-conformance`).
- `cmd/rimsky-cli/` — operator-facing CLI; thin client over the control-api with a `compose` subcommand.
- `cmd/rimsky-entrypoint/` — PID-1 wrapper for the unified `rimsky/all` Docker image; supervises the three runtime processes inside one container.

---

## 6. Schema (post-Phase-5)

The Phase-5 cycle of the layer-crystallization refactor consolidated the legacy split tables. End-state schema:

### 6.1 Foundation-owned tables

- **`rimsky_worker_request`** — parent run-bookkeeping row. One per dispatched run. `phase` ∈ `{'pending', 'active', 'held', 'completed'}` drives the active+held lifecycle. `claimed_by` carries the supervisor id while `phase='active'`. Replaces the legacy `rimsky_dispatch`.
- **`rimsky_claim_handle`** — child of `rimsky_worker_request` (FK with `ON DELETE SET NULL` so held claim handles outlive their parent's active terminal until auto-terminal explicitly removes them). One row per (worker_request, lock-or-claim acquired). `lock_kind` ∈ `{'named', 'scope'}`. `is_held BOOLEAN` marks claims that persist past active terminal. `realized_write_semantics` is the per-claim verdict from `ClaimProducer.Open`. Replaces the legacy `rimsky_lock_holders`.
- **`rimsky_claim_holders`** — held-claim subgraph state ledger. One row per (claim_handle, holder_node) for held subgraph members; `state` ∈ `{'active', 'completed', 'failed'}`. Auto-terminal fires when all rows for a claim_handle are non-active. The FK column is `claim_handle_id` (renamed from `lock_holder_id`).
- **`rimsky_nodes`** (split-owned with modeling) — foundation owns `has_value`, `has_outstanding_request`, `auto_recovers` (the foundation's two-bit-plus-flag state space). Modeling owns `frame_id` and `template_node_id`.
- **`rimsky_supervisors`** — supervisor heartbeat registry.
- **`rimsky_node_attributes`** — per-node typed attributes object (single row per node). Populated from source-directive substitution at dispatch and executor writeback during run; validated against the template's JSON Schema.

### 6.2 Modeling-owned tables

- **`rimsky_templates`** — content-addressed template registry; `id TEXT` is `sha256-<64-hex>`.
- **`rimsky_template_tags`** — movable tag → content hash aliases.
- **`rimsky_instances`** — template instantiations; FK `template_hash TEXT`; `instance_key` nullable.
- **`rimsky_schedules`** — cron + next-fire time.
- **`rimsky_frames`** — per-instance frame queue; partial unique index `uq_rimsky_frames_running` enforces at-most-one running frame per instance.
- **`rimsky_events`** — append-only log; primary audit trail.
- **`rimsky_lifecycle_idempotency`** — per-(peer, event-type, object-id) idempotency for LifecycleSubscriber events. Renamed from `rimsky_store_lifecycle`.

Code locations: `foundation/persistence/postgres/` and `foundation/persistence/sqlite/` carry the SQL — one file per table cluster (`worker_request.go`, `claim_handle.go`, `claim_holders.go`, `node_attributes.go`, `events.go`, `schedules.go`, `supervisors.go`, `frames.go`, `templates.go`, `instances.go`, `lifecycle_idempotency.go`, `template_tags.go`). Per-driver `migrations/` directories carry the embedded SQL FS consumed by `foundation/persistence/migrations.go::Migrator`.

### 6.3 Migrations

Driver-agnostic runner: `foundation/persistence/migrations.go::Migrator` consults the per-driver embedded SQL FS, applies migrations in lexical order, records each in `rimsky_migrations`. Postgres acquires a session-level advisory lock for the duration of the batch; SQLite holds an in-process mutex (single-process is the only supported topology).

Pre-v1 break-freely applies: migrations are rewritten in place when a refactor would be cleaner without a migration path.

---

## 7. Blessed invariants in source

Each invariant is annotated `@blessed-invariant N` in source and exercised by a scenario test. Locations are post-Phase-6:

| # | Statement | Code location |
|---|-----------|---------------|
| 1 | State machine rejects illegal transitions (`running → running` errors). | `foundation/cascade/state.go` |
| 2 | Worker-request claim brackets the running window. | `foundation/persistence/postgres/queue.go` |
| 3 | Multi-handle acquisition uses deterministic sorted order. | `foundation/integration/runner_acquire.go` |
| 4 | Claimant-guarded release on every claim_handle delete and worker-request claimed_by nullification. | `foundation/persistence/postgres/queue.go`; `foundation/integration/runner_acquire.go`; `foundation/integration/orphan_reaper.go` |
| 5 | Verify-before-run. | `foundation/integration/runner_acquire.go` |
| 6 | Orphan-claim cutoff is `5 × heartbeat_interval`. | `foundation/integration/conductor.go`; `foundation/integration/orphan_reaper.go` |
| 7 | Advisory lock on the dispatch tick. | `foundation/persistence/postgres/advisory_locker.go`; `foundation/persistence/sqlite/advisory_locker.go` |
| 8 | Session advisory lock on migrations. | `foundation/persistence/migrations.go`; per-driver advisory_locker.go |
| 9a | Lock state lives only in foundation persistence; producers do not persist lock state. | `foundation/locks/interface.go` |
| 9b | ClaimProducer impls do not internally serialize on lock-shaped predicates. | `foundation/locks/interface.go` |
| 10 | Lock acquisition is atomic with the worker-request claim (rimsky-side). | `foundation/integration/runner_acquire.go` |
| 11 | Userdata is opaque to rimsky. | `modeling/attribute/substitution.go`; `protocols/proto/v1/executor.proto` |
| 12 | Attributes validate twice (dispatch-time post-substitution; commit-time post-writeback). | `modeling/attribute/validate.go` |
| 13 | Held-claim resolution is auto-terminal, single, and aggregate-outcome-driven. | `foundation/integration/auto_terminal.go`; `foundation/integration/terminal_decision.go` |
| 15 | `ClaimProducer.Open` fires inside the rimsky-side acquisition transaction. | `foundation/integration/runner_acquire.go` |
| 20 | Claim content (payload, address, scope) is inert in Rimsky. | `foundation/locks/types.go`; `modeling/attribute/substitution.go::walkPath` |

(Invariant 14 was retired post-v3; the numbering is preserved.)

Scenario tests under `test/scenarios/` (e.g. `verify_before_run_race_test.go`, `state_machine_same_state_rejected_test.go`, the `locks/`, `stores/`, `claim_stores/`, `frame_resolution/`, and `lifecycle/` subdirectories) catch regressions of these. The smoke test (`test/smoke/`) drives 100 sequential force-fires through the full pipeline as the integration backstop.

---

## 8. Library entry points

Rimsky's runtime processes are library-first: the binaries in `cmd/` are thin wrappers around library calls in `modeling/config/`. Consumers who want to embed rimsky in their own process call the library entry points directly.

```go
package config

func StartScheduler(cfg SchedulerConfig) (SchedulerHandle, error)
func StartSupervisor(cfg SupervisorConfig) (SupervisorHandle, error)
func StartControlAPI(cfg ControlAPIConfig) (ControlAPIHandle, error)
```

Each handle exposes:

- `Shutdown(ctx context.Context) error` — graceful shutdown with context timeout.
- `Health() HealthReport` — current health status.

Configuration structs are documented in the operator guide; the unified `rimsky.yml` shape is in `modeling/config/`.

---

## 9. Where executors and claim-producers run

Both are peer services — separate processes, possibly separate machines, possibly separate clusters. They register no runtime state with rimsky.

### 9.1 Discovery

Operators declare peers under `claim_producers:` and `executors:` blocks in `rimsky.yml`. Each entry has an `endpoint` plus an optional `protocols:` list (default: a single-element list matching the block name). At process startup, control-api / supervisor / scheduler dial each entry and run the `Capabilities()` handshake per declared protocol; any failure (unreachable, mismatch) fails the rimsky process at startup.

### 9.2 Claim-producer dispatch

Foundation calls `ClaimProducer.Open` inside the acquisition transaction (blessed invariant 15). At terminal (active or held), foundation calls `Commit` / `Abandon` / `Release` via `ResolveClaimHandleTerminal`. The per-claim `realized_write_semantics` returned from `Open` is recorded on the claim_handle row and used as the conflict-predicate input — byte-equal scope must agree on `realized_write_semantics` (uniformity invariant; producers enforce).

### 9.3 Executor dispatch

Supervisor resolves `node.executor` to `endpoint + transport` from `rimsky.yml`. Constructs `ExecuteRequest` (substituted attributes; opaque userdata; callback URL). Calls `Executor.Execute` over gRPC or HTTP+JSON. Sync terminal: response carries the outcome. Async terminal: response carries an `async_ack_id`; executor later POSTs `${callback_url}/v1/callback/{async_ack_id}` with body keyed `type` (not `kind`).

The supervisor's callback endpoint runs alongside its main loop (`foundation/integration/callback.go`). `callback.advertise_host` (YAML) or `RIMSKY_SUPERVISOR_CALLBACK_ADVERTISE_HOST` (env) controls the peer-reachable hostname executors receive.

---

## 10. Distribution model

### 10.1 Go modules

Three Go modules are independently importable:

- `github.com/fallguy/rimsky/foundation` — minimal embedding for custom orchestrator integrations.
- `github.com/fallguy/rimsky/protocols` — minimal dependency for external service authors.
- `github.com/fallguy/rimsky` — full repo, including bundled services and cmd binaries.

### 10.2 Docker images

One image per binary, plus one per reference executor and per reference claim-producer. Per-component Dockerfiles in `deploy/Dockerfile.*`:

- `rimsky-scheduler`, `rimsky-supervisor`, `rimsky-control-api`, `rimsky-migrate`, `rimsky-conformance`, `rimsky-claim-producer-conformance`, `rimsky-cli`.
- `rimsky-http-node`, `rimsky-claude-agent`.
- `rimsky-store-filesystem`, `rimsky-store-postgres`, `rimsky-store-stub`.
- `rimsky/all` — the unified image bundling the three runtime processes under a single PID-1 entrypoint (`rimsky-entrypoint`); defaults to `driver: sqlite` for solo development. NOT for multi-replica deployments — running with replicas > 1 creates independent SQLite databases.

### 10.3 Docker Compose reference deployment

`deploy/docker-compose.yml` brings up Postgres + migrate + scheduler + supervisor + control-api + bundled claim-producers + bundled executors. Control API on `:8080`; Postgres on `:5544`. Target: working deployment in under 60 seconds.

### 10.4 Kubernetes / Helm

`deploy/kubernetes/rimsky-chart/` — Helm chart and plain manifests for production deployments. **Known stale**: env-var names lag behind the binaries. Verify before deploying.

### 10.5 Polyglot artifacts

The `claude-agent` executor is published to npm as `@rimsky/executor-claude-agent` and as a Docker image. Future executors in other languages follow the same shape.

---

## 11. Observability

### 11.1 Logging

Stdlib `log/slog` throughout. JSON output by default, field-structured. Request-scoped correlation IDs through context. No Zap, no Zerolog.

### 11.2 Metrics

Prometheus format at `/metrics` on each process. Core metrics: dispatch queue depth, claim latency, node state distribution, executor RPC latency, scheduler tick latency, Postgres connection pool stats.

### 11.3 Events

`rimsky_events` is the primary audit trail. Metrics and logs are derived; events are the source of truth.

### 11.4 Health

`/health` on each process returns `{status: ok | degraded, details}`. The control-api `/health` aggregates supervisors and probes peer health endpoints.

### 11.5 Public observability API

`modeling/observability/` mounts the public surface at `/v1/observability/*` on `rimsky-control-api`. The observability surface exposes only `claim_id` and `scope_data` for claim-handle responses — payload/address bytes never traverse rimsky-side endpoints; clients follow `claim_id` to the producer's own observability protocol when payload/address inspection is required (per the dashboard/observability spec at `docs/specs/2026-05-02-dashboard-and-observability-design.md`).

`dashboards/rimsky-dashboard/` is the reference dashboard (React + Vite + TypeScript SPA + Hono Node server). Like `executors/claude-agent/`, it is forbidden from importing `foundation/`, `modeling/`, or `protocols/`; it consumes the observability protocols only.

---

## 12. Testing strategy

### 12.1 Unit tests (`*_test.go` alongside source)

State-machine transitions; cascade-target predicate; backoff math; quality-rule evaluators; persistence-layer methods. Cross-driver conformance lives at `foundation/persistence/conformance/`.

### 12.2 Integration tests (testcontainers-go + Postgres)

Every claim-handle method, dispatch-queue claim contention, orphan-claim sweep, migration runner idempotency. Each scenario boots its own Postgres container (`postgres:14-alpine` or `postgres:15`).

### 12.3 Scenario tests

`test/scenarios/` — full-stack end-to-end via `modeling/scenario.Start` against pre-launched producer-services on ephemeral ports. Bucketed by surface: top-level, `locks/` (named-lock counts, scope-lock conflict, atomic acquisition, sorted acquisition no-deadlock, claimant-guarded release), `stores/` (filesystem direct write, disjoint vs overlapping scopes, pool specialization), `attributes/`, `claim_stores/` (pick policies, on-commit/on-give-up), `frame_resolution/`, `lifecycle/`.

The smoke fixture in `test/smoke/setup.go` is the reference example.

### 12.4 Protocol conformance

- `cmd/rimsky-conformance/` — Executor + LifecycleSubscriber.
- `cmd/rimsky-claim-producer-conformance/` — ClaimProducer.

Both are shipped as Docker images so external authors can run them in CI.

---

## 13. Go library choices

- **Postgres:** `jackc/pgx/v5`.
- **SQLite:** `modernc.org/sqlite` (pure-Go, no CGO).
- **HTTP router:** `go-chi/chi`.
- **gRPC:** `google.golang.org/grpc`.
- **Config parsing:** `github.com/knadh/koanf` with YAML + env providers.
- **CLI (binaries):** stdlib `flag`.
- **Structured logging:** stdlib `log/slog`.
- **Validation (JSON Schema draft-07):** `github.com/santhosh-tekuri/jsonschema/v5`.
- **Cron parsing:** `github.com/robfig/cron/v3`.
- **Testing:** `testify/require` + `testcontainers-go`.

Deliberate omissions: no Viper, no Cobra, no Zap, no Gin, no Echo. Lighter stack, fewer surprises.

---

## 14. Where to look first

- **Foundation contract:** `docs/specs/2026-05-04-foundation-contract.md` — what foundation owns.
- **Modeling contract:** `docs/specs/2026-05-04-modeling-layer-contract.md` — what modeling owns.
- **Service-protocol contract:** `docs/specs/2026-05-04-service-protocol-contract.md` — the three peer-service protocols.
- **Conceptual model:** `docs/node-graph-design.md`.
- **Operating:** `docs/operator-guide.md`.
- **Glossary:** `docs/glossary.md`.
- **Writing a claim producer:** `docs/claim-producer-author-guide.md`.
- **Writing an executor:** `docs/executor-author-guide.md`.
- **Recent changes & rationale:** `CHANGELOG.md` (long but informative).
