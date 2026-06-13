# feature-index

Per-feature index for fresh-session onboarding. Each entry names a feature's
canonical directory + a one-line purpose + the layer it sits in + the
features it depends on. The repo root holds four top-level code
directories: `cmd/` (binaries), `lib/` (shippable library code), `test/`
(out-of-tree tests + their machinery), `tools/` (dev tooling). Layer
ordering inside `lib/` is foundation → graph → runtime → control, then
`cmd/` consumes them; test scaffolding (stores / executors / scenario
harness) lives under `test/support/`. Keep entries terse; deep design
lives in `.ok-planner/design/concepts/`.

## Foundation layer (`lib/foundation/`)

| Feature | Path | Depends on | Purpose |
| --- | --- | --- | --- |
| auth | `lib/foundation/auth/` | shared | API-key plaintext format (mint / hash / validate); grant entry parser + wildcard matcher + permission check; identity + audit payload types. Pure functions; no I/O. |
| cascade | `lib/foundation/cascade/` | shared | Node-state machine (`NodeState`, `TransitionReason`, `ErrIllegalTransition`); illegal-transition rejection. |
| signal | `lib/foundation/signal/` | shared | Canonical signal taxonomy (`TypePath`, payload schemas, `ValidateTypePath` / `ValidateSubscriptionType`) + CEL `when:` predicate compilation; thin audit subpackage (`lib/foundation/signal/audit/`) writes one `rimsky_events` row per emitted signal. |
| locks | `lib/foundation/locks/` | shared | `ClaimProducer` protocol Go interface + claim/lock primitive types (`ClaimID`, `ClaimSpec`, `NamedLockSpec`, `ClaimResult`, `Registry`); in-Go `storetest` fake. |
| persistence | `lib/foundation/persistence/` | shared, spec, cascade, locks | Database / Tables / Tx interfaces plus the postgres + sqlite drivers; migration runner; advisory-lock primitives; blob backends. |
| persistence/conformance | `lib/foundation/persistence/conformance/` | persistence | Driver-agnostic conformance suite for `Tables` impls (postgres + sqlite). |
| persistence/internal | `lib/foundation/persistence/internal/` | persistence | Package-private helpers; depguard `foundation-internal-isolation` blocks external imports. |
| internal/pgtest | `lib/foundation/internal/pgtest/` | (test-only) | Testcontainers-based postgres fixture used inside foundation. |
| shared | `lib/foundation/shared/` | (stdlib) | Infrastructure types: `Clock`, `Logger`, `UUID`, `DeepMergeJSON`, error helpers, `SilentLogger`. |
| spec | `lib/foundation/spec/` | (stdlib) | Persistable row-type primitives: `TemplateSpec`, `TemplateNodeDef`, `EvaluatorState`, `ErrorTypePolicy`, frame-resolution constants, claim-handle state enum, severities, backoff kinds. Pure data; no algorithms. |

## Protocols layer (`lib/protocols/`)

| Feature | Path | Depends on | Purpose |
| --- | --- | --- | --- |
| protocols | `lib/protocols/` (separate Go module) | (grpc, protobuf) | The three service-protocol Go interfaces (`ClaimProducer`, `Executor`, `LifecycleSubscriber`) + `.proto` sources + generated gRPC bindings. |

## Graph layer (`lib/graph/`)

