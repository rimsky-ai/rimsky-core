# feature-index

Per-feature index for cold-read onboarding. Each entry names a feature's
canonical directory + a one-line purpose + the layer it sits in + the
features it depends on. Layer ordering is foundation → graph → runtime →
control → cmd / stores / executors / sensors / subscribers / dashboards.
Keep entries terse; deep design lives in `.ok-planner/specs/` and
`docs/concepts/`.

## Foundation layer (`foundation/`)

| Feature | Path | Depends on | Purpose |
| --- | --- | --- | --- |
| cascade | `foundation/cascade/` | shared | Node-state machine (`NodeState`, `LastOutcome`, `ErrIllegalTransition`); illegal-transition rejection. |
| locks | `foundation/locks/` | shared | `ClaimProducer` protocol Go interface + claim/lock primitive types (`ClaimID`, `ClaimSpec`, `NamedLockSpec`, `ClaimResult`, `Registry`); in-Go `storetest` fake. |
| persistence | `foundation/persistence/` | shared, spec, cascade, locks | Database / Tables / Tx interfaces plus the postgres + sqlite drivers; migration runner; advisory-lock primitives; blob backends. |
| persistence/conformance | `foundation/persistence/conformance/` | persistence | Driver-agnostic conformance suite for `Tables` impls (postgres + sqlite). |
| persistence/internal | `foundation/persistence/internal/` | persistence | Package-private helpers; depguard `foundation-internal-isolation` blocks external imports. |
| internal/pgtest | `foundation/internal/pgtest/` | (test-only) | Testcontainers-based postgres fixture used inside foundation. |
| shared | `foundation/shared/` | (stdlib) | Infrastructure types: `Clock`, `Logger`, `UUID`, `DeepMergeJSON`, error helpers, `SilentLogger`. |
| spec | `foundation/spec/` | (stdlib) | Persistable row-type primitives: `TemplateSpec`, `TemplateNodeDef`, `EvaluatorState`, `ErrorTypePolicy`, frame-resolution constants, claim-handle state enum, severities, backoff kinds. Pure data; no algorithms. |

## Protocols layer (`protocols/`)

| Feature | Path | Depends on | Purpose |
| --- | --- | --- | --- |
| protocols | `protocols/` (separate Go module) | (grpc, protobuf) | The three service-protocol Go interfaces (`ClaimProducer`, `Executor`, `LifecycleSubscriber`) + `.proto` sources + generated gRPC bindings. |

## Graph layer (`graph/`)

| Feature | Path | Depends on | Purpose |
| --- | --- | --- | --- |
| attribute | `graph/attribute/` | spec, locks, foundation/shared | Substitution layer (`{{...}}` directive resolution against deps / claim / params / trigger / child contexts) + schema validation + callback wiring. |
| frame | `graph/frame/` | persistence, spec, cascade | Frame-resolution model: queued/serial/coalesce frame enqueue, per-instance frame-delivery mode, advance/start transitions. |
| node | `graph/node/` | spec, cascade, locks | Template + node algorithms over `foundation/spec` row types: `Evaluate`, `HoldingSubgraphsForTemplate`, `ValidateTemplate`, `RequiredStores`; subscription-edge inverse map; substitution-ref parsing; inheritance + co-holdership topology. |
| scheduler | `graph/scheduler/` | persistence, frame, node, cascade, runtime (per-file exemption) | Scheduler-tick orchestration: advisory-lock guarded tick, pure-cascade fan-out, scope-substitution-resolution sweep, sweep coordination (the actual sweeps live in `runtime/`). |
| shared | `graph/shared/` | spec | Graph-specific aliases / enums (e.g. `AccessKind`, `MessageType`); re-exports of `Severity`/`BackoffKind`/`JitterKind` from `foundation/spec`. |
| template/canonical | `graph/template/canonical/` | spec | RFC 8785 JCS-canonicalized template-spec hashing (`CanonicalSpecHash`). |
| scenario | `graph/scenario/` | full stack | End-to-end scenario test harness (boots scheduler + supervisor + control-api against testcontainers Postgres). |

## Runtime layer (`runtime/`)

