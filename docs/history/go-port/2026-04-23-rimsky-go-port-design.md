# Rimsky — Go Port Design

Spec for the Go rewrite of rimsky, the project-agnostic reactive node-graph orchestration platform currently implemented in TypeScript under `/rimsky/`. This spec supersedes `docs/specs/2026-04-21-rimsky-v1-design.md` as the target for the public v1 release. The existing TypeScript implementation is treated as an executable sketch: its architecture carries forward; its code does not.

Conceptual reference: the Go port ships its conceptual doc at `rimsky-go/docs/node-graph-design.md`, derived from the TS project's `docs/cell-graph-design.md`. The TS conceptual doc stays in place unchanged (it documents the TS project, which is not being published); the Go doc is rewritten with node terminology and the three-collections architecture (§18 lists the substantive changes).

**Terminology shift:** the Go port uses **"node"** where the TS project used **"cell"**. The conceptual model is identical; only the word changed. See §1.5.

---

## 1. Purpose and framing

### 1.1 What rimsky is

Rimsky is a node-graph orchestrator. Nodes communicate via two messages (`invalidate`, `recalculate`), operate on versioned resources, and execute work through external executors. The node graph is the primitive; resources and executors are pluggable extensions nodes can use, alone or in combination.

A graph of pure-cascade nodes with schedules is already useful — it's a declarative scheduler. Adding resources gives data semantics (versioning, double-buffering, rollback, quality rules). Adding executors gives work semantics (deterministic jobs, agentic reasoning, anything a protocol-speaking service can do). Most real graphs use all three.

### 1.2 Why Go

The TypeScript implementation is feature-complete and well-tested (205+ tests). It proved the model. It is not the right long-term vehicle for an open-source enterprise-infrastructure platform, because the serious cloud-native infrastructure category is overwhelmingly Go, secondarily Rust, and essentially absent in TypeScript. The language-landscape survey (conversation of 2026-04-22) is the dispositive evidence; the credibility cost of shipping the public v1 in TS is real and not addressable by engineering work.

The rewrite is the moment to also restructure around architectural clarity — a three-collection separation (orchestrator, resources, executors) that was latent in the TS code but not named or enforced.

### 1.3 Contract fidelity to TS v1

The Go port keeps **core-model fidelity** — the state machine, message semantics, policy-chain evaluator, dispatch-queue invariants, event-kind taxonomy, and migration schema carry forward wire-compatible where they already exist. Changes are **additive** (new capabilities: the node-executor protocol, cross-language executors, optional `executor` field on nodes) and **Go-idiomatic** (package layout, library choices, concurrency primitives).

The Go version is not a transliteration. It is v1.

### 1.4 Open-source positioning

Rimsky will be extracted into its own public repository at v1 ship. Until then, development continues in-tree alongside the original TypeScript prototype at `/rimsky/` and the new Go port at `/rimsky-go/`. Rimsky is an independent project; its specs and plans intentionally avoid naming specific consumers. Consumer-facing migration work, template authoring, and cutover planning live in the consumer's own documentation, not in rimsky's.

### 1.5 Terminology change: cell → node

The TS project used **cell** throughout: cells, cell graph, cell state, cell template. The Go port uses **node** instead. Reasoning:

- State left the node: resources own versioning, rollback, and data. The "stateful self-contained cell" metaphor no longer applies — nodes are graph-vertex declarations that reference executors and resources.
- Execution left the node: executors run the work as peer services. A node holds no execution logic.
- Native vocabulary: in conversation, the designers and reviewers consistently drifted to "node" and had to correct back to "cell." Vocabulary that requires discipline to sustain is the wrong vocabulary.
- Landscape alignment: Airflow, Temporal, Argo, Dagster, and LangGraph all use "node," "task," "step," or "activity." "Cell" required explanation in every user-facing doc; "node" does not.

The rename is mechanical (identifiers, table names, proto messages, docs). Messages (`invalidate`, `recalculate`), resources, executors, the event log, the state machine, and every semantic all stay the same. The spec uses "node" throughout from this section onward. TS v1 code references (e.g. `rimsky/src/cell/state-machine.ts`) keep "cell" because they point at the unchanged TS source tree.

---

## 2. Scope

### 2.1 In scope for Go v1

**Orchestrator core:**
- Node contract (state machine, transitions, transition reasons).
- Messages: `invalidate`, `recalculate`.
- Per-node error taxonomy with ordered policy chains. Actions: `retry` (with backoff/jitter), `invalidate(targets)`, `give_up`.
- Resources with `ResourceId`, versioning, double-buffering, `changed: bool` commit verdict, `no-op commit` semantics, quality rules with severity (error | warning).
- Dispatch queue interface; Postgres-table default implementation.
- Scheduler process (polling loop).
- Supervisor process (claim dispatch rows; call executors; interpret responses; heartbeat).
- Control API (HTTP + JSON) for templates, instances, nodes, events, resources, health, operator overrides.
- Postgres-backed storage.
- Schedule property on any node (replaces timer nodes as a distinct kind).
- Concurrency tags with per-tag advisory-lock ordering.
- Structured logging (Go `slog`); Prometheus `/metrics` endpoint; `/health`.
- Library entry points (`StartScheduler`, `StartSupervisor`, `StartControlAPI`) plus reference env-var binaries.
- Scenario-driven integration tests with testcontainers-go plus unit tests for pure logic.

**Protocol:**
- `proto/v1/node_executor.proto` — the node-executor contract (gRPC canonical + HTTP+JSON bridge).
- `proto/v1/events.proto` — event-kind shapes for consumers who want type-checked event payloads.

**Reference resources** (Go, implemented inside the orchestrator process as `Resource`-interface implementations):
- `inline-jsonb` — data stored directly in `rimsky_resource_versions.data`.
- `external-sql` — data written to a consumer-owned SQL table via a declared access method.

**Reference executors** (each its own service, each its own Docker image):
- `http-node` (Go) — generic "POST userdata to URL, commit response."
- `claude-agent` (TypeScript, published as npm package and Docker image) — spawns Claude CLI, hosts its own internal MCP callback for the agent, reports result back to rimsky via the node-executor protocol.

**Deploy artifacts:**
- Per-component Dockerfiles.
- Docker Compose reference deployment.
- Helm chart / Kubernetes manifests.
- Published Docker images.
- Published Go module (orchestrator).
- Published npm package (claude-agent executor).

**Testing:**
- Unit tests (state machine, policy evaluator, template validator, backoff math, cron math).
- Integration tests (testcontainers-go + Postgres).
- Scenario tests (full-stack end-to-end with fake executor).
- **Protocol conformance suite** — a runnable tool executor authors use to self-verify their implementation.

**Documentation:**
- Architecture overview (refresh of `node-graph-design.md`).
- Protocol reference (generated from `.proto` + handwritten narrative).
- Operator guide.
- Executor author guide.
- Resource author guide.

### 2.2 Explicitly deferred (post-v1)

- `s3-object` resource (bytes in S3/MinIO, manifest in Postgres).
- `sql-node` reference executor.
- `langgraph-node` reference executor (Python).
- gRPC surface for the control API (HTTP+JSON only in v1).
- Template update endpoint (deploy + remove only).
- Freshness policies (`warn_after` / `fail_after`).
- Observer nodes, external-trigger nodes.
- Priority on nodes/messages.
- OpenTelemetry traces.
- Web UI.
- Any form of content-hash-based staleness.
- pypi package publishing.

### 2.3 Non-goals

- Rimsky is not a workflow DSL for LLM applications (LangGraph occupies that niche).
- Rimsky is not multi-tenant SaaS; one deployment = one organization.
- Rimsky is not ETL like Airflow or Dagster. It is a reactive state machine for long-running pipelines.
- Rimsky does not own consumer data tables.