| Feature | Path | Depends on | Purpose |
| --- | --- | --- | --- |
| attribute | `lib/graph/attribute/` | spec, locks, foundation/shared | Substitution layer (`{{...}}` directive resolution against deps / claim / params / trigger / child contexts) + schema validation + callback wiring. |
| frame | `lib/graph/frame/` | persistence, spec, cascade | Frame-resolution model: queued/serial/coalesce frame enqueue, per-instance frame-delivery mode, advance/start transitions. |
| node | `lib/graph/node/` | spec, cascade, locks | Template + node algorithms over `lib/foundation/spec` row types: `Evaluate`, `HoldingSubgraphsForTemplate`, `ValidateTemplate`, `RequiredStores`; subscription-edge inverse map; substitution-ref parsing; inheritance + co-holdership topology. |
| scheduler | `lib/graph/scheduler/` | persistence, frame, node, cascade, runtime (per-file exemption) | Scheduler-tick orchestration: advisory-lock guarded tick, pure-cascade fan-out, scope-substitution-resolution sweep, sweep coordination (the actual sweeps live in `lib/runtime/`). |
| shared | `lib/graph/shared/` | spec | Graph-specific aliases / enums (e.g. `AccessKind`, `MessageType`); re-exports of `Severity`/`BackoffKind`/`JitterKind` from `lib/foundation/spec`. |
| template/canonical | `lib/graph/template/canonical/` | spec | RFC 8785 JCS-canonicalized template-spec hashing (`CanonicalSpecHash`). |

## Runtime layer (`lib/runtime/`)

| Feature | Path | Depends on | Purpose |
| --- | --- | --- | --- |
| supervisor / runner | `lib/runtime/runner*.go`, `lib/runtime/supervisor.go` | foundation, graph, protocols | Per-supervisor acquisition loop: candidate selection, atomic claim acquisition tx, dispatch to executor pool, terminal event handling, error-type policy routing. |
| auto-terminal | `lib/runtime/auto_terminal*.go`, `lib/runtime/terminal_decision*.go` | persistence, cascade, locks | Held-claim aggregate-outcome resolution (Commit vs Abandon vs force-cancel); unified terminal-decision engine `ResolveClaimHandleTerminal`; sibling-cancel walk; descendant-cancel walk; recursive parent-claim chain. |
| sub-graph dispatch | `lib/runtime/subgraph_dispatch.go`, `lib/runtime/fanout_dispatch.go` | graph/node, persistence | Sub-graph caller absorption (entry-node shared identity), fan-out child dispatch (per `partition_key`), state propagation up the run tree. |
| sweeps | `lib/runtime/sweep_*.go`, `lib/runtime/conductor.go` | persistence | Scheduler-driven sweeps: orphan claim reaping, stale heartbeats, orphaned node-runs, parked nodes ready-for-resume, parked-overdue (timeout to failed), claim-handle retention (committed/abandoned cutoff). |
| run-tree state propagation | `lib/runtime/state_propagation.go`, `lib/runtime/run_tree.go` | persistence, cascade | Child→parent aggregation policies (`strict` / `min_quorum` / etc.); leaf-run terminal → parent-run state aggregation. |
| lineage writer | `lib/runtime/lineage_writer.go` | persistence, graph/node | Append-only emit path for `leaf_run` + `claim_terminal` lineage rows; hash helpers; SubstitutionRef collection. |
| messages + publishers | `lib/runtime/message_delivery.go`, `lib/runtime/publishers.go`, `lib/runtime/backfill.go` | persistence | Frame-boundary message delivery; publisher-subscription desired-state lifecycle (mounting rows at instance-create, the retry-forever Subscribe reconciler, Unsubscribe teardown, startup resync sweep); backfill operation orchestration. |
| peer | `lib/runtime/peer/` | protocols | The only concrete gRPC `ClaimProducer` / `LifecycleSubscriber` / `Publisher` / `Validation` / `DataProcessing` client. Renamed from `lib/runtime/remote/` in spec `2026-05-24-repo-reorganization-design` phase P2 — "peer" matches the `concept:service` vocabulary better. Also owns the `x-rimsky-service-name` client-side gRPC interceptor (`WithServiceName` + unary/stream interceptors in `service_name_interceptor.go`) installed on the executor + claim-producer dials for host-agent-proxy routing, and `ProducerCallError` (`errors.go`) translating gRPC `ErrorInfo.Reason` into the rimsky error_class. |
| executor pool | `lib/runtime/executor/` | protocols, peer | Executor gRPC client pool; installs `peer`'s `x-rimsky-service-name` interceptor on its dial. |
| host-agent daemon | `lib/runtime/hostagent/` | protocols | Importable dev-machine daemon main loop (`@concept: host-agent`), the agent end of `HostAgent.Connect`. Dials the proxy with reconnect-backoff (`run.go`), binds a local HTTP forward listener (`local_http.go`), validates+exec()s spawned binaries (injecting `RIMSKY_AGENT_PORT`), runs the Capabilities handshake, and reaps (`spawn.go`), tunnels executor server-stream + claim-producer unary dispatch to the child (`dispatch.go`). Run by the `cmd/rimsky-host-agent` binary and the `rimsky agent` CLI subcommand. |