| Feature | Path | Depends on | Purpose |
| --- | --- | --- | --- |
| supervisor / runner | `runtime/runner*.go`, `runtime/supervisor.go` | foundation, graph, protocols | Per-supervisor acquisition loop: candidate selection, atomic claim acquisition tx, dispatch to executor pool, terminal event handling, error-type policy routing. |
| auto-terminal | `runtime/auto_terminal*.go`, `runtime/terminal_decision*.go` | persistence, cascade, locks | Held-claim aggregate-outcome resolution (Commit vs Abandon vs force-cancel); unified terminal-decision engine `ResolveClaimHandleTerminal`; sibling-cancel walk; descendant-cancel walk; recursive parent-claim chain. |
| sub-graph dispatch | `runtime/subgraph_dispatch.go`, `runtime/fanout_dispatch.go` | graph/node, persistence | Sub-graph caller absorption (entry-node shared identity), fan-out child dispatch (per `partition_key`), state propagation up the run tree. |
| sweeps | `runtime/sweep_*.go`, `runtime/conductor.go` | persistence | Scheduler-driven sweeps: orphan claim reaping, stale heartbeats, orphaned node-runs, parked nodes ready-for-resume, parked-overdue (timeout to failed), claim-handle retention (committed/abandoned cutoff). |
| run-tree state propagation | `runtime/state_propagation.go`, `runtime/run_tree.go` | persistence, cascade | Child→parent aggregation policies (`strict` / `min_quorum` / etc.); leaf-run terminal → parent-run state aggregation. |
| lineage writer | `runtime/lineage_writer.go` | persistence, graph/node | Append-only emit path for `leaf_run` + `claim_terminal` lineage rows; hash helpers; SubstitutionRef collection. |
| messages + publishers | `runtime/message_delivery.go`, `runtime/publishers.go`, `runtime/backfill.go` | persistence | Frame-boundary message delivery; publisher Subscribe/Unsubscribe lifecycle + resync sweep; backfill operation orchestration. |
| remote | `runtime/remote/` | protocols | The only concrete gRPC `ClaimProducer` client. |
| executor pool | `runtime/executor/` | protocols | Executor gRPC client pool. |

## Control layer (`control/`)

| Feature | Path | Depends on | Purpose |
| --- | --- | --- | --- |
| controlapi | `control/controlapi/` | runtime, persistence, foundation | HTTP control plane: templates, instances, nodes, lineage, assets, backfills, publisher-subscriptions, messages, observability, admin diagnostics. Fires lifecycle events synchronously at state transitions. |
| cli | `control/cli/` | controlapi | `rimsky-cli` thin client + `compose` workflow (template-tag-prefixed multi-instance dispatch). |
| observability | `control/observability/` | persistence, runtime | Diagnostics-cache, cascade-graph endpoint, metrics, lock-holder browse, node-run browse. |
| config | `control/config/` | foundation, runtime, graph, persistence | Unified `rimsky.yml` parsing + validation + per-process handle assembly. |

## Reference binaries (`cmd/`)

| Binary | Path | Purpose |
| --- | --- | --- |
| rimsky-scheduler | `cmd/rimsky-scheduler/` | Scheduler-tick process. |
| rimsky-supervisor | `cmd/rimsky-supervisor/` | Supervisor (runner + callback server). |
| rimsky-control-api | `cmd/rimsky-control-api/` | Control-plane HTTP server. |
| rimsky-migrate | `cmd/rimsky-migrate/` | Migration runner. |
| rimsky-entrypoint | `cmd/rimsky-entrypoint/` | Unified PID-1 (`rimsky/all` image). |
| rimsky-cli | `cmd/rimsky-cli/` | Thin client CLI. |
| rimsky-executor-conformance | `cmd/rimsky-executor-conformance/` | Probe-based conformance harness for `Executor` impls. |
| rimsky-claim-producer-conformance | `cmd/rimsky-claim-producer-conformance/` | Conformance harness for `ClaimProducer` impls. |
| rimsky-data-processing-conformance | `cmd/rimsky-data-processing-conformance/` | Conformance harness for `DataProcessing` impls. |
| rimsky-validation-conformance | `cmd/rimsky-validation-conformance/` | Conformance harness for `Validation` impls. |
| rimsky-publisher-conformance | `cmd/rimsky-publisher-conformance/` | Conformance harness for `Publisher` impls. |
| rimsky-blob-backend-conformance | `cmd/rimsky-blob-backend-conformance/` | Conformance harness for `BlobBackend` impls. |
| rimsky-conformance-probe | `cmd/rimsky-conformance-probe/` | Stub-mode probe used by executor-conformance startup. |
| rimsky-verifier-http | `cmd/rimsky-verifier-http/` | Reference verifier executor (HTTP-shape checks). |
| rimsky-verifier-shape-checks | `cmd/rimsky-verifier-shape-checks/` | Reference verifier executor (JSON-shape checks). |
| rimsky-docs-glossary / lint / llms-full | `cmd/rimsky-docs-*/` | Docs build tooling. |
| rimsky-license-check | `cmd/rimsky-license-check/` | License-boundary lint + header stamp. |