---

## 3. Architecture: three collections

### 3.1 The three collections

The Go rimsky separates what the TS version glued together. Each collection is architecturally distinct; for v1 they ship in one repository, versioned and documented together.

**Orchestrator** — the node-graph runtime. State machine, scheduler, supervisor, control API, dispatch queue, Postgres storage, migrations. Knows nothing about LLMs. Knows resources and executors only through their interfaces / protocol.

**Resource library** — implementations of the `Resource` interface. Each implementation decides how to store versions, how to roll back, how to evaluate quality rules. v1 ships `inline-jsonb` and `external-sql` as Go implementations running inside the orchestrator's process.

**Executor library** — reference executor services that speak the node-executor protocol. v1 ships `http-node` (Go) and `claude-agent` (TypeScript). Executors run as peer services; the orchestrator calls them over the wire.

### 3.2 Boundaries

**Rules of separation (enforced by import graph):**

- Orchestrator imports only orchestrator, `proto/v1/*`, and stdlib/external libs.
- Resource implementations import only the resource-library interface package, `proto/v1/*`, and stdlib/external libs. Not orchestrator internals.
- Executor implementations (the Go ones) import only `proto/v1/*` and stdlib/external libs. Not orchestrator, not resources.
- The orchestrator consumes resources through the `Resource` Go interface, registered at process startup by the deployer's `main()`. Swapping implementations does not require modifying orchestrator code.
- The orchestrator consumes executors through the node-executor protocol only. Swapping executors is a config change.

**Single Go module:** the module is `github.com/fallguy/rimsky`, rooted at the repo and holding all Go-importable code under `core/`. The path ends at the repo name (not `/core`) so imports read `github.com/fallguy/rimsky/core/shared`, not `.../core/core/shared`. The structural intent of spec §3.1 ("only `core/` is Go-importable") is preserved — all importable packages live under `core/`; sibling directories (`executors/`, `resources/`, `proto/`) are peer services or build targets, not Go libraries.

**Polyglot executors:** the TypeScript `claude-agent` executor is not Go-shaped. It publishes to npm and ships a Docker image.

### 3.3 What this changes vs. TS v1

The agentic subsystem collapses. The TS v1's `callback-mcp/`, `supervisor/agentic-runner.ts`, `supervisor/cli-runner.ts`, the Claude-CLI spawn logic, the silence-detection loop, the token-registry, the internal MCP server — all of that **moves out of the orchestrator** and into the `claude-agent` executor. The orchestrator supervisor becomes "claim dispatch row, call executor over protocol, interpret response, persist outcome." Approximately 1,000 lines of orchestrator code disappear.

Resource versioning, quality-rule evaluation, rollback semantics — these move into the resource library. The orchestrator's supervisor no longer runs quality rules; it asks the resource "commit this result," and the resource's implementation runs its own rules and returns accept-or-reject.

Timer nodes cease to exist as a distinct kind. `schedule` becomes a property on any node (§5.2).

Nodes become optional-executor (§5.1). A node with no executor is a pure-cascade node, handled inline by the scheduler.

---

## 4. Repository layout

```
rimsky/                              # repo root
├── core/                            # the single Go module
├── go.mod                           # module github.com/fallguy/rimsky (at repo root; core/ holds the importable tree)
│   ├── node/                        # state machine, policy evaluator, template types
│   ├── message/                     # invalidate / recalculate shapes and helpers
│   ├── queue/                       # DispatchQueue interface + Postgres impl
│   ├── scheduler/                   # tick loop, schedule firing, orphan sweep, ready sweep
│   ├── supervisor/                  # claim, executor client, outcome handling, heartbeat
│   ├── controlapi/                  # HTTP+JSON routes, schemas, error mapping, auth hook
│   ├── storage/                     # store interfaces + Postgres impls (all state tables)
│   ├── migrations/                  # numbered SQL files + runner
│   ├── resource/                    # Resource + QualityRule INTERFACES (not impls)
│   ├── executor/                    # protocol-client helpers (gRPC / HTTP bridge clients)
│   ├── shared/                      # cross-package types, errors, clock, logger, config
│   └── cmd/                         # reference binaries, also used as Docker entrypoints
│       ├── rimsky-scheduler/
│       ├── rimsky-supervisor/
│       └── rimsky-control-api/
├── proto/                           # protocol source of truth
│   └── v1/
│       ├── node_executor.proto
│       └── events.proto
├── resources/                       # reference resource implementations (Go, in-process)
│   ├── inlinejsonb/
│   └── externalsql/
├── executors/                       # reference executors (peer services)
│   ├── http-node/                   # Go; its own `package main`; Docker image
│   │   └── cmd/rimsky-exec-http/
│   └── claude-agent/                # TypeScript; npm package; Docker image
│       ├── package.json
│       └── src/
├── conformance/                     # protocol conformance test suite (Go binary)
├── deploy/
│   ├── docker-compose.yml           # "clone and run" reference
│   ├── kubernetes/                  # Helm chart + manifests
│   └── Dockerfile.*                 # per-component Dockerfiles
├── docs/
│   ├── node-graph-design.md          # conceptual reference (derived from TS cell-graph-design.md)
│   ├── architecture.md
│   ├── protocol.md
│   ├── operator-guide.md
│   ├── executor-author-guide.md
│   └── resource-author-guide.md
└── examples/
```

Consumer-specific migration guides (for teams porting existing work onto rimsky) are the consumer's concern; they live in the consumer's own documentation, not rimsky's.

During monorepo incubation, the layout above lives under a `rimsky-go/` prefix. On extraction to a standalone repo, the prefix drops and this becomes the repo root layout.

### 4.1 Packages within `core/` (cross-package import rules)

- `node/` — pure logic; imports `shared/` only.
- `message/` — types; imports `shared/` only.
- `queue/` — interface + Postgres impl; imports `shared/` and stdlib `database/sql`/`pgx`.
- `storage/` — interfaces + Postgres impls; imports `shared/` and `pgx`.
- `scheduler/` — imports `node/`, `message/`, `queue/`, `storage/`, `shared/`, `resource/` (interface only). Does not import `supervisor/` or `controlapi/`.
- `supervisor/` — imports `node/`, `message/`, `queue/`, `storage/`, `shared/`, `resource/` (interface only), `executor/` (protocol client). Does not import `scheduler/` or `controlapi/`.
- `controlapi/` — imports `node/`, `message/`, `storage/`, `shared/`, `resource/` (interface only). Does not import `scheduler/` or `supervisor/`.
- `resource/` — interface declarations only; imports `shared/` only.
- `executor/` — protocol-client helpers; imports `shared/` and generated code from `proto/v1/`.
- `shared/` — depends on nothing except stdlib.
- `migrations/` — embeds SQL files via `embed.FS`; imports `shared/` only.
- `cmd/*` — the only packages that import everything they need to wire together.

A circular-import check in CI (`go vet` plus a custom lint) enforces these rules.

---

## 5. Node model

### 5.1 Node shape

A node has any combination of:

- `type` (required) — node type identifier within the template.
- `description` (optional).
- `executor` (optional) — named executor this node dispatches to. Absent = pure-cascade node.
- `userdata` (optional) — opaque JSON blob, passed through to the executor on `Execute`. Rimsky does not interpret it.
- `schedule` (optional) — cron expression (UTC). When the schedule fires, the scheduler emits `invalidate` to the node.
- `dependencies` (optional) — list of sibling node type names.
- `concurrency_tags` (optional) — tags the scheduler uses for per-tag limits at dispatch time.
- `owns_resources` (optional) — list of resources this node writes.
- `reads_resources` (optional) — resources the node reads outside its dependency chain.
- `error_types` (optional) — per-node error taxonomy with policy chains.