## Control layer (`lib/control/`)

| Feature | Path | Depends on | Purpose |
| --- | --- | --- | --- |
| controlapi | `lib/control/controlapi/` | runtime, persistence, foundation | HTTP control plane: templates, instances, nodes, lineage, assets, backfills, publisher-subscriptions, messages, observability, admin diagnostics, auth (API keys + permissions + audit), MCP-as-skin at `POST /mcp`. Fires lifecycle events synchronously at state transitions. |
| controlapi/mcp | `lib/control/controlapi/mcp/` | controlapi, foundation/auth | JSON-RPC 2.0 envelope + tool catalog for the MCP protocol skin; dispatches tools back into the chi router in-process so the same auth gate runs. |
| cli | `cmd/rimsky/cli/` | controlapi | `rimsky` thin client + `compose` workflow (template-tag-prefixed multi-instance dispatch); carries Bearer token via `--key` or `RIMSKY_API_KEY`. `run.go` adds the late-bind flags `--template`/`--param k=v`/`--service <name>=<path>` (+ auto-start-agent on `--service` via PID-existence check) and `service_bindings` on `POST /instances`; `auth_login.go` implements interactive `auth login` (writes the per-context `api_key` extended onto `config.go::Context`); `aliases.go` resolves bare `--service` names via `~/.rimsky/aliases.yml` (global) + `.rimsky/aliases.yml` (project-local). `agent.go` runs the bundled `concept:host-agent` daemon. |
| observability | `lib/control/observability/` | persistence, runtime | Diagnostics-cache, cascade-graph endpoint, metrics, lock-holder browse, node-run browse. |
| config | `lib/control/config/` | foundation, runtime, graph, persistence | Unified `rimsky.yml` parsing + validation + per-process handle assembly. |

## Reference binaries (`cmd/`)

| Binary | Path | Purpose |
| --- | --- | --- |
| rimsky-scheduler | `cmd/rimsky-scheduler/` | Scheduler-tick process. |
| rimsky-supervisor | `cmd/rimsky-supervisor/` | Supervisor (runner + callback server). |
| rimsky-control-api | `cmd/rimsky-control-api/` | Control-plane HTTP server. |
| rimsky-migrate | `cmd/rimsky-migrate/` | Migration runner. |
| rimsky-entrypoint | `cmd/rimsky-entrypoint/` | Unified PID-1 (the `rimsky` image). |
| rimsky | `cmd/rimsky/` | Thin client CLI. The verb dispatcher lives in `cmd/rimsky/main.go`; all verb handlers (including `auth init|login|create-key|list|show|revoke|rotate|status`, the `agent start|status|stop` host-agent group, the bundled role JSONs at `cmd/rimsky/cli/roles/`, and the `conformance <protocol>` subcommands — `executor`, `claim-producer`, `data-processing`, `validation`, `publisher`, `blob-backend`, `probe`) live in `cmd/rimsky/cli/` and `cmd/rimsky/conformance*.go`. |
| rimsky-host-agent-proxy | `cmd/rimsky-host-agent-proxy/` | Late-bound dev-machine service proxy (`@concept: host-agent-proxy`). Serves the agent-facing `HostAgent.Connect` bidi stream + the supervisor-facing `Executor`/`ClaimProducer`(+observability)/`LifecycleSubscriber`(consumer role) protocols on one gRPC port; `Publisher`/`Validation`/`DataProcessing` are likewise real transparent-forwarding handlers (no fronted protocol ships as an Unimplemented stub). Resolves a dispatch to an owner's connected agent, lazily spawns the named binding, rewrites callbacks onto the agent's local listener, and reaps on run-scope-terminal. |
| rimsky-host-agent | `cmd/rimsky-host-agent/` | Dev-machine daemon (`@concept: host-agent`), the agent end of `HostAgent.Connect`. Thin signal-handled wrapper over the importable `lib/runtime/hostagent` main loop; also linked as the `rimsky agent` CLI subcommand (`cmd/rimsky/cli/agent.go`). Dials the proxy outbound, exec()s local binaries (injecting `RIMSKY_AGENT_PORT`), runs the Capabilities handshake, tunnels gRPC dispatch + local HTTP callbacks back through the stream, and reaps children on `Reap`/stream-close. |