## Bundled service reference impls

### Claim producers (`stores/`)

| Producer | Path | Purpose |
| --- | --- | --- |
| filesystem | `stores/filesystem/` | Concrete-paths producer; opt-in `LifecycleSubscriber`. |
| postgres | `stores/postgres/` | Regional-access + items-queue producer; opt-in `LifecycleSubscriber`. |
| stub | `stores/stub/` | In-memory test fixture. |
| common | `stores/common/` | Shared helpers across the reference impls. |

### Executors (`executors/`)

| Executor | Path | Purpose |
| --- | --- | --- |
| http-node | `executors/http-node/` | HTTP-driven executor (Go). |
| stub | `executors/stub/` | In-memory test fixture (Go). |
| claude-agent | `executors/claude-agent/` | TypeScript / npm executor. |
| verifier-http | `executors/verifier-http/` | Verifier executor that runs HTTP-shape checks. |
| verifier-shape-checks | `executors/verifier-shape-checks/` | Verifier executor that runs JSON-shape checks. |

### Sensors (`sensors/`)

| Sensor | Path | Purpose |
| --- | --- | --- |
| sensor-cron | `sensors/sensor-cron/` | Cron-firing sensor (replaces the retired per-node `schedule:` field). |
| sensor-http | `sensors/sensor-http/` | HTTP polling sensor. |
| sensor-object-store | `sensors/sensor-object-store/` | Object-store change-detection sensor. |
| sensor-webhook | `sensors/sensor-webhook/` | Webhook-receiver sensor. |

### Lifecycle subscribers (`subscribers/`)

| Subscriber | Path | Purpose |
| --- | --- | --- |
| openlineage | `subscribers/openlineage/` | Polls `rimsky_lineage` and emits OpenLineage 1.x events. |

### Dashboards (`dashboards/`)

| Dashboard | Path | Purpose |
| --- | --- | --- |
| rimsky-dashboard | `dashboards/rimsky-dashboard/` | Operator dashboard (TypeScript / Vite). |

### MCP servers (`mcp-servers/`)

| Server | Path | Depends on | Purpose |
| --- | --- | --- | --- |
| control-api | `mcp-servers/control-api/` (separate Go module + `cmd/rimsky-mcp-control-api/`) | control/controlapi (HTTP) | MCP bridge over the control-api HTTP surface; deployable binary that translates MCP tool calls into REST calls so MCP-aware clients can drive templates / instances / publisher-subscriptions / observability. |

## Shared infrastructure

| Feature | Path | Depends on | Purpose |
| --- | --- | --- | --- |
| conformance | `conformance/` | runtime, protocols, foundation | Shared scenario package (10+ scenarios under `conformance/scenarios/`) imported by the conformance binaries under `cmd/rimsky-*-conformance/`; runs probe-driven invariant checks against external `Executor` / `ClaimProducer` / `Publisher` / `DataProcessing` / `Validation` / `BlobBackend` impls. |
| internal/pgtest | `internal/pgtest/` | (test-only, testcontainers) | Root-module pgtest fixture (parallel to `foundation/internal/pgtest/`) used by graph / runtime / control / subscribers tests; spins up a per-test Postgres container with rimsky migrations applied. |

## Reference impls (examples)

| Reference impl | Path | Purpose |
| --- | --- | --- |
| atomic-staging-fs-producer | `examples/atomic-staging-fs-producer/` | Reference `ClaimProducer` impl wiring the atomic-staging pattern (`asset` lifetime: `durable`) over a filesystem backing store. Bundles `cmd/`, `server/`, `store/`, `sweep/`, and a sample `template.yaml`. |

## Test harnesses (`test/`)

| Suite | Path | Depends on | Purpose |
| --- | --- | --- | --- |
| scenarios | `test/scenarios/` | full stack | End-to-end scenario tests pinning the @blessed-invariant set (locks, stores, claim_stores, frame_resolution, lifecycle, asset). Each scenario boots the supervisor + producer-services against testcontainers Postgres. |
| smoke | `test/smoke/` | full stack | Reference smoke fixture (boots producer-services on ephemeral ports, drives a sustained dispatch loop) used to harden new invariant coverage. |
