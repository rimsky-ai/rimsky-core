# Architecture

Implementation shape of rimsky v1 (Go). This document covers how the code is organized, which processes run, how distribution works, and where the conceptual invariants are enforced in source. Conceptual model lives in `node-graph-design.md`; wire protocol lives in `protocol.md`. Vocabulary (claim, named lock, region, selector, address, payload, intent, alias, acquirer, inheritor, holding subgraph, auto-terminal, write_semantics, pick policy) lives in `glossary.md` — the authoritative naming reference.

---

## 1. Three collections

Rimsky is built from three architecturally-distinct collections. For v1 they ship in one repository, versioned and documented together, but each is designed to separate cleanly.

### 1.1 Orchestrator

The node-graph runtime. State machine, scheduler, supervisor, control API, dispatch queue, Postgres storage, migrations. Knows nothing about LLMs. Knows stores and executors only through their interfaces / protocol.

The orchestrator is a single Go module (`github.com/fallguy/rimsky/core`). Packages within it are subject to the import-graph rules in §3. The orchestrator is the only Go-importable collection; the Go reference executor ships as a `package main` binary and Docker image, not as an importable package. Reference store implementations live under `stores/<kind>/` as standalone binaries and are dialed over gRPC.

### 1.2 Store library

Implementations of the store protocol (declared as the `Store`
interface in `core/store/`, transported over the wire per
`proto/v1/store_service.proto`). A store is a deployment-level data
backend with two primitives — **claims** (store-bound, `(store,
region, intent)` rows in `rimsky_lock_holders`) and **named locks**
(store-independent, `(name, limit)` rows). Per stores-redesign-v3, store
implementations run as **separate processes** under `stores/<kind>/`;
rimsky processes dial them at startup over gRPC and validate the
`Capabilities()` handshake. Each process has its own simple `Registry`
(name → `Store` map populated by the cmd binary's startup wiring).

The `core/store/` package exports the rimsky-side `Store` interface
(4+1 verbs: `Open` / `Commit` / `Abandon` / `Release` plus
`Capabilities`), `ClaimID` / `ClaimSpec` / `NamedLockSpec` /
`Capabilities` / `ClaimResult` / `OpenOutcome` (the value types),
`WriteSemantics` / `Intent`, the pure `ModeCoexists` and
`RegionsByteEqual` helpers, and the simple `Registry`. The only
concrete `Store` impls in this module are `core/store/remote/`
(gRPC client) and `core/store/storetest/` (unit-test fake). The
`rimsky_lock_holders` postgres helpers used by supervisor and
scheduler live in `core/persistence/postgres/lock_holders.go`
under the `persistence.LockHoldersStore` interface.

Lock state lives only in postgres (`rimsky_lock_holders`); stores never
persist lock state (blessed invariant 9a). A supervisor pool's config
lists the stores it has access to, and dispatch eligibility filters
out nodes whose required stores aren't in the local pool.

v3 ships three reference store-services under `stores/`:

- `stores/filesystem/` — direct-mode filesystem store. Concrete paths
  only (v2's glob support is dropped per spec §7.7). `Open` returns
  the path under the configured root.
- `stores/postgres/` — postgres-backed store. Supports regional access
  and store-side **pick policies** declared in the store-service's
  own config under a `pick_policies:` block (recommended convention:
  `@policy-name` selectors). The store runs its own visibility-timeout
  sweep internally and exposes a separate admin port for items
  insertion (per spec §13.1).
- `stores/stub/` — in-memory stub for scenario tests; configurable
  region table + selector handlers.

Each ships its own binary, Dockerfile, and `config-example.yml`.

### 1.3 Executor library

Reference executor services that speak the node-executor protocol. Executors run as peer services; the orchestrator calls them over the wire. v1 ships:

- `executors/http-node/` — Go. Generic "POST userdata to URL, commit response." Protocol: gRPC + HTTP bridge.
- `executors/claude-agent/` — TypeScript. Spawns Claude CLI, hosts its own internal MCP callback for the agent, reports outcome back to rimsky via protocol callback (async handoff). Published as npm package `@rimsky/executor-claude-agent` and as a Docker image.

Because executors are peer services, a new executor in Python (or Rust, or any language that can speak gRPC or HTTP+JSON) requires no orchestrator changes — only adding an entry to a supervisor's config.

### 1.4 Dashboards

Standalone reference dashboards composing the three observability protocols (Rimsky observability API on control-api, executor observability protocol per executor, store observability protocol per store). Per `docs/specs/2026-05-02-dashboard-and-observability-design.md`. v1 ships one dashboard at `dashboards/rimsky-dashboard/` (React + Vite + TypeScript SPA + Hono Node server) — a credible reference implementation, not a privileged consumer. Other dashboards may be built against the same protocols. The dashboard is a wire-protocol consumer only; like `executors/claude-agent/`, it is forbidden from importing `core/`. Bundled with the dev `docker-compose` stack on the `default` and `dashboard` profiles; opt-in for production.

The `core/observability/` package mounts the public observability API at `/v1/observability/*` on `rimsky-control-api`. It depends on `core/persistence/` (the unified persistence abstraction), `core/store/` for shared types, and the proto bindings; it MUST NOT import `core/config/`, `core/persistence/postgres/`, `core/persistence/sqlite/`, `core/scheduler/`, `core/supervisor/`, or `core/controlapi/`. Wired into the controlapi router at startup by `core/config/StartControlAPI`, which also runs the best-effort observability handshake against each declared executor and store endpoint and starts the background re-prober. Per spec §1.3 the lock-holder responses expose only `claim_id` and `region_data` — payload/address bytes never traverse Rimsky-side endpoints; clients follow `claim_id` to the store's observability protocol when payload/address inspection is required.

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
│   ├── storage/                    # storage interfaces + Postgres impls (all state tables)
│   ├── migrations/                 # numbered SQL files + runner
│   ├── store/                      # Store INTERFACE (4 verbs) + remote/ gRPC client + storetest/ in-Go fake (reference impls live under stores/<kind>/)
│   ├── attributes/                 # substitution engine + JSON Schema validation + callback handler
│   ├── qualityrule/                # builtin rule implementations
│   ├── executor/                   # protocol-client helpers (gRPC / HTTP bridge clients)
│   ├── config/                     # library entry points (StartScheduler, etc.)
│   ├── shared/                     # cross-package types, errors, clock, logger
│   ├── scenario/                   # scenario-test harness
│   ├── internal/                   # unexported helpers (pgtest, etc.)
│   ├── cli/                        # `rimsky-cli` library: HTTP client + verb handlers + compose/
│   │   ├── client.go              # one method per control-api endpoint
│   │   ├── compose/                # rimsky-compose.yml plan-and-apply
│   │   ├── clitest/                # httptest-backed fake control-api for tests
│   │   └── embedded/               # //go:embed boundary for `init` scaffold assets
│   └── cmd/                        # reference binaries (Docker entrypoints)
│       ├── rimsky-cli/             # operator-facing CLI (rimsky-compose.yml plan-and-apply)
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
- `queue/` — `DispatchQueue` interface + Postgres implementation. Imports `shared/` and `pgx`. Eligibility input carries the node's claim and named-lock requirements as `[]any` (concrete elements are `store.ClaimSpec` or `store.NamedLockSpec` values; the supervisor type-switches).
- `storage/` — storage interfaces + Postgres implementations (nodes, supervisors, lock-holders, node-attributes, claim-holders, events, migrations). Imports `shared/` and `pgx`.
- `store/` — universal 4-verb `Store` interface plus `remote/` (gRPC client; the only concrete `Store` impl in the rimsky module) and `storetest/` (in-Go fake). Reference store implementations live as standalone binaries under `stores/<kind>/` (filesystem, postgres, stub) per stores-redesign-v3 — no in-process subpackages. The interface package imports `shared/` only. `Open` is invoked over the wire; rimsky no longer shares a `pgx.Tx` with the store (the v2 `TxFromContext`/`WithTx` pattern is gone). The `rimsky_lock_holders` postgres helpers live at `core/persistence/postgres/lock_holders.go` under `persistence.LockHoldersStore`. **Allowed importers:** `persistence/`, `scheduler/`, `supervisor/`, `controlapi/`, `attributes/`, `config/`, `cmd/*`, `scenario/`.
- `attributes/` — substitution engine, JSON Schema validation, callback handler, postgres helpers for `rimsky_node_attributes`. Imports `shared/`, `storage/`, `store/` (for claim-payload reads), and `pgx`. **Allowed importers:** `supervisor/`, `controlapi/`, `scheduler/`, `config/`, `cmd/*`, `scenario/`.
- `qualityrule/` — builtin quality-rule implementations. Imports `shared/` and `store/` (interface).
- `executor/` — protocol-client helpers (gRPC + HTTP bridge clients). Imports `shared/` and generated code from `proto/v1/`.
- `scheduler/` — imports `node/`, `message/`, `queue/`, `storage/`, `shared/`, `store/` (interface, plus type-asserted capabilities for sweeps), `attributes/`. **Does not import `supervisor/` or `controlapi/`.**
- `supervisor/` — imports `node/`, `message/`, `queue/`, `storage/`, `shared/`, `store/` (interface and capability assertions), `attributes/`, `executor/`. **Does not import `scheduler/` or `controlapi/`.**
- `controlapi/` — imports `node/`, `message/`, `storage/`, `shared/`, `store/` (interface), `attributes/`. **Does not import `scheduler/` or `supervisor/`.**
- `shared/` — depends on nothing except stdlib.
- `migrations/` — embeds SQL files via `embed.FS`; imports `shared/` only.
- `config/` — library entry points (§6). Imports the subsystem it starts (scheduler, supervisor, controlapi), `store/`, `attributes/`, and `shared/`.
- `cli/` — operator-facing CLI library. Pure HTTP client over the control-api; no orchestration logic of its own. Imports `shared/`, `node/`, `canonical/`, and stdlib. **Allowed importers:** `cmd/rimsky-cli/` only. The compose subpackage (`cli/compose/`) implements the `rimsky-compose.yml` plan-and-apply orchestration; its only knowledge of rimsky internals is through the public HTTP surface. See `docs/history/2026-05-02-rimsky-cli-and-compose-design.md`.
- `cmd/*` — the only packages allowed to import everything needed to wire up a binary.

### 3.2 Why these rules matter

The scheduler and supervisor are independent processes. They communicate only through shared Postgres state (dispatch queue, node states, events, heartbeats). Forcing their packages to be unable to import each other prevents accidental coupling — a future change that has the scheduler directly calling supervisor code would fail to compile.

The control API has the same property: it never calls into scheduler or supervisor internals. It reads and writes shared state; the runtime subsystems observe those reads/writes through their own polling/query logic.

The `store/` package declares the universal 4-verb `Store` interface; reference implementations live as standalone binaries under `stores/<kind>/` (`stores/filesystem/`, `stores/postgres/`, `stores/stub/`). Stores are dialed at process startup from the `stores:` block in `rimsky.yml` over gRPC; the per-process `Registry` is a simple name → `Store` map populated at `main()` from the resolved remote clients. There is no shared `rimsky_stores` table and no in-process Factory pattern — each process's registry is independent runtime state.

The `attributes/` package owns all `{{...}}` substitution and JSON Schema validation for per-run node attributes. Executors never substitute; userdata is opaque to rimsky (blessed invariant 11). Validation gates run twice — at dispatch (after substitution) and at commit (after executor writeback).

---

## 4. Three long-running processes

Rimsky ships as three independent long-running processes, each shipped as a standalone Go binary and a Docker image.

### 4.1 Scheduler

Binary: `cmd/rimsky-scheduler/`. Docker: `rimsky-scheduler`.

Responsibilities per tick (default interval 1.5s):

1. **Advisory-lock guard.** `pg_try_advisory_lock(SCHEDULER_TICK_KEY)`. If another replica holds it, skip this tick. This is the multi-replica double-tick safety invariant (§5.7).
2. **Schedule firing.** For each node whose cron indicates a fire is due: emit `invalidate`; compute and write next `next_fire_at`; log `schedule_fired`.
3. **Pure-cascade sweep.** For each `stale` node with no `executor` and all dependencies `fresh`: transition `stale → fresh` inline; emit `recalculate` to dependents; log `pure_cascade_commit`.
4. **Stale-heartbeat sweep.** For each `running` node whose `last_heartbeat_at` < `now - heartbeat_timeout`: log `heartbeat_lost`; clear supervisor assignment; transition `running → stale`; re-enqueue.
5. **Orphaned-claim sweep.** For each dispatch row with `claimed_by IS NOT NULL`, `claimed_at < now - orphaned_claim_timeout`, and node still `stale`: release the claim (claimant-guarded).
6. **Ready sweep.** For each `stale` executor node with all dependencies `fresh` and no pending dispatch row: enqueue a dispatch row.
7. **Frame engine tick.** `frame.RunTick` runs under the same advisory lock and drives queued→running promotion, frame-end detection, and stuck-frame reaping for the per-instance frame queue. See §4.1.1.

Code location: `core/scheduler/scheduler.go` is the tick loop; `core/scheduler/schedule_ticker.go` handles cron firing; `core/scheduler/pure_cascade.go` handles the inline sweep; `core/scheduler/recalculate.go` and `core/scheduler/invalidate.go` handle message semantics; `core/frame/engine.go` (called from the same tick) handles the frame-engine work — see §4.1.1.

#### 4.1.1 Frame engine

The frame engine implements the per-instance resolution model defined in `docs/history/2026-04-26-frame-resolution-design.md`. It owns the `rimsky_frames` queue and the `frame_id` propagation that brackets every cascade.

- **Producer** (`core/frame/producer.go::EnqueueOrCoalesce`) is called by every source of an invalidation event: `core/scheduler/schedule_ticker.go` (cron-driven schedule firing), `core/controlapi/nodes.go` (operator-originated invalidate), and the admin force-fire route. It inserts a `queued` row in `serial_queue` mode or upserts in `coalesce` mode, keyed by instance.
- **Engine** (`core/frame/engine.go::RunTick`) is invoked by the scheduler tick (`core/scheduler/scheduler.go::tick` calls `frame.RunTick`) inside the existing `pg_try_advisory_lock(SCHEDULER_TICK_KEY)` so the engine is single-flight across replicas — there is no separate frame advisory lock and no separate frame goroutine. RunTick handles three things per tick:
  1. **Frame-end detection.** Closes any `running` frame whose nodes have all reached a terminal state (`fresh` for committed, `failed` for given-up).
  2. **Queued→running promotion.** Picks the next `queued` frame for any instance with no `running` frame and stamps `frame_id` on the source nodes' dispatch rows, atomically with the state transition.
  3. **Stuck-frame reaper.** Closes frames whose `frame_timeout_ms` has elapsed in `running`; same predicate the spec calls out in §7.
- **Schema.** `rimsky_frames` carries the queue + state machine; `rimsky_dispatch.frame_id` (NOT NULL) brackets every dispatched run; `rimsky_nodes.frame_id` is non-null for `stale` / `running` nodes inside an active frame and cleared on a successful return to `fresh` (the centralised clear lives in `core/persistence/postgres/nodes.go::enforceAndUpdate`). `rimsky_lock_holders.frame_id` and `rimsky_claim_holders.frame_id` are observability-only and forward-compat for the post-v1 Rule 3b parallel-buffered enhancement.
- **Cross-references.** Conceptual model: `docs/node-graph-design.md` "Frames as the unit of resolution." Authoritative behavior: `docs/history/2026-04-26-frame-resolution-design.md`.

Frames are per-instance. Mode is per-template (`coalesce` | `serial_queue`). Under both modes at most one `rimsky_frames` row is in `running` state per instance, enforced by the partial unique index `uq_rimsky_frames_running`.

### 4.2 Supervisor

Binary: `cmd/rimsky-supervisor/`. Docker: `rimsky-supervisor`.

Responsibilities:

1. **Register.** On startup, upsert into `rimsky_supervisors` with `id`, accepted-executor list, concurrency limit, callback host/port (if hosting callbacks for async handoff).
2. **Heartbeat tick.** Every `heartbeat_interval_ms`, update `last_heartbeat_at`, active-node count, and each active node's `last_heartbeat_at`. (Operator-originated invalidates no longer preempt — see frame-resolution spec at `docs/history/2026-04-26-frame-resolution-design.md`. The kill-poll path was removed when `rimsky_nodes.kill_requested` was dropped.)
3. **Claim.** While active < concurrency, query the dispatch queue for a claimable row matching this supervisor's accept list and respecting concurrency-tag limits. Claim via `SELECT ... FOR UPDATE SKIP LOCKED`.
4. **Verify.** Re-read `claimed_by` on the dispatch row IMMEDIATELY before any expensive work. If the row has been released or re-claimed, log `orphaned_claim_lost_race` and bail. Hard backstop against double-execute.
5. **Dispatch.** Resolve `node.executor` → endpoint + transport from static config. Construct `ExecuteRequest`. Call `Execute` on the executor client (gRPC or HTTP bridge, per config).
6. **Handle response.** Terminal events map to commit (Complete), error routing (Blocked/Errored), or async-hold (AsyncAccepted).
7. **Complete.** Delete dispatch row (claimant-guarded).

Code locations: `core/supervisor/supervisor.go` is the top-level loop; `core/supervisor/runner.go` is the omnibus runner — per-dispatch execution including the verify-before-run step, attribute substitution, executor dispatch, and the heartbeat loop; `core/supervisor/runner_acquire.go` runs the v3 §7.3 atomic acquisition transaction (lock-holder inserts + `Store.Open` RPCs in the rimsky-side tx; the store's own state mutation runs in the store-service's own tx — blessed invariant 15); `core/supervisor/runner_terminal.go` drives per-claim release at the acquirer's terminal; `core/supervisor/auto_terminal.go` implements the auto-terminal mechanism per v3 spec §4.10 invariant 13 (subgraph-complete check, aggregate-outcome routing, store verb call + claimant-guarded lock-holder deletion); `core/supervisor/callback.go` handles async-handoff callbacks; `core/supervisor/on_error.go` dispatches through the policy chain; `core/supervisor/terminal_outcome.go` maps protocol terminal events to per-claim verb intentions consumed by `runner_terminal` and `auto_terminal`.

### 4.3 Control API

Binary: `cmd/rimsky-control-api/`. Docker: `rimsky-control-api`.

HTTP+JSON endpoints:

- `POST /templates`, `GET /templates`, `GET /templates/:id`, `DELETE /templates/:id`.
- `POST /instances`, `GET /instances`, `GET /instances/:id_or_key`, `DELETE /instances/:id_or_key`.
- `GET /nodes/:id`, `GET /instances/:id_or_key/nodes`.
- `POST /nodes/:id/invalidate`, `POST /nodes/:id/reset`, `POST /nodes/:id/kill`.
- `GET /events` (paginated, filterable).
- `GET /lock-holders/:lock_holder_id/claim-holders` — debug visibility into held-claim bookkeeping (rows in `rimsky_claim_holders` keyed by FK to a lock-holder row).
- `POST /admin/stores/:name/pick-policies/:selector/items` — admin-only enqueue endpoint (gated by `X-Rimsky-Admin-Token`); writes a row into the operator-owned items table backing the named pick policy on the named postgres store.
- `POST /admin/scheduled-nodes/:node_id/force-fire` — admin-only; sets `rimsky_schedules.next_fire_at = now()` so the next scheduler tick fires the schedule. Used by the smoke test fixture.
- `GET /health`, `GET /metrics` (Prometheus).

Auth is pluggable: `Authenticator` interface. Default: none; reference binary binds to localhost by default. Enterprise deployments provide their own auth module.

Code locations: `core/controlapi/app.go` is the route wiring; individual route handlers are in `templates.go`, `instances.go`, `nodes.go`, `events.go`, `claims.go`, `admin_force_fire.go`, `health.go`. Auth in `auth.go`. Redaction in `redact.go`.

### 4.4 Supporting binaries

- `cmd/rimsky-migrate/` — runs pending migrations. Session-level advisory lock held for the duration of the batch.
- `cmd/rimsky-conformance/` — the protocol-conformance test suite (Go binary). Run against any executor endpoint to validate conformance.
- `cmd/rimsky-conformance-probe/` — the stub-mode probe issued by `rimsky-conformance` at startup when `--require-stub-mode` is set.

---

## 5. Blessed invariants in source

Each invariant is annotated `@blessed-invariant` in the Go source and exercised by a scenario test. The full list (with rationale and revision history) lives in `docs/history/2026-04-27-stores-redesign-v3-design.md` §4.10 (v2 §21 retains the historical record for invariants 14 and 15 that v3 retired/revised). Pointers:

### 5.1 State machine rejects illegal transitions

- **File:** `core/node/state.go`
- **Context:** `NextState(current, requested, reason)` returns an error if the transition is illegal. In particular, `running → running` under reason `dispatch_claimed` is not silently idempotent.
- **Persistence layer enforcement:** `core/persistence/postgres/nodes.go` — the `UpdateState` method calls into the state machine; no short-circuit on `from == to`.
- **Scenario test:** `state-machine-same-state-rejected` (in `test/scenarios/`).

### 5.2 Dispatch claim brackets run

- **File:** `core/persistence/postgres/queue.go`
- **Context:** counts come from `rimsky_dispatch.claimed_by IS NOT NULL`. The claim window exactly brackets the node's `running` window. The invariant is annotated at the top of the claim query.
- **Interface contract:** `core/persistence/queue.go` documents the invariant at the interface level so any alternate implementation must respect it.

### 5.3 Multi-lock acquisition uses deterministic sorted order

- **File:** `core/supervisor/runner.go` (the runner orchestrates atomic acquisition; the queue no longer holds tag-limit logic)
- **Context:** when a node requires multiple locks (named, region/claim), acquisition sorts the specs in the v3 spec §4.10 invariant 3 deterministic order — `(lock_kind, lock_name | (store_name, region_data))` — before claiming. The `region_data` bytes are themselves the canonical form (stores canonicalize at `Open` per §7.7), so no separate canonicalization step is needed. Prevents deadlock between concurrent claims sharing a lock subset.

### 5.4 Claimant-guarded release

- **Files:** `core/persistence/postgres/queue.go`, `core/supervisor/runner.go`, `core/scheduler/scheduler.go`
- **Context:** every `DELETE FROM rimsky_lock_holders` and every `UPDATE rimsky_dispatch SET claimed_by = NULL` carries `AND … = supervisor_id`. `ReleaseClaim(dispatchID, expectedClaimedBy)` and `ReleaseLock(...)` are no-ops on mismatch. Prevents stale orphan sweeps from nulling live claims or releasing live locks.

### 5.5 Verify-before-run

- **File:** `core/supervisor/runner.go`
- **Context:** the runner re-reads `claimed_by` on the dispatch row immediately after claim and before calling the executor. If ownership has moved, logs `orphaned_claim_lost_race` and bails.
- **Scenario test:** `verify-before-run-race` — orchestrates the race explicitly.

### 5.6 Generous orphan-claim cutoff

- **File:** `core/scheduler/scheduler.go`
- **Context:** the orphan-claim sweep uses `5 × heartbeat_interval` as its default cutoff for both `rimsky_dispatch` and `rimsky_lock_holders`. Configurable, but the default is the safety-critical value.

### 5.7 Advisory lock on scheduler tick

- **File:** `core/scheduler/scheduler.go`
- **Context:** `pg_try_advisory_lock(SCHEDULER_TICK_KEY)` at the top of each tick. If another replica holds it, skip this tick. Prevents multi-replica double-ticks.

### 5.8 Session advisory lock on migrations

- **File:** `core/persistence/migrations.go`
- **Context:** the migration runner acquires a session-level `pg_advisory_lock` for the duration of a migration batch. Released at session close; prevents concurrent-runner migration-table corruption.

### 5.9a Lock state lives only in postgres

- **File:** `core/store/interface.go` (annotated on the `Store` interface comment)
- **Context:** no store implementation persists lock state. Stores may persist *data* state — e.g. `stores/postgres/` flips an items-table row to `'in_progress'` at `Open` for pick-policy claims — but the question "is anyone holding lock X" is answered exclusively by `rimsky_lock_holders`.

### 5.9b Store implementations do not internally serialize on lock-shaped predicates

- **File:** `core/store/interface.go` (annotated on the `Store` interface comment)
- **Context:** the §9-strategy-2 reader-lease serialization pattern (the store tracks active read leases; writers block at the store boundary) is forbidden as a way to fake `staged_async`. Honest support requires snapshot delegation (the store creates a stable view materialized at `Open(read)`) or native MVCC pass-through (the store opens a snapshot transaction at `Open(read)`, ends it at `Release`). A store that cannot honestly provide stable reads during writes declares `staged_blocking` (or `direct`) and lets the scheduler do the serialization.

### 5.10 Lock acquisition is atomic with dispatch claim

- **Files:** `core/supervisor/runner_acquire.go` (acquisition function), `core/persistence/postgres/queue.go` (dispatch SQL)
- **Context:** per v3 spec §4.10 invariant 10 + §7.3, the rimsky-side acquisition transaction either claims dispatch AND inserts all required `rimsky_lock_holders` rows AND records the `Store.Open`-returned address AND inserts any required `rimsky_claim_holders` rows for held claims, or none of them. The store's own state mutation runs in its own transaction (decoupled from rimsky's per §7.3); store atomicity is the store's concern. Single-writer-per-region (invariant 4b) holds because the rimsky-side conflict predicate gates lock-holder INSERTs against `rimsky_lock_holders` only — store orphan state is invisible to the predicate.

### 5.11 Userdata is opaque to rimsky

- **Files:** `core/attributes/substitution.go`, `proto/v1/node_executor.proto` (`ExecuteRequest.userdata` comment)
- **Context:** no code path inspects, parses, substitutes, or validates `userdata`. `{{...}}` substitution syntax inside userdata is treated as literal bytes; if an executor wants to interpret it, that's the executor's choice.

### 5.12 Attributes validate twice

- **File:** `core/attributes/validate.go`
- **Context:** validation runs at dispatch (after source-directive substitution; every required source must resolve) and at commit (after executor writeback merges; the populated object must validate against the schema). Both gates mandatory; failure raises `template_resolution_failed` or `attributes_schema_failed` respectively.

### 5.13 Held-claim resolution is auto-terminal, single, and aggregate-outcome-driven (revised)

- **File:** `core/supervisor/auto_terminal.go`
- **Context:** at holding-subgraph completion (all `rimsky_claim_holders` rows for the lock-holder are in `'completed'` or `'failed'`), the supervisor fires exactly one store verb per held claim based on aggregate outcome — all-completed → `Commit`; any-failed → `Abandon`. The store decides what those mean for its own state per its own configuration (e.g. the postgres reference store-service's per-pick-policy `on_commit_default` / `on_give_up_default`). The store verb shares a single SQL transaction with the `rimsky_lock_holders` row deletion and the `rimsky_claim_holders` finalisation; cascade FK on `rimsky_claim_holders.lock_holder_id` cleans up the bookkeeping rows. **Replaces** the prior `claimstorepg/holders.go` first-delete-wins / last-released-wins algorithm; no per-terminal-leaf reconciliation, no `actual_action` column. Per the 2026-04-30 cleanup the rimsky-side surface carries no store-internal vocabulary (no `claim_resolutions`, no `policy_override`, no `Delete` wire verb).

### 5.14 `RegionsConflict` and `UnmarshalRegion` are pure (retired in v3)

- **Context:** these methods existed on the in-process v2 `Store` interface and were removed when v3 moved stores out-of-process. Region conflict in v3 is a byte-equality check at the rimsky-side conflict predicate (no per-store helpers); the invariant is no longer load-bearing. Retained as a numbered placeholder so downstream invariant numbering (notably invariant 20) stays stable.

### 5.15 `Open` fires inside the acquisition transaction (decoupled in v3)

- **File:** `core/supervisor/runner_acquire.go`
- **Context:** the rimsky-side acquisition transaction wraps the dispatch claim, the `rimsky_lock_holders` INSERTs, and the `address` UPDATE recording the store-returned bytes from `Store.Open`. The store's own state mutations run in the store-service's own transaction, decoupled from rimsky's tx (v3 moved stores out-of-process; the prior `store.WithTx` / `store.TxFromContext` shared-tx mechanism was removed). Atomicity within the store is the store's concern; rimsky's invariant is that no lock-holder row is recorded without the corresponding `Open` having returned successfully and its address persisted in the same rimsky-side tx.

### 5.20 Claim content (payload, address, region) is inert in Rimsky

- **Files:** `core/store/types.go` (on `ClaimResult`); `core/attributes/substitution.go::walkPath` (sanctioned introspection site)
- **Context:** Rimsky reads claim content by named-field path **only** at substitution-leaf extraction; does not log, validate, transform, normalize, decrypt, hash, index, pattern-match, attach to traces, include in errors, or otherwise act on claim content. `ClaimResult.Address` / `.Payload` / `.Region` are `json.RawMessage` to permanently narrow the surface for accidental pretty-printing via `slog.Any` or `%+v`. Distinct from store-config bytes (operator-managed; not under invariant 20 — see v3 spec §13.3).

---

## 6. Library entry points

Rimsky's runtime subsystems are library-first: the binaries in `cmd/` are thin wrappers around library calls in `core/config/`. Consumers who want to embed rimsky in their own process (custom wiring, custom auth, custom store registration) call the library entry points directly.

```go
package rimsky

// StartScheduler starts a scheduler process and returns a handle for
// lifecycle control (Shutdown, Health).
func StartScheduler(cfg SchedulerConfig) (SchedulerHandle, error)

// StartSupervisor starts a supervisor process. SupervisorID must be unique
// across the deployment; Resolver maps executor names to endpoints+transports;
// Stores is the per-process store registry built from the `stores:` block
// in rimsky.yml.
func StartSupervisor(cfg SupervisorConfig) (SupervisorHandle, error)

// StartControlAPI binds host:port (port=0 for OS-assigned) and starts serving.
func StartControlAPI(cfg ControlAPIConfig) (ControlAPIHandle, error)
```

Each handle exposes:

- `Shutdown(ctx context.Context) error` — graceful shutdown with context timeout.
- `Health() HealthReport` — current health status with subsystem details.

Configuration structs (per spec §6.1, the v3 stores cleanup, and the
2026-05-01 control-plane design):

- `SchedulerConfig` — Postgres URL, tick interval, heartbeat/orphan timeouts, advisory-lock key, `Stores RemoteStoresConfig` (one endpoint+capabilities entry per remote store-service), `NamedLocks store.NamedLocksConfig`, logger.
- `SupervisorConfig` — `SupervisorID`, Postgres URL, concurrency limit, heartbeat interval, claim poll interval, `Resolver` for executor endpoints, `Stores RemoteStoresConfig`, `NamedLocks store.NamedLocksConfig`, callback host/port, logger.
- `ControlAPIConfig` — Postgres URL, bind host/port, `Stores RemoteStoresConfig`, `NamedLocks store.NamedLocksConfig`, `Executors ExecutorsConfig` (declared executors validated at template registration), optional `Authenticator`, logger.

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

The end-state schema is defined under `core/persistence/<driver>/migrations/`
(Postgres: a `001-initial.sql` + per-feature additions; SQLite: a single
hand-written `001-initial.sql` capturing the union). Tables:

- `rimsky_migrations` — migration bookkeeping.
- `rimsky_templates` — stored templates with validated schema. The `spec` JSONB carries the post-cleanup template shape (`stores`, `locks`, `attributes`, `inherits`).
- `rimsky_instances` — template instantiations (`id`, `template_hash`, `instance_key`, `params`, `created_at`, `terminated_at`).
- `rimsky_nodes` — per-instance nodes (state, metadata, executor name, schedule ref). `concurrency_tags` removed; concurrency control now lives in template `locks` declarations enforced via `rimsky_lock_holders`.
- `rimsky_dispatch` — work queue; one row per outstanding dispatch. `executor_name` is nullable (native claim-only nodes); `required_stores TEXT[]` is denormalized at enqueue time for the pool-specialization predicate (each supervisor only claims dispatch rows whose required stores are a subset of its accepted-stores config); `last_heartbeat_at` drives the dispatch-claim sweep.
- `rimsky_schedules` — cron + next-fire time for scheduled nodes.
- `rimsky_supervisors` — supervisor registry with heartbeats, callback endpoints, plus `accepted_executors TEXT[]` and `accepted_stores TEXT[]` for pool eligibility.
- `rimsky_node_attributes` — per-node typed attributes object (single row per node). `data JSONB` populated from source-directive substitution at dispatch and executor writeback during run; validated against the template's JSON Schema. Replaces the old per-run `result` and resource-version writes.
- `rimsky_lock_holders` — single source of truth for the two lock primitives (`lock_kind IN ('named', 'region')`). For region claims carries `store_name`, `region_data` (the store's identifier — resolved selector text or pick-policy-chosen item identifier), `address` (store-supplied bytes from `Open`, needed by terminal verbs and the orphan reaper), and `intent` (`r` / `rw`). For named locks carries `lock_name`. Common fields: `holder_supervisor_id`, `holder_node_id`, `last_heartbeat_at`, `expires_at`. Inserted atomically with dispatch claim; orphan-reaped at `5 × heartbeat_interval`. (Stores never persist lock state — blessed invariant 9a.)
- `rimsky_claim_holders` — bookkeeping for held claims under auto-terminal resolution. One row per (lock-holder × holding-subgraph-member) inserted at the acquirer's `Open` inside the v3 §7.3 atomic acquisition transaction. Carries `lock_holder_id UUID NOT NULL REFERENCES rimsky_lock_holders(id) ON DELETE CASCADE`, `holder_node_id`, `state TEXT IN ('active', 'completed', 'failed')`, `completed_at`, `frame_id` (observability-only). Cascade-deletes when the parent lock-holder row is removed at auto-terminal.
- `rimsky_events` — append-only log; primary audit trail. New kinds added: `lock_acquired`, `lock_released`, `lock_orphan_reaped`, `attributes_substituted`, `attributes_committed`, `attributes_validation_failed`, `claim_acquired`, `claim_held`, `claim_resolved`, `template_resolution_failed`. Removed: `commit`, `pure_cascade_commit`.

`rimsky_resources` and `rimsky_resource_versions` are no longer present.

In addition, postgres-store **pick policies** that use an items-table pattern operate over an **operator-owned items table** owned by the postgres store-service: `item_id TEXT PRIMARY KEY`, `payload JSONB`, `state TEXT IN ('available', 'in_progress', 'completed')`, `claim_token TEXT`, `claimed_at TIMESTAMPTZ`, `enqueued_at TIMESTAMPTZ`, `priority INTEGER`, `sequence BIGSERIAL`. Under v3 the items table lives entirely in the postgres store-service's domain (rimsky has no SQL contact with it); the table schema is documented in `stores/postgres/store/`. Each pick-policy entry in the postgres store-service's own config names its own items table; operators create these tables out-of-band, and the store verifies them at startup (`stores/postgres/store/store.go::New`).

Code location: `core/persistence/postgres/` owns the postgres SQL — one file per table cluster (`templates.go`, `instances.go`, `nodes.go`, `lock_holders.go`, `claim_holders.go`, `node_attributes.go`, `events.go`, `schedules.go`, `supervisors.go`, `frames.go`, `queue.go`, plus `coordinator.go` and `backend.go` for the Driver/Store wiring). `core/persistence/store.go` and `core/persistence/queue.go` declare the cross-driver interfaces; the SQLite driver impl lives at `core/persistence/sqlite/`.

### 8.3 Migrations

Driver-agnostic runner: `core/persistence/migrations.go::Migrator` consults
the per-driver embedded SQL FS (`core/persistence/<driver>/migrations/`),
applies migrations in lexical order, and records each in `rimsky_migrations`.
The Postgres driver acquires a session-level advisory lock for the duration
of the batch; the SQLite driver holds an in-process mutex (single-process
is the only supported topology). All four cmd binaries can run migrations:
`rimsky-migrate` is dedicated, but the other three also call
`driver.Migrate(ctx, log)` in tests / on-demand.

The Postgres driver ships three migrations (`001-initial.sql`,
`002-frame-resolution.sql`, `003-template-registry-and-lifecycle.sql`).
The SQLite driver ships a single hand-written init file capturing the
union — migrations don't need to be replayed against a fresh SQLite DB.

v1 owns its own migration line. The first migration on each driver
defines the full Go v1 schema — it is not a diff against any predecessor.

---

## 9. Distribution model

### 9.1 Go module

One Go module: `github.com/fallguy/rimsky/core`. Published via standard Go module proxy at v1.0.0 when the OSS extraction completes. Consumers who want to embed rimsky (custom wiring, custom auth, custom-registered stores) import this module.

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
- Persistence layer methods (`core/persistence/postgres/...` and `core/persistence/sqlite/...`; cross-driver conformance tests in `core/persistence/conformance/`).

### 11.2 Integration tests (testcontainers-go + Postgres)

Every store interface method, dispatch-queue claim contention, orphan-claim sweep, migration runner idempotency and concurrent-run safety.

### 11.3 Scenario tests

`test/scenarios/` — full-stack end-to-end via the scenario harness (`core/scenario/harness.go`), bucketed by surface: top-level (happy-path / cascade scenarios), `stores/` (filesystem-direct write, disjoint vs. overlapping regions, pool specialization, single-writer-per-region under all `write_semantics` values), `locks/` (named-lock counts, region-lock conflict, atomic acquisition, heartbeat-extends-expiry, orphan reap, sorted acquisition no-deadlock, claimant-guarded release), `attributes/` (substitution from deps/claim/params, schema validation gates, incremental and terminal-final writeback, userdata opacity, value-pass vs. claim-pass lifetime), `claim_stores/` (pick-policy selectors, on-commit/on-give-up actions, auto-terminal aggregate-outcome under linear chains and fan-out, multi-claim), and `frame_resolution/` (the per-instance frame engine). Versioned-mode scenarios are permanently eliminated; sidecar mode is post-v1.

All scenarios use `SystemClock` against testcontainers Postgres (real `NOW()` governs claim eligibility).

### 11.4 Protocol conformance

`cmd/rimsky-conformance/` validates any executor endpoint against the protocol. Shipped as a Docker image for executor authors to run in CI. See `protocol.md` for contract details. The stub-mode requirement keeps conformance deterministic for LLM-calling executors.

---

## 12. Go library choices

- **Postgres:** `jackc/pgx/v5`.
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

## Control-plane v1 + store lifecycle protocol

Per `docs/history/2026-05-01-control-plane-and-store-lifecycle-design.md`.

**New packages:**

- `core/canonical/` — RFC 8785 JCS canonicalization wrapper + `CanonicalSpecHash`.
- `core/controlapi/lifecycle.go` — fan-out helper for the six lifecycle events.
- `core/controlapi/tags.go` — tag CRUD HTTP handlers.
- `core/controlapi/instance_terminator.go` — background worker that fires
  `OnInstanceTerminated`.

**New tables:**

- `rimsky_template_tags` — movable tag → content hash aliases.
- `rimsky_store_lifecycle` — per-(store, scope) bookkeeping for lifecycle
  fan-out idempotency.

**Schema changes:**

- `rimsky_templates.id` is now TEXT (content hash) instead of UUID; gains
  `state`, `registered_at`, `source` columns.
- `rimsky_instances.template_id UUID` becomes `template_hash TEXT`;
  `consumer_key` becomes `instance_key` (nullable); `terminated_at` added.

**Protocol changes:**

- Six new RPCs on `StoreService` (lifecycle events) — see
  `proto/v1/store_service.proto`.
- Two new fields on `OpenRequest`: `template_id`, `instance_id`.

**Config changes:**

- `RIMSKY_STORES_CONFIG` → `RIMSKY_CONFIG`. `rimsky.yml` is the unified
  deployment-shape config (stores + named_locks + executors).
- The supervisor's per-process YAML loses its `executors:` block.