Docs build tooling is not part of this repo. Rimsky carries no docs
tooling and no docs gate.

## Dev tooling (`tools/`)

| Tool | Path | Purpose |
| --- | --- | --- |
| license-check | `tools/license-check/` | License-boundary lint + header stamp (reads `licensing.yml`). Run via `make license-lint` / `make license-stamp`. |

## Bundled services (`lib/services/`)

Consumption-side services shipped as images. Their own Go module
(`github.com/rimsky-ai/rimsky-core/lib/services`); depend only on
`lib/protocols`; never imported back into core internals (enforced
by the module graph and the `consumption-side-isolation` lint rule).
Each has a co-located Dockerfile; `make service-images` builds them
all.

| Service | Path | Purpose |
| --- | --- | --- |
| store-filesystem | `lib/services/stores/filesystem/` | Filesystem-backed claim producer (atomic stage-then-swap). |
| store-postgres | `lib/services/stores/postgres/` | Postgres-backed claim producer. |
| sensor-cron | `lib/services/sensors/sensor-cron/` | Cron-schedule sensor: emits messages on a cron firing. |
| sensor-http | `lib/services/sensors/sensor-http/` | HTTP-poll sensor: polls a URL and emits on changed body / status. |
| sensor-object-store | `lib/services/sensors/sensor-object-store/` | Object-store sensor: emits on new/changed objects under a prefix. |
| sensor-webhook | `lib/services/sensors/sensor-webhook/` | Webhook sensor: receives inbound HTTP and emits a message. |
| subscriber-openlineage | `lib/services/subscribers/openlineage/` | OpenLineage lifecycle subscriber: persists lineage events to an OL backend. |
| executor-http-node | `lib/services/executors/http-node/` | HTTP-call executor: dispatches a node by issuing a configured HTTP request. |
| executor-verifier-http | `lib/services/executors/verifier-http/` | Verifier executor: validates a claim's data via an HTTP probe. |
| executor-verifier-shape-checks | `lib/services/executors/verifier-shape-checks/` | Verifier executor: structural shape checks on a claim's data. |
| executor-claude-agent | `lib/services/executors/claude-agent/` | TypeScript executor (separate Apache deliverable; consumes `lib/protocols` as a local npm `file:` dep). |

## Bundled service test-infra carve-outs

Test-infrastructure carve-outs and in-rimsky testfixture wrappers
that stay in the root module (separate from the production-side
services under `lib/services/`).

### Claim producers (`test/support/stores/`)

| Producer | Path | Purpose |
| --- | --- | --- |
| stub | `test/support/stores/stub/` | In-memory test fixture. Stays in rimsky as test infrastructure. |
| filesystem/testfixture | `test/support/stores/filesystem/testfixture/` | Test-fixture wrapper around `test/support/stores/stub`; preserves the public `Start` + `Config` surface for in-rimsky scenario tests. |
| postgres/testfixture | `test/support/stores/postgres/testfixture/` | Test-fixture wrapper around `test/support/stores/stub`; preserves the public surface for in-rimsky scenario tests. |