There is no `kind` field. The TS v1's `kind: deterministic | agentic | timer` discriminator is gone.

### 5.2 Node shapes in practice

- **Executor node.** Has `executor` and probably `userdata`. Runs when dispatched; commits results (if it owns resources) or returns work-complete (if not).
- **Pure-cascade node.** No `executor`. When invalidated and dependencies are fresh, the scheduler instantly transitions `stale → fresh` and emits `recalculate` to dependents. Commit verdict is always `changed: true`. Cannot own resources (validated at template deploy).
- **Scheduled node.** Any node with a `schedule`. The scheduler emits `invalidate` to it when the cron fires. Otherwise indistinguishable from any other node — scheduled executor nodes, scheduled pure-cascade nodes, and scheduled nodes with dependencies (re-run periodically and on upstream change) all compose naturally.
- **Fan-out.** A pure-cascade node with a `schedule`, no dependencies, and many downstream dependents. Replaces the TS v1 timer-node pattern.

### 5.3 State machine

States: `fresh`, `stale`, `running`, `failed`. Transitions:

| From → To | Trigger | Reason kind |
|---|---|---|
| `fresh` → `stale` | `invalidate` message received | `invalidate_received` / `operator_invalidate` |
| `stale` → `running` | Supervisor claims dispatch row (executor nodes only) | `dispatch_claimed` |
| `stale` → `fresh` | Scheduler inline transition (pure-cascade nodes with deps fresh) | `pure_cascade` |
| `stale` → `failed` | Supervisor determines dispatch is impossible (executor name unresolvable) | `dispatch_impossible` |
| `running` → `fresh` | Executor returned success (possibly `changed: false`) | `work_completed` |
| `running` → `stale` | Error policy action `retry` or `invalidate(targets)` | `policy_retry` / `policy_invalidate` |
| `running` → `stale` | Infrastructure re-enqueue (heartbeat loss, stream error, executor dial failure) | `heartbeat_lost` / `infra_reenqueue` |
| `running` → `failed` | Error policy action `give_up` | `policy_give_up` |
| `failed` → `stale` | Operator `reset` or `invalidate` | `operator_reset` / `operator_invalidate` |
| any → `fresh` | `invalidate(restore_version)` with resource-supported rollback | `restore_version` |

All state changes write a `state_transition` event. The state machine rejects illegal transitions.

**No same-state short-circuit.** The `nextState(current, reason)` function and the `NodeStore.UpdateState` method MUST NOT return silently when the requested state equals the current state. Specifically, `running → running` under reason `dispatch_claimed` MUST throw — this is the load-bearing invariant against double-execute, where a slow supervisor that got as far as `UpdateState(id, "running", dispatch_claimed)` while another supervisor was already running the same node would otherwise silently succeed, bypassing the claim-ownership re-check. This invariant is `@blessed-invariant` (§17); any Go implementation that adds an idempotency optimization for "ergonomics" breaks it. The TS reference is `rimsky/src/cell/state-machine.ts:37-73` (no `from === to` branch in the switch; the TS project kept "cell" terminology).

### 5.4 Policy chain evaluation

Unchanged from TS v1 (§4.2 / §7.3 of `2026-04-21-rimsky-v1-design.md`). Per node × error class:

- `action_index` tracks position in the chain.
- `retry_counter` tracks attempts within a single `retry` action.
- Different class → reset counters.
- `retry`: if counter < count, increment + schedule backoff re-enqueue. If exhausted, advance `action_index`, reset counter, re-enter.
- `invalidate(targets)`: emit invalidates, stay stale, advance `action_index` for next same-class recurrence.
- `give_up`: transition to `failed`.
- Successful run resets all counters for the class.

### 5.5 Template schema (YAML)

```yaml
name: string
version: string
description: string

nodes:
  - type: string
    description: string
    executor: string                         # optional; named executor
    userdata: object                         # optional; opaque
    schedule: string                         # optional; cron (UTC)
    dependencies: [string]                   # sibling node types
    concurrency_tags: [string]
    owns_resources:
      - path: [string]                       # ResourceId segments
        implementation: string               # "inline-jsonb" | "external-sql" | ...
        config: object                       # impl-specific (e.g. SQL schema/table)
        retention:
          keep_versions: int
        quality_rules:
          - type: string                     # builtin or custom
            config: object
            severity: error | warning
    reads_resources:
      - path: [string]
        via: string
    error_types:
      <error_class>:
        policy:
          - action: retry
            count: int
            backoff: linear | exponential
            jitter: none | plus_minus
            base_delay_ms: int
            max_delay_ms: int
          - action: invalidate
            targets: [string]
            restore_version: previous | null
          - action: give_up
            reason_template: string

params_schema: object                        # JSON Schema for instance.params
params_redact: [string]                      # top-level keys to redact in HTTP output
```

### 5.6 Template validation (at `POST /templates`)

- All `dependencies` reference declared nodes.
- All `error_types.<class>.policy[*].invalidate.targets` reference declared nodes.
- No dependency cycles.
- `schedule` (if present) is a valid cron expression.
- `executor` (if absent) and `owns_resources` (if non-empty) is an error: pure-cascade nodes cannot own resources.
- `userdata` on a node with no `executor` produces a warning, not an error.
- `owns_resources[*].implementation` references a registered resource implementation; `config` block validates against that implementation's declared schema (see §8.2).
- `owns_resources[*].path` placeholders (e.g. `{consumer_key}`) are resolvable at instantiation.

Templates failing validation are rejected at the API boundary; nothing is stored.

### 5.7 Node instantiation

`POST /instances` with `{template_id, consumer_key, params}`:

1. Validate `consumer_key` unique within `template_id`.
2. Validate `params` against `params_schema`.
3. Allocate instance UUID.
4. For each node: allocate node UUID; resolve `dependencies` to sibling node UUIDs; resolve placeholders (§5.8) across `concurrency_tags`, `owns_resources[*].path`, `owns_resources[*].config`, `reads_resources[*].path`; provision resources through their declared implementations (registry row + any implementation-specific setup like creating target SQL tables).
5. For nodes with `schedule`: compute `next_fire_at` from cron and current clock; write to schedule table.
6. Log `state_transition` events for all nodes (initial state `stale`).
7. Enqueue dispatch rows for root executor nodes (no dependencies, has `executor`).
8. For root pure-cascade nodes: scheduler will transition them inline on its next tick.

### 5.8 Placeholder resolution

The following placeholder forms are valid inside string fields of the template:

- `{instance_id}` — the rimsky-assigned instance UUID.
- `{consumer_key}` — the consumer-supplied key for this instance.
- `{params.<key>}` — top-level value from `instance.params` (values that pass through `params_redact` for control-API display still resolve normally at instantiation; placeholders read the unredacted params).

Scope of placeholder substitution:

- `concurrency_tags[*]` — substituted at instantiation.
- `owns_resources[*].path[*]` — substituted at instantiation. Rejected if any placeholder segment fails to resolve (no silent empty-string fallback).
- `owns_resources[*].config` — substituted recursively across string leaves at instantiation, before the resource implementation's `Factory.Create` is called.
- `reads_resources[*].path[*]` — same rules as `owns_resources`.
- `userdata` — NOT substituted by rimsky. The opaque-block promise means rimsky does not interpret or rewrite its contents. Executors may implement their own templating over `userdata` using data they receive in the `ExecuteRequest` (which includes `instance_params`).

