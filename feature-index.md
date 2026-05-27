# feature-index

Per-feature index for cold-read onboarding. Each entry names a feature's
canonical directory + a one-line purpose + the layer it sits in + the
features it depends on. Layer ordering is foundation → graph → runtime →
control → cmd / stores / executors / sensors / subscribers / dashboards.
Keep entries terse; deep design lives in `.ok-planner/design/concepts/`.

## Foundation layer (`foundation/`)

| Feature | Path | Depends on | Purpose |
| --- | --- | --- | --- |
| auth | `foundation/auth/` | shared | API-key plaintext format (mint / hash / validate); grant entry parser + wildcard matcher + permission check; identity + audit payload types. Pure functions; no I/O. |
| cascade | `foundation/cascade/` | shared | Node-state machine (`NodeState`, `TransitionReason`, `ErrIllegalTransition`); illegal-transition rejection. |
| signal | `foundation/signal/` | shared | Canonical signal taxonomy (`TypePath`, payload schemas, `ValidateTypePath` / `ValidateSubscriptionType`) + CEL `when:` predicate compilation; thin audit subpackage (`foundation/signal/audit/`) writes one `rimsky_events` row per emitted signal. |
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
| peer | `runtime/peer/` | protocols | The only concrete gRPC `ClaimProducer` / `LifecycleSubscriber` / `Publisher` / `Validation` / `DataProcessing` client. Renamed from `runtime/remote/` in spec `2026-05-24-repo-reorganization-design` phase P2 — "peer" matches the `concept:service` vocabulary better. Also owns the `x-rimsky-service-name` client-side gRPC interceptor (`WithServiceName` + unary/stream interceptors in `service_name_interceptor.go`) installed on the executor + claim-producer dials for host-agent-proxy routing, and `ProducerCallError` (`errors.go`) translating gRPC `ErrorInfo.Reason` into the rimsky error_class. |
| executor pool | `runtime/executor/` | protocols, peer | Executor gRPC client pool; installs `peer`'s `x-rimsky-service-name` interceptor on its dial. |
| host-agent daemon | `runtime/hostagent/` | protocols | Importable dev-machine daemon main loop (`@concept: host-agent`), the agent end of `HostAgent.Connect`. Dials the proxy with reconnect-backoff (`run.go`), binds a local HTTP forward listener (`local_http.go`), validates+exec()s spawned binaries (injecting `RIMSKY_AGENT_PORT`), runs the Capabilities handshake, and reaps (`spawn.go`), tunnels executor server-stream + claim-producer unary dispatch to the child (`dispatch.go`). Run by the `cmd/rimsky-host-agent` binary and the `rimsky agent` CLI subcommand. |

## Control layer (`control/`)

| Feature | Path | Depends on | Purpose |
| --- | --- | --- | --- |
| controlapi | `control/controlapi/` | runtime, persistence, foundation | HTTP control plane: templates, instances, nodes, lineage, assets, backfills, publisher-subscriptions, messages, observability, admin diagnostics, auth (API keys + permissions + audit), MCP-as-skin at `POST /mcp`. Fires lifecycle events synchronously at state transitions. |
| controlapi/mcp | `control/controlapi/mcp/` | controlapi, foundation/auth | JSON-RPC 2.0 envelope + tool catalog for the MCP protocol skin; dispatches tools back into the chi router in-process so the same auth gate runs. |
| cli | `control/cli/` | controlapi | `rimsky` thin client + `compose` workflow (template-tag-prefixed multi-instance dispatch); carries Bearer token via `--key` or `RIMSKY_API_KEY`. `run.go` adds the late-bind flags `--template`/`--param k=v`/`--service <name>=<path>` (+ auto-start-agent on `--service` via PID-existence check) and `service_bindings` on `POST /instances`; `auth_login.go` implements interactive `auth login` (writes the per-context `api_key` extended onto `config.go::Context`); `aliases.go` resolves bare `--service` names via `~/.rimsky/aliases.yml` (global) + `.rimsky/aliases.yml` (project-local). `agent.go` runs the bundled `concept:host-agent` daemon. |
| observability | `control/observability/` | persistence, runtime | Diagnostics-cache, cascade-graph endpoint, metrics, lock-holder browse, node-run browse. |
| config | `control/config/` | foundation, runtime, graph, persistence | Unified `rimsky.yml` parsing + validation + per-process handle assembly. |

## Reference binaries (`cmd/`)