### Executors (`test/support/executors/`)

| Executor | Path | Purpose |
| --- | --- | --- |
| stub | `test/support/executors/stub/` | In-memory test fixture (Go). Stays in rimsky as test infrastructure. |

### Dashboards

No dashboard reference impls are part of this repo.

## Shared infrastructure

| Feature | Path | Depends on | Purpose |
| --- | --- | --- | --- |
| conformance | `lib/protocols/conformance/` | protocols | Shared conformance library imported by the `rimsky conformance <protocol>` CLI subcommands (`cmd/rimsky/conformance*.go`); runs probe-driven invariant checks against external `Executor` / `ClaimProducer` / `Publisher` / `DataProcessing` / `Validation` / `BlobBackend` impls. Includes `lib/protocols/conformance/claimproducer/` — the importable `ClaimProducer` conformance suite (`Run`), shared with the `rimsky conformance claim-producer` subcommand and reused by scenario tests that want to assert a producer endpoint passes the standard suite. |
| pgmigrate | `test/support/pgmigrate/` | (test-only, testcontainers) | Root-module pgtest fixture (parallel to `lib/foundation/internal/pgtest/`) used by graph / runtime / control / scenario tests; spins up a per-test Postgres container with rimsky migrations applied. Typically imported under the `pgtest` alias. |

## Reference impls (examples)

The `examples/` directory is a standalone Apache-2.0 Go module of minimal copy-and-modify protocol reference servers (it depends only on `lib/protocols` plus stdlib and permissive third-party packages). See `examples/README.md` for the per-protocol guarantees.

| Example | Path | Protocol |
| --- | --- | --- |
| executor | `examples/executor/` | `Executor` (+ `ExecutorObservability` handshake) |
| claimproducer | `examples/claimproducer/` | `ClaimProducer` (read-only) |
| atomic-staging-fs-producer | `examples/atomic-staging-fs-producer/` | `ClaimProducer` (staged-write over a POSIX filesystem) |
| lifecyclesubscriber | `examples/lifecyclesubscriber/` | `LifecycleSubscriber` |
| publisher | `examples/publisher/` | `Publisher` (in-memory subscriptions) |
| validation | `examples/validation/` | `Validation` (registration-time mix-in) |
| data-processing | `examples/data-processing/` | `DataProcessing` (fan-out candidate lifecycle) |
| compose | `examples/compose/` | `rimsky compose` manifest (not a protocol server) |

## Test harnesses (`test/`)

| Suite | Path | Depends on | Purpose |
| --- | --- | --- | --- |
| scenarios | `test/scenarios/` | full stack | End-to-end scenario tests pinning the @blessed-invariant set (locks, stores, claim_stores, frame_resolution, lifecycle, asset). Each scenario boots the supervisor + producer-services against testcontainers Postgres. |
| smoke | `test/smoke/` | full stack | Reference smoke fixture (boots producer-services on ephemeral ports, drives a sustained dispatch loop) used to harden new invariant coverage. |
| support/scenario | `test/support/scenario/` | full stack | End-to-end scenario test harness (boots scheduler + supervisor + control-api against testcontainers Postgres). Moved out of the graph layer in the 2026-05-27 root-folder reorg. |
| support/testpg | `test/support/testpg/` | (test-only, testcontainers) | Postgres-testcontainer helper. Demoted from a standalone Go module to a plain package under `test/support/` in the 2026-05-27 reorg; consumed by `test/support/pgmigrate`. |
| support/eventwait | `test/support/eventwait/` | foundation/persistence | Event-ledger test waits (`@agent-contract`): `WaitForEvent` blocks until a matcher is satisfied over the append-only `rimsky_events` ledger (or fails fatally with a scope dump); `Events` is the non-blocking read used for absence assertions over the same durable record. |