Unresolved placeholders at instantiation return a 400 from `POST /instances` with the offending field path; nothing is committed.

---

## 6. Execution model

### 6.1 Scheduler loop

Per tick (default 1.5s):

1. **Advisory-lock guard.** Attempt `pg_try_advisory_lock(SCHEDULER_TICK_KEY)`. If another replica holds it, skip this tick.
2. **Schedule firing.** For each node whose `schedule` indicates a fire is due: emit `invalidate`; compute and write next `next_fire_at`; log `schedule_fired`.
3. **Pure-cascade sweep.** For each `stale` node with no `executor` and all deps `fresh`: transition `stale → fresh` inline; emit `recalculate` to dependents; log `non_resource_commit`.
4. **Stale-heartbeat sweep.** For each `running` node whose `last_heartbeat_at` < `now - heartbeat_timeout`: log `heartbeat_lost`; clear supervisor assignment; transition `running → stale`; re-enqueue (infra restart, no retry counter bump).
5. **Orphaned-claim sweep.** For each dispatch row with `claimed_by IS NOT NULL`, `claimed_at < now - orphaned_claim_timeout`, and node still `stale`: release the claim (claimant-guarded by `expected claimed_by`). Log `orphaned_claim_released`.
6. **Ready sweep.** For each `stale` executor node with all deps `fresh` and no pending dispatch row: enqueue a dispatch row.

### 6.2 Supervisor loop

1. **Register.** On startup, upsert into `rimsky_supervisors` with `id`, executor-name accept list, concurrency, callback host/port if hosting a callback endpoint.
2. **Heartbeat tick.** Every `heartbeat_interval_ms`, update own `last_heartbeat_at`, active-node count, and each active node's `last_heartbeat_at`. Poll `kill_requested` on each active node; if true, send kill signal through the executor's cancel mechanism.
3. **Claim.** While active < concurrency, query the dispatch queue for a claimable row matching this supervisor's accept list (executor names) and respecting concurrency-tag limits. Claim via `SELECT ... FOR UPDATE SKIP LOCKED`.
4. **Verify (`@blessed-invariant` — see §17).** Re-read `claimed_by` on the dispatch row IMMEDIATELY before any work begins (before the executor RPC is issued, before subprocess spawn, before any expensive operation). If the row has been released, re-claimed by another supervisor, or deleted, the supervisor logs `orphaned_claim_lost_race` and returns without dispatching. This is the hard backstop against the orphan-claim double-execute race; scenario test `verify-before-run-race` (§14.3) exercises it. The TS reference is `rimsky/src/supervisor/agentic-runner.ts:579` (`verifyAgenticClaimOwnership`).
5. **Dispatch.** Resolve `node.executor` → endpoint + transport from static config. Construct executor RPC input: `{node_id, instance_id, userdata, deps_data, reads_data, instance_params}`. Call `Execute` (gRPC or HTTP bridge per config).
6. **Handle response.** Executor returns one of:
   - `{result, changed, change_summary}` → run quality rules on `result` against node's owned resources (delegated to resource implementation). On accept, commit; emit `recalculate`. On reject (error severity), route through `on_error(quality_rule_failed)`.
   - `{blocked, reason, context}` → route through `on_error(executor_blocked)` (or custom class if declared).
   - `{error, error_class, payload}` → route through `on_error(error_class)`.
7. **Infra errors.** Transport failures (connection refused, timeout, executor process died, DNS failure) are routed through `on_error` with `infra:<class>` prefix (e.g. `infra:transport_timeout`). Consumers who want application-level policy on them declare them in `error_types`.
8. **Complete.** Delete dispatch row (claimant-guarded).

### 6.3 Concurrency tags

Per-tag limits apply at dispatch-claim time. Tags sorted lexicographically before acquiring `pg_advisory_xact_lock` (deadlock safety). Counts computed from `rimsky_dispatch.claimed_by IS NOT NULL` — the load-bearing invariant (`@blessed-invariant` in code) is that the claim window exactly brackets the run window.

Limits are supervisor-side config: each supervisor passes `concurrency_limits: map[string]int` to `StartSupervisor`.

`per-instance:{instance_id}` tags in templates receive no automatic limit — consumers who want per-instance serialization include the specific tag value in their supervisor config's limits map.

### 6.4 Pure-cascade nodes (no executor)

Handled entirely by the scheduler's pure-cascade sweep (§6.1 step 3). Never enter the dispatch queue. Never touch the supervisor. No `work_started` / `work_completed` events; a single `non_resource_commit` event is logged. Commit verdict is always `changed: true`; propagation is the purpose.

---

## 7. Protocol (`proto/v1`)

### 7.1 Transport

gRPC with Protocol Buffers is the canonical transport. The generated gRPC service is the authoritative contract.

For ease-of-implementation, the protocol ships with an HTTP+JSON bridge: each gRPC method has a parallel HTTP route using the JSON mapping from `google.api.http` annotations. A handler author writing in a language without comfortable gRPC support (or a consumer team writing a one-off internal executor) can implement the HTTP+JSON form exclusively; a conformance-certified executor must implement at least one transport.

Supervisor-side config declares `transport: grpc | http` per executor.

### 7.2 `node_executor.proto` (sketch)

```protobuf
syntax = "proto3";
package rimsky.v1;

service NodeExecutor {
  // Called by supervisor when dispatching a node. The response stream
  // carries zero or more Heartbeat events followed by EXACTLY ONE terminal
  // event (Complete | Blocked | Errored | AsyncAccepted). The executor MUST
  // close the stream immediately after emitting a terminal event; the
  // supervisor treats stream close without any terminal event as an
  // infrastructure error (see §6.2 step 7).
  rpc Execute(ExecuteRequest) returns (stream ExecuteEvent);
}

message ExecuteRequest {
  string node_id = 1;
  string instance_id = 2;
  string node_type = 3;
  google.protobuf.Struct userdata = 4;        // opaque to rimsky
  google.protobuf.Struct instance_params = 5; // with redactions applied
  map<string, google.protobuf.Value> deps_data = 6;
  map<string, google.protobuf.Value> reads_data = 7;
  string callback_url = 8;                    // for async handoff; optional
  string cancel_token = 9;                    // for in-flight cancel
}

message ExecuteEvent {
  oneof event {
    Heartbeat heartbeat = 1;
    Complete complete = 2;
    Blocked blocked = 3;
    Errored errored = 4;
    AsyncAccepted async_accepted = 5;  // terminal; see async-handoff below
  }
}

message Heartbeat {
  int64 timestamp_ms = 1;
  string note = 2;       // free-form
}

message Complete {
  google.protobuf.Value result = 1;
  bool changed = 2;
  string change_summary = 3;
}

message Blocked {
  string reason = 1;
  google.protobuf.Value context = 2;
}

message Errored {
  string error_class = 1;
  google.protobuf.Value payload = 2;
}

message AsyncAccepted {
  // Echoed back by the executor on the callback so the supervisor can correlate.
  string async_ack_id = 1;
  // Optional deadline hint; the supervisor still uses its own heartbeat-loss
  // cutoff for timeout enforcement.
  int64 expected_completion_ms = 2;
}
```