| Binary | Path | Purpose |
| --- | --- | --- |
| rimsky-scheduler | `cmd/rimsky-scheduler/` | Scheduler-tick process. |
| rimsky-supervisor | `cmd/rimsky-supervisor/` | Supervisor (runner + callback server). |
| rimsky-control-api | `cmd/rimsky-control-api/` | Control-plane HTTP server. |
| rimsky-migrate | `cmd/rimsky-migrate/` | Migration runner. |
| rimsky-entrypoint | `cmd/rimsky-entrypoint/` | Unified PID-1 (the `rimsky` image). |
| rimsky | `cmd/rimsky/` | Thin client CLI (renamed from `rimsky-cli` per the 2026-05-15 control-plane MCP and auth spec). The verb dispatcher lives in `cmd/rimsky/main.go`; all verb handlers (including `auth init|login|create-key|list|show|revoke|rotate|status`, the `agent start|status|stop` host-agent group, and the bundled role JSONs at `control/cli/roles/`) live in `control/cli/`. |
| rimsky-executor-conformance | `cmd/rimsky-executor-conformance/` | Probe-based conformance harness for `Executor` impls. |
| rimsky-claim-producer-conformance | `cmd/rimsky-claim-producer-conformance/` | Conformance harness for `ClaimProducer` impls. |
| rimsky-data-processing-conformance | `cmd/rimsky-data-processing-conformance/` | Conformance harness for `DataProcessing` impls. |
| rimsky-validation-conformance | `cmd/rimsky-validation-conformance/` | Conformance harness for `Validation` impls. |
| rimsky-publisher-conformance | `cmd/rimsky-publisher-conformance/` | Conformance harness for `Publisher` impls. |
| rimsky-blob-backend-conformance | `cmd/rimsky-blob-backend-conformance/` | Conformance harness for `BlobBackend` impls. |
| rimsky-conformance-probe | `cmd/rimsky-conformance-probe/` | Stub-mode probe used by executor-conformance startup. |
| rimsky-license-check | `cmd/rimsky-license-check/` | License-boundary lint + header stamp. |
| rimsky-host-agent-proxy | `cmd/rimsky-host-agent-proxy/` | Late-bound dev-machine service proxy (`@concept: host-agent-proxy`). Serves the agent-facing `HostAgent.Connect` bidi stream + the supervisor-facing `Executor`/`ClaimProducer`(+observability)/`LifecycleSubscriber`(consumer role) protocols on one gRPC port; `Publisher`/`Validation`/`DataProcessing` registered UNIMPLEMENTED. Resolves a dispatch to an owner's connected agent, lazily spawns the named binding, rewrites callbacks onto the agent's local listener, and reaps on run-scope-terminal. |
| rimsky-host-agent | `cmd/rimsky-host-agent/` | Dev-machine daemon (`@concept: host-agent`), the agent end of `HostAgent.Connect`. Thin signal-handled wrapper over the importable `runtime/hostagent` main loop; also linked as the `rimsky agent` CLI subcommand (`control/cli/agent.go`). Dials the proxy outbound, exec()s local binaries (injecting `RIMSKY_AGENT_PORT`), runs the Capabilities handshake, tunnels gRPC dispatch + local HTTP callbacks back through the stream, and reaps children on `Reap`/stream-close. |

Docs build tooling is not part of this repo. Rimsky carries no docs
tooling and no docs gate.

## Bundled service reference impls

Production-side bundled implementations are not part of this repo. Only
test-infrastructure carve-outs and the in-rimsky testfixture wrappers
remain here.

### Claim producers (`stores/`)

| Producer | Path | Purpose |
| --- | --- | --- |
| stub | `stores/stub/` | In-memory test fixture. Stays in rimsky as test infrastructure. |
| filesystem/testfixture | `stores/filesystem/testfixture/` | Test-fixture wrapper around `stores/stub`; preserves the public `Start` + `Config` surface for in-rimsky scenario tests. |
| postgres/testfixture | `stores/postgres/testfixture/` | Test-fixture wrapper around `stores/stub`; preserves the public surface for in-rimsky scenario tests. |

### Executors (`executors/`)

| Executor | Path | Purpose |
| --- | --- | --- |
| stub | `executors/stub/` | In-memory test fixture (Go). Stays in rimsky as test infrastructure. |

### Sensors

No sensor reference impls are part of this repo.

### Lifecycle subscribers

No subscriber reference impls are part of this repo.

### Dashboards

No dashboard reference impls are part of this repo.

## Shared infrastructure

| Feature | Path | Depends on | Purpose |
| --- | --- | --- | --- |
| conformance | `conformance/` | runtime, protocols, foundation | Shared scenario package (10+ scenarios under `conformance/scenarios/`) imported by the conformance binaries under `cmd/rimsky-*-conformance/`; runs probe-driven invariant checks against external `Executor` / `ClaimProducer` / `Publisher` / `DataProcessing` / `Validation` / `BlobBackend` impls. Includes `conformance/claimproducer/` — the importable `ClaimProducer` conformance suite (`Run`), shared with the `cmd/rimsky-claim-producer-conformance` binary and reused by scenario tests that want to assert a producer endpoint passes the standard suite. |
| internal/pgtest | `internal/pgtest/` | (test-only, testcontainers) | Root-module pgtest fixture (parallel to `foundation/internal/pgtest/`) used by graph / runtime / control / subscribers tests; spins up a per-test Postgres container with rimsky migrations applied. |

## Reference impls (examples)

No example reference impls are part of this repo.

## Test harnesses (`test/`)

| Suite | Path | Depends on | Purpose |
| --- | --- | --- | --- |
| scenarios | `test/scenarios/` | full stack | End-to-end scenario tests pinning the @blessed-invariant set (locks, stores, claim_stores, frame_resolution, lifecycle, asset). Each scenario boots the supervisor + producer-services against testcontainers Postgres. |
| smoke | `test/smoke/` | full stack | Reference smoke fixture (boots producer-services on ephemeral ports, drives a sustained dispatch loop) used to harden new invariant coverage. |