**Async handoff path.** For executors whose work cannot reasonably complete within a single held `Execute` call (e.g. `claude-agent` spawning a Claude CLI), the executor streams zero or more `Heartbeat` events and terminates the stream with an `AsyncAccepted` event. The executor is then responsible for POSTing the eventual outcome to `callback_url` (HTTP+JSON form of `Complete` / `Blocked` / `Errored` with the same `node_id` and `async_ack_id`) at some later point. The supervisor treats `AsyncAccepted` as a non-terminal outcome for the node itself — the dispatch row claim and heartbeat are held until the callback arrives (subject to the scheduler's heartbeat-loss sweep on `last_heartbeat_at`). This absorbs the TS v1 callback-MCP pattern into the protocol cleanly: the executor is free to host whatever internal mechanism (MCP, subprocess, external service) it needs; rimsky sees only the final callback.

The callback endpoint runs on the supervisor (`callback_host`:`callback_port` registered in `rimsky_supervisors`), accepts HTTP+JSON only for v1, and validates that `async_ack_id` matches an outstanding async-handoff registered by this supervisor. Unknown or stale IDs are rejected with 404.

### 7.3 Protocol versioning

- Proto files live in `proto/v1/`. The `v1` directory name is part of the package path (`rimsky.v1`).
- Changes within `v1` are backward-compatible only (new fields with default values, new methods). Breaking changes go to `proto/v2/`.
- The module ships both versions during transition periods. Executors speaking `v1` work with orchestrators speaking `v1`, several minor versions apart.
- Protocol version is advertised in a `Capabilities` RPC (not in §7.2 sketch; will be added to the full `.proto`).

### 7.4 Auth

v1 ships mTLS as the supported auth model at the executor boundary (orchestrator ↔ executor). The supervisor config specifies per-executor client cert paths; executors verify orchestrator certs. In a single-trust-zone deployment (docker-compose reference), mTLS is optional and can be disabled via config.

The control API's auth is operator-facing and handled by a pluggable `Authenticator` interface (default: none; reference binary binds localhost by default).

### 7.5 Conformance test suite

A Go binary (`rimsky-conformance`) that, given an executor endpoint + transport, runs a battery of protocol-conformance scenarios:

- Correct `Execute` for valid request.
- Correct rejection of malformed `userdata`.
- Correct `Blocked` / `Errored` terminal events.
- Async handoff via `callback_url` (if advertised in capabilities).
- Heartbeat emission on long-running calls.
- Cancel-token handling.
- Result-serialization edge cases (BigInt-equivalent types, circular structures, null handling).

Shipped as a reusable artifact; executor authors run it before declaring their implementation protocol-compliant.

---

## 8. Resource library

### 8.1 The `Resource` Go interface

```go
package resource

type Resource interface {
    // Identity
    Path() []string
    OwnerNodeID() UUID

    // Versioning
    CurrentVersion(ctx context.Context) (*Version, error)
    PreviousVersion(ctx context.Context) (*Version, error)
    ListVersions(ctx context.Context, limit int) ([]*Version, error)

    // Commit flow. The resource runs quality rules internally (rules are
    // bound at Factory.Create time — see §8.2) and returns reject with
    // populated QualityErrors on error-severity failure. Warning-severity
    // failures are logged internally (via `quality_rule_failed` event with
    // severity=warning) and do not populate QualityErrors.
    Commit(ctx context.Context, req CommitRequest) (*CommitResult, error)
    NoOpCommit(ctx context.Context) error

    // Rollback. Implementations that cannot support rollback return
    // ErrRollbackUnsupported; policy chains invoking rollback treat that
    // as an error-class outcome per §8.5.
    RestoreVersion(ctx context.Context, target VersionRef) (*Version, error)
}

type Version struct {
    ID             UUID
    ProducedByNode UUID
    Data           json.RawMessage   // inline-jsonb
    DataRef        string            // for external-backed resources (S3 key, SQL row reference)
    ChangeSummary  string
    CommittedAt    time.Time
}

type CommitRequest struct {
    ProducedBy     UUID
    Result         any
    Changed        bool
    ChangeSummary  string
}

type CommitResult struct {
    Accepted       bool
    Version        *Version            // populated if Accepted
    QualityErrors  []QualityRuleError  // populated if Accepted == false
}

type VersionRef struct {
    Kind string // "previous" | "id"
    ID   UUID   // when Kind == "id"
}
```

Implementations register themselves with a name the template can reference:

```go
resource.Register("inline-jsonb", inlinejsonb.Factory)
resource.Register("external-sql", externalsql.Factory)
```

### 8.2 Implementation config schema

Each implementation declares a JSON Schema describing the `config` block its templates expect:

```go
// Config is the placeholder-resolved, schema-validated config block from the
// template's owns_resources[].config, passed as a generic decoded JSON map.
// Implementations cast fields to their expected Go types; ConfigSchema()
// guarantees the shape.
type Config map[string]any

type Factory interface {
    ConfigSchema() []byte             // JSON Schema for the template's config block

    // Create is called at instance provisioning. cfg is the (validated,
    // placeholder-resolved) config block from the template. rules is the
    // per-node quality_rules block, also from the template, bound into the
    // resource once at creation — Commit() invokes them internally on every
    // commit. reg is the shared resource registry (for version pointers).
    Create(cfg Config, rules []QualityRuleSpec, reg Registry) (Resource, error)
}

type QualityRuleSpec struct {
    Type     string                 // builtin name or "custom"
    Config   map[string]any
    Severity string                 // "error" | "warning"
}
```

Template validation uses `ConfigSchema()` to validate the `config` block at deploy. This gives operators deploy-time errors rather than instantiation-time surprises. Quality rules are validated separately against the registered rule-type registry.

### 8.3 `inline-jsonb`

- Stores commit data as `JSONB` in `rimsky_resource_versions.data`.
- `RestoreVersion("previous")` sets `current_version_id = previous_version_id`, drops the rejected-current-version row, writes a rollback event.
- GCs older-than-`keep_versions` rows on commit.

Config schema:

```yaml
keep_versions: int    # default 2
```

### 8.4 `external-sql`

- Stores version manifest in `rimsky_resource_versions` (with `data_ref`, not `data`).
- Writes committed data to a consumer-declared SQL table via configured connection.
- Uses a staging-table + atomic-swap pattern for double-buffering: commit N writes to `staging`, swap renames `current` → `previous`, `staging` → `current` under transaction.
- `RestoreVersion("previous")` swaps `previous` ↔ `current`.
- Connection pool separate from rimsky's own; lifecycle-managed by the resource.

Config schema:

```yaml
connection_ref: string      # named connection in supervisor config (no inline credentials)
schema: string
table: string
staging_table: string       # optional; default: "{table}__staging"
previous_table: string      # optional; default: "{table}__previous"
primary_key: [string]
```

Connections are named in supervisor config:

```yaml
sql_connections:
  example_production:
    url: "${EXAMPLE_PG_URL}"
```

This keeps credentials out of templates.

### 8.5 Rollback semantics

Rollback is a resource-level capability. Three states:

- **Supported:** `RestoreVersion("previous")` succeeds, swapping current/previous atomically.
- **Supported with constraint:** the resource may reject specific rollback targets (e.g. a target that has already been GC'd).
- **Unsupported:** the resource returns `ErrRollbackUnsupported`. Policy chains that invoke rollback see this as an error; the chain proceeds to the next action.

This removes the TS v1 muddle where `restore_version` was a message-level feature. In Go v1, the message is unchanged (`invalidate` carries `restore_version`), but the supervisor routes it to the resource and *asks*; the resource decides.

### 8.6 Quality rules

Builtin rule types (ported from TS v1 §5.3):

- `row_count_ratio` — ratio against previous version's count.
- `no_nulls` — specified fields must be non-null.
- `nullable_fields_present` — fields must exist in schema.
- `custom` — registered handler function.

Custom rule handlers register alongside resource implementations:

```go
qualityrule.Register("example:custom_rule", exampleCustomRule)
```

Template's `owns_resources[].quality_rules` refers to these by name. Severity is per-rule-invocation in the template, not per-rule-definition.

---

## 9. Executor library

### 9.1 Reference executors shipped in v1

**`http-node`** (Go, `executors/http-node/`):
- Protocol implementation: gRPC + HTTP bridge.
- On `Execute`: takes `userdata.url`, `userdata.method`, `userdata.headers`, `userdata.body`. Makes the HTTP request. Commits the response body (JSON or bytes). Returns `changed: true` if response differs from cached previous result; `changed: false` if identical by configured comparison.
- Config via env vars: `TIMEOUT_MS`, `MAX_BODY_BYTES`, optional TLS cert verification knobs.

**`claude-agent`** (TypeScript, `executors/claude-agent/`):
- Protocol implementation: gRPC + HTTP bridge (Node gRPC libraries for gRPC; Fastify for HTTP bridge).
- Uses async handoff: on `Execute`, accepts the job, spawns Claude CLI subprocess with its own internal MCP callback, streams heartbeats, posts terminal outcome to the orchestrator's `callback_url`.
- Encapsulates Claude CLI subprocess management, internal MCP callback, JSON-schema validation of agent-reported results, silence detection, and subprocess teardown — all rehomed out of the supervisor per the three-collections architecture.
- `userdata` shape: `{model, system_prompt, user_prompt_template, tools, result_schema}`.
- Performs JSON-schema validation of agent-reported `result` against `userdata.result_schema` before calling the orchestrator's callback.
- Publishes to npm as `@rimsky/executor-claude-agent`; also ships as a Docker image.

### 9.2 Executor author contract

An executor service:

1. Implements the `NodeExecutor.Execute` gRPC method (and/or the HTTP bridge).
2. Handles `ExecuteRequest.cancel_token` to abort in-flight work.
3. Emits at least one terminal event (`Complete` | `Blocked` | `Errored`) per `Execute`, or routes to async handoff.
4. Passes the conformance test suite.

Executors are operationally separate processes. They register no runtime state with rimsky; they are pointed-at via supervisor config.

### 9.3 Executor resolution

Supervisor config (§10.2) maps executor names to endpoints. At dispatch time:

1. Supervisor claims a dispatch row for a node with `executor: "X"`.
2. Supervisor looks up `X` in its config. If absent: log `unresolved_executor` event, route the node to `on_error(unresolved_executor)`.
3. Supervisor constructs the RPC call against the configured endpoint + transport.

The supervisor's configured executor list also serves as the dispatch queue claim filter: a supervisor with config entries for `claude-agent` and `http-node` only claims dispatch rows whose node's executor is one of those.

---

## 10. Processes and configuration

### 10.1 Three long-running processes

Same as TS v1: **scheduler**, **supervisor**, **control-api**. Each shipped as a standalone Go binary and a Docker image.

### 10.2 Supervisor config

Read from YAML file + env-var expansion:

```yaml
supervisor_id: "${HOSTNAME}"
postgres_url: "${RIMSKY_PG_URL}"
concurrency: 8
heartbeat_interval_ms: 5000
claim_poll_interval_ms: 1000

executors:
  claude-agent:
    transport: grpc
    endpoint: "${CLAUDE_AGENT_ENDPOINT:-http://claude-agent:9090}"
    tls: optional
  http-node:
    transport: http
    endpoint: "${HTTP_NODE_ENDPOINT:-http://http-node:9091}"

concurrency_limits:
  agentic: 10
  "per-instance:example-tenant": 1

# Named SQL connections available to external-sql resources (§8.4). Credentials
# live here, not in templates. Connection names referenced by
# `owns_resources[*].config.connection_ref` at template deploy must exist here
# when that template's instance runs on this supervisor.
sql_connections:
  example_production:
    url: "${EXAMPLE_PG_URL}"

callback:
  host: "0.0.0.0"            # bind address
  port: 9100                 # bind port
  advertise_host: ""         # optional; hostname executors should use to reach this supervisor. Falls back to the bind address if empty. In k8s/docker-compose, set to the supervisor's service name or pod IP — `0.0.0.0` is not routable from other containers.
  advertise_port: 0          # optional; falls back to the bind port if zero.
```

Supervisor registration writes `callback_host`/`callback_port` into `rimsky_supervisors` using the advertised values (not the bind values) so executors receive a reachable URL in `ExecuteRequest.callback_url`.

### 10.3 Scheduler and control-api configs

Simpler — scheduler takes Postgres URL, tick interval, heartbeat/orphan timeouts, advisory-lock pool reference. Control API takes Postgres URL, bind host/port, optional auth module path.

### 10.4 Library entry points

```go
package rimsky

func StartScheduler(cfg SchedulerConfig) (*SchedulerHandle, error)
func StartSupervisor(cfg SupervisorConfig) (*SupervisorHandle, error)
func StartControlAPI(cfg ControlAPIConfig) (*ControlAPIHandle, error)
```

Each handle has `Shutdown(ctx) error` and a `Health() HealthReport` method.

### 10.5 Reference binaries

`cmd/rimsky-scheduler/main.go`, `cmd/rimsky-supervisor/main.go`, `cmd/rimsky-control-api/main.go`. Thin env-var readers that build the config struct and call the library entry point. Handle SIGTERM/SIGINT → graceful shutdown with context timeout.

### 10.6 Go library choices

- **Postgres:** `jackc/pgx/v5`. `database/sql`-compat mode for flexibility; `pgx` native for hot paths (dispatch-queue claim).
- **HTTP router:** `go-chi/chi`. Small, stdlib-friendly, no middleware magic.
- **gRPC:** `google.golang.org/grpc`.
- **Config parsing:** `github.com/knadh/koanf` with YAML + env providers. Avoid Viper's surface area.
- **CLI:** stdlib `flag` for binaries; no Cobra for v1 (no subcommands yet).
- **Structured logging:** stdlib `log/slog`. No Zap, no Zerolog.
- **Validation:** `github.com/xeipuuv/gojsonschema` for JSON Schema (templates, userdata, resource configs).
- **Cron parsing:** `github.com/robfig/cron/v3`.
- **Testing:** `testify/require` + `testcontainers-go`.

---

## 11. Storage

### 11.1 Postgres schemas

Tables keep their TS v1 shape with the following changes:

- `rimsky_nodes` drops `kind TEXT`. `node_type`, `executor` (nullable), `schedule_cron` (nullable) replace it. A node's execution strategy is derived from these fields.
- `rimsky_timers` removed. `rimsky_schedules` new: `(node_id, cron_expr, next_fire_at, last_fired_at)`. All nodes with schedules; no kind check.
- `rimsky_dispatch.node_kind` column renamed to `rimsky_dispatch.executor_name` (nullable) and used by the supervisor's accept-list filter instead of `kind = ANY(...)`.
- `rimsky_supervisors.accepts` column becomes `accepted_executors TEXT[]` (executor names, not kinds).
- `rimsky_resource_versions` unchanged.
- `rimsky_events` unchanged except new kinds (§11.2).

### 11.2 Event kinds (delta vs. TS v1)

Added:

- `schedule_fired` — payload: `{node_id, cron_expr, fired_at}`. Replaces `timer_fired`.
- `schedule_dispatch_failed` — payload: `{node_id, error}`. Emitted when a schedule tick cannot fire (invalid cron expression, DB write failure, or cascade error); non-fatal — other schedules in the same tick continue.
- `unresolved_executor` — payload: `{node_id, executor_name, supervisor_id}`.
- `pure_cascade_commit` — synonym/replacement for the v1 `non_resource_commit`; emitted when the scheduler transitions a pure-cascade node inline.

Removed:

- `timer_fired` (use `schedule_fired`).
- `timer_dispatch_failed`.

Kept (core): `state_transition`, `error`, `work_started`, `work_completed`, `commit`, `no_op_commit`, `quality_rule_failed`, `heartbeat_lost`, `operator_override`, `orphaned_claim_released`, `orphaned_claim_lost_race`, `message_emitted`, `message_received`, `work_rejected` (emitted by the supervisor when the response from an executor fails protocol-level validation — e.g. the `Complete.result` is not serializable as JSON, or violates the `userdata.result_schema` contract the executor is responsible for enforcing; payload: `{reason, errors}`; surfaced so operators can see "executor returned, but its output was unusable" distinct from application-level errors).

**Removed from TS v1:** `silence_detected`. Silence detection was a supervisor-internal concern in TS v1 because the supervisor owned subprocess lifecycle. Under the Go architecture, the supervisor does not own subprocesses — the executor does. An executor that wants to report a silence-style timeout does so by emitting `Errored { error_class: "silence_timeout" }` via the protocol (§7.2); that produces a normal `error` event with the chosen class, and the node's `error_types` map handles it if the template author wants application-level policy. Rimsky does not have a distinct "silence" concept.

### 11.3 Migrations

`core/migrations/*.sql`, embedded via `//go:embed`. Numbered files, applied in order, tracked in `rimsky_migrations`. Migration runner holds `pg_advisory_lock` (session) to prevent concurrent-runner corruption. `npm run migrate` equivalent is `rimsky-migrate` binary.

The first migration (`001-initial.sql`) defines the Go v1 schema — it is not a diff against TS v1 migrations. The Go rimsky owns its own migration line; consumers migrating from the TS v1 prototype manage their own data-migration work outside rimsky.

---

## 12. Control API

HTTP+JSON, unchanged in shape from TS v1:

- `POST /templates`, `GET /templates`, `GET /templates/:id`, `DELETE /templates/:id`.
- `POST /instances`, `GET /instances`, `GET /instances/:id_or_key`, `DELETE /instances/:id_or_key`.
- `GET /nodes/:id`, `GET /instances/:id_or_key/nodes`.
- `POST /nodes/:id/invalidate`, `POST /nodes/:id/reset`, `POST /nodes/:id/kill`.
- `GET /events` (paginated, filterable).
- `GET /resources/:id/current`, `GET /resources/:id/versions`, `GET /resources/:id/versions/:version_id`.
- `GET /health` (with supervisor/executor health rollup).
- `GET /metrics` (Prometheus).

Auth: pluggable `Authenticator` interface. Default: none. Reference binary binds to localhost by default. Enterprise deployments set up their own auth module.

---

## 13. Observability

- **Logging:** `log/slog` throughout, JSON output by default, field-structured. Request-scoped correlation IDs through context.
- **Metrics:** Prometheus format at `/metrics` on each process. Core metrics: dispatch-queue depth, claim latency, node-state distribution, executor RPC latency by executor name, scheduler tick latency, Postgres connection pool stats.
- **Events:** the append-only `rimsky_events` table remains the primary audit trail. Events are the source of truth; metrics and logs are derived.
- **Health:** `/health` on each process reports `{status: ok | degraded, details}`. Control API's `/health` aggregates all supervisors and configured executors (probed via their gRPC health-check endpoints).

---

## 14. Testing strategy

### 14.1 Unit tests (`*_test.go` alongside source)

- Node state machine — transition table coverage.
- Policy evaluator — all action types, recurrence semantics, reset on success, unknown-class give-up.
- Template validator — every validation rule has a positive and negative test.
- Quality-rule evaluators (per builtin).
- Cron next-fire calculation.
- Backoff/jitter math.

### 14.2 Integration tests (testcontainers-go + Postgres)

- Storage: every store interface method, including tx participation.
- Dispatch queue: claim contention, orphan-claim sweep, claimant-guarded operations.
- Migration runner: idempotency, concurrent-run safety.

### 14.3 Scenario tests (`test/scenarios/*_test.go`)

Full-stack end-to-end. Same coverage as TS v1 ports cleanly:

- `happy-path-executor` — deterministic executor node runs, commits, cascades.
- `pure-cascade` — pure-cascade node invalidated, transitions inline, propagates.
- `scheduled-node` — schedule fires, node invalidated, runs.
- `fan-out-pattern` — schedule on root cascade node, multiple dependents run.
- `cascade-invalidate` — downstream error invalidates upstream, re-runs.
- `give-up` — policy chain exhausts, node failed.
- `double-buffering` — quality rule rejects new commit, previous stays current.
- `rollback-via-restore-version` — `invalidate(restore_version: previous)` swaps.
- `agentic-executor-async-handoff` — executor returns async, posts to callback, commit proceeds.
- `executor-blocked` — executor reports blocked, routed to `on_error(executor_blocked)`.
- `unresolved-executor` — node references unconfigured executor, routed cleanly.
- `heartbeat-loss-reenqueue` — supervisor dies, node reclaimed.
- `orphaned-claim` — claim released after orphan cutoff, fresh supervisor picks up.
- `verify-before-run-race` — orphan sweep releases a claim AFTER supervisor A claims but BEFORE supervisor A's runner dispatches; supervisor B re-claims; supervisor A's verify-before-run (§6.2 step 4) detects the lost claim and bails; only supervisor B's run proceeds. Exercises the `@blessed-invariant` in §17.
- `state-machine-same-state-rejected` — two supervisors race to `UpdateState(node, "running", dispatch_claimed)`; one succeeds, the second receives an illegal-transition error rather than a silent idempotent accept. Exercises the `@blessed-invariant` in §17.
- `concurrency-tag-limit` — two nodes with same tag, second waits.
- `no-op-commit` — executor returns `changed: false`, no cascade.

All scenarios use `SystemClock` against testcontainers Postgres (the Postgres `NOW()` governs claim eligibility).

### 14.4 Protocol conformance suite

`cmd/rimsky-conformance` Go binary; runs against any executor endpoint. Validates the protocol contract from §7. Shipped as a Docker image for executor authors to run in CI.

**Deterministic executors** (e.g. `http-node`): conformance runs against the live executor; tests use stubbed upstreams where needed (a local HTTP fixture server for `http-node`) so every assertion is deterministic and CI-friendly.

**LLM-calling executors** (e.g. `claude-agent`): conformance runs against the executor in a **stub mode** that short-circuits the LLM call with a canned response. Every executor that wraps nondeterministic external services MUST expose a stub-mode config flag (convention: env var `RIMSKY_EXECUTOR_STUB_MODE=1`) and MUST pass conformance in that mode. Live-mode testing with a real LLM is the executor author's responsibility — out of scope for v1 conformance. This keeps §20's "conformance green in CI" success criterion deterministic and achievable without paying API costs or depending on model availability.

**Enforcement.** The `rimsky-conformance` binary takes a `--require-stub-mode` flag (default: enabled for the bundled `claude-agent` conformance profile, disabled for the `http-node` profile). When set, the binary issues a probe request at startup that MUST return a canned stub response; if the executor answers with evidence of a live call (or rejects the probe as unsupported), conformance fails immediately with a clear error. The probe contract is part of the conformance suite itself, not the protocol, so no changes to `proto/v1` are needed. (A future `Capabilities` RPC on the protocol, flagged in §7.3, is the long-term mechanism for advertising stub-mode support; v1 uses the probe-and-fail-fast approach.)

### 14.5 Cross-language SDK tests

No SDK tests per se (there are no SDKs; §Q3 decided). But the conformance suite is the test every executor — Go, TS, Python, others — runs to self-verify. This is the operational equivalent.

---

## 15. Documentation

Rimsky-project docs live under `rimsky-go/docs/` and ship with the project; they travel with it on extraction.

Deliverables for v1:

- **`rimsky-go/docs/node-graph-design.md`** — the Go port's conceptual reference. Derived from the TS project's `docs/cell-graph-design.md` with node terminology and the three-collections architecture; see §18 for the substantive changes.
- **`rimsky-go/docs/architecture.md`** — implementation shape: package layout, import graph, process model, distribution.
- **`rimsky-go/docs/protocol.md`** — node-executor protocol reference (gRPC + HTTP bridge, message shapes, auth, conformance).
- **`rimsky-go/docs/operator-guide.md`** — deploying rimsky (Docker Compose, Helm), configuring supervisors, writing templates, monitoring.
- **`rimsky-go/docs/executor-author-guide.md`** — writing a new executor in any language, running the conformance suite, publishing.
- **`rimsky-go/docs/resource-author-guide.md`** — writing a new resource implementation (Go; other languages out of scope in v1 since resources are in-process to the orchestrator).

Consumer-specific migration guides (for teams porting existing work onto rimsky) are authored by those consumers in their own documentation, not in rimsky's. Rimsky provides the model, the protocol, the reference implementations, and the author guides; consumer-side migration planning is downstream.

The TS conceptual doc (`docs/cell-graph-design.md`) is left unchanged — it remains the accurate conceptual reference for the TS project, which is not being published. The Go port's conceptual doc (`rimsky-go/docs/node-graph-design.md`) is the standalone reference for the published Go rimsky and is authored fresh for it (it reads the TS doc as a source, but is not a patched version of it).

---

## 16. Migration and OSS extraction

### 16.1 Concurrent development

- TS rimsky stays in `/rimsky/` unchanged throughout this port; it continues to serve whichever consumer(s) were using it before.
- Go rimsky is developed in `/rimsky-go/` (new directory) in parallel.
- Zero code sharing between the two; they exchange only design decisions documented in this spec.

### 16.2 OSS extraction

Once Go rimsky reaches feature completeness (this spec's §2.1 scope) and has been exercised end-to-end:

1. `/rimsky-go/` lifted out into its own public repository.
2. Package name changes from internal path to `github.com/<org>/rimsky` (final org TBD).
3. Docs repositioned from monorepo-context to standalone.
4. CI, license, README, CONTRIBUTING, issue templates added.
5. Published Docker images go to a public registry.
6. Published Go module via standard Go module proxy.
7. Published npm package (`@rimsky/executor-claude-agent`) to public npm.

The extraction is mechanical. The architectural isolation enforced by this spec is what makes it so.

---

## 17. Invariants preserved from TS v1

These are load-bearing and must survive the port unchanged in semantics:

- **State machine rejects illegal transitions.** `updateState` never short-circuits on `from == to`. Double-execute detection depends on it.
- **Dispatch claim brackets run.** Tag-limit counts come from `rimsky_dispatch.claimed_by IS NOT NULL`, not from node state. The claim window exactly brackets the node's `running` window. Refactoring the claim/complete flow must preserve this.
- **Per-tag locks acquired in sorted order.** Prevents deadlock between concurrent claims sharing a tag subset.
- **Claimant-guarded release.** `releaseClaim(id, expected_claimed_by)` is a no-op if mismatch. Prevents stale sweeps from nulling live claims.
- **Verify-before-run.** Runners re-read `claimed_by` after claim, before any expensive work. Hard backstop against orphan-claim race.
- **Generous orphan-claim cutoff.** Default `5 * heartbeat_timeout`. Lower values are a double-execute vector.
- **Advisory lock on scheduler tick.** `pg_try_advisory_lock` prevents multi-replica double-ticks.
- **Session advisory lock on migrations.** Prevents concurrent-runner migration-table corruption.

Each gets a `// @blessed-invariant` comment and a scenario test that exercises the edge case.

---

## 18. Changes from `cell-graph-design.md` in the new `node-graph-design.md`

The Go port's conceptual doc (`rimsky-go/docs/node-graph-design.md`) is derived from the TS project's `docs/cell-graph-design.md`. The TS doc stays in place unchanged; the Go doc is a fresh authoring that carries the model forward with these substantive differences (plus the global cell → node rename covered in §1.5):

- §3.1 kinds list changes from "Resource-owning cells / Timer cells / External trigger cells" to "Nodes can be any combination of: executor-invoking, schedule-driven, resource-owning, dependency-coupled, pure-cascade." Kinds are properties, not classes.
- §3.2 state transitions add a `stale → fresh` inline transition for pure-cascade nodes.
- §7 (parameterization) distinguishes `userdata` (per-node opaque block) from `source context` (per-instance).
- §8 (node contract) strips `kind`; replaces with optional `executor`, optional `schedule`.
- §9 (lifecycle) adds the pure-cascade execution path.
- §11 (deferred) adds s3-object resources, sql-node/langgraph-node executors, control-API gRPC surface, pypi publishing.

The concept of "agentic nodes" is demoted from a first-class kind to an emergent pattern: "nodes whose executor runs an LLM are called agentic."

---

## 19. Open items (deferred for implementation discretion)

- Format of supervisor config file (YAML vs. HCL vs. JSON) — YAML is the prior, but open to revisit.
- Whether the conformance suite ships as a library executor authors link (and write Go test harness around) or as a binary they run against their endpoint. Leaning binary.
- Whether `external-sql` supports arbitrary SQL dialects in v1 or Postgres-only. Leaning Postgres-only; MySQL/others post-v1.
- `per-instance:*` wildcard support in concurrency tags. Deferred to post-v1 (same as TS v1).

---

## 20. Success criteria for v1

- All scenarios in §14.3 pass.
- Conformance suite runs green against both reference executors.
- `docker compose up` brings up a working rimsky deployment on a laptop, with the reference executors reachable, in under 60 seconds.
- Helm chart deploys a working rimsky on a Kubernetes cluster.
- An end-to-end reference-deployment smoke test (Docker Compose bring-up + template deploy + instance lifecycle + both reference executors reachable + commit flow through inline-jsonb resource) passes against a real Postgres.
- The TS rimsky execution log's equivalent Go execution log reports build ✓ · tests ✓ · lint ✓ · migrations ✓ · binaries ✓ · exports ✓ · CHANGELOG ✓.

---

## 21. Glossary (delta from `cell-graph-design.md` §12, applied in the new `node-graph-design.md`)

- **Node** — unchanged (unit of work).
- **Pure-cascade node** — node with no `executor`. Transitions `stale → fresh` inline when dependencies settle; exists to propagate cascade without doing work itself. Replaces the TS v1 timer-node pattern when combined with `schedule`.
- **Executor** — a service that implements the node-executor protocol and does work on behalf of nodes that reference it by name.
- **Executor name** — the string that appears in `nodes[*].executor` and is resolved by supervisor config to an endpoint.
- **`userdata`** — opaque JSON block on a node template, passed verbatim to the executor on `Execute`. Replaces the TS v1 per-kind `execution` block.
- **`schedule`** — optional cron expression on any node. Scheduler emits `invalidate` when it fires.
- **Resource implementation** — a Go implementation of the `Resource` interface (e.g. `inline-jsonb`, `external-sql`). Registered by name, referenced by templates.
- **Conformance suite** — the `rimsky-conformance` Go binary that validates a given executor endpoint against the protocol contract.
- **Async handoff** — pattern where an executor returns immediately from `Execute`, then calls back to the orchestrator with the eventual outcome.
- **Three collections** — the architectural separation of orchestrator, resource library, and executor library. Versioned together in v1, separable indefinitely.
