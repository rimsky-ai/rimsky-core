# Cross-cutting gotchas — discovery sketch

**Date:** 2026-06-22
**Status:** Sketch (discovery only; investigation findings for a future `/brainstorm` → spec → `/execute-plan` cycle; no changes made beyond the deletes already merged in the same audit pass)

## Context

CLAUDE.md carried a "Cross-cutting gotchas" section listing nine items framed as "they don't have a natural concept-doc home and would trip a fresh session." An audit pass categorized those nine:

- **3 were duplicates of existing concept content.** The async-callback body shape duplicated `concept:executor`; the universal `Idempotency-Key` requirement duplicated `concept:message`; the dropped `POST /sensors/{watch_id}/observations` route was pre-v1 migration cruft (CLAUDE.md noted the v0→v1 incompatibility, but pre-v1 we don't carry compat notes). Deleted from CLAUDE.md.
- **1 was a code-local explanation.** SQLite driver pool size of 8. Moved to the code site: the constant was renamed `sqliteUnifiedStackMaxOpenConns` (the name encodes the purpose) and `code:lib/foundation/persistence/sqlite/database_test.go::TestSQLiteUnifiedStackMaxOpenConnsAvoidsBeginStarvation` fails if it drops below 2 (the floor is the actual correctness invariant; 8 is the tuned default). CLAUDE.md entry deleted.
- **2 are code defects deferred for a separate pass.** The services-integration-harness needing `make core-images`/`make service-images` to be run before `make test-all`; the gRPC test-client `Struct` camelCase footgun in the claude-agent executor (`code:lib/services/executors/claude-agent/src/server.ts::jsToProtoStruct` silently drops nested values on the wire). Left in CLAUDE.md pending a dedicated fix — see "Out of scope" below.
- **3 needed deeper investigation** to determine whether they map cleanly to latent concepts, are remnants/defects, or both. This sketch captures the discovery for those three.

The framing the project rules use: *every code path has an implicit concept, story, and decision*. Writing them down is discovery, not creation. Where no coherent concept emerges, the gotcha is a defect or remnant to fix.

All three of the investigated gotchas turned out to be **both** — a real latent concept worth writing down, plus implementation defects worth fixing alongside the doc-write. The recommended sequencing is **fix-then-write**, so the concept doc describes the cleaned-up code rather than locking in the current defect as design.

---

## 1. `RIMSKY_AGENT_PORT` — spawn contract honored only by test stubs

### Latent concept

A **spawn-contract** between a parent that launches a child binary as a late-bound service and the child itself: the parent picks a free port, sets it as `RIMSKY_AGENT_PORT` in the child env, and poll-dials `127.0.0.1:<port>` until the child binds. No port-handshake-back — the child must bind the spawner-picked port, or the parent reaps it.

### Implementation reality

The contract is documented in CLAUDE.md and implemented in the shared spawn primitive (`code:lib/runtime/hostagent/spawn.go::SpawnService`, the shared seam between the host-agent daemon and `rimsky compose run --service`), but **zero bundled production service binaries actually read `RIMSKY_AGENT_PORT`**. Each uses its own env var:

- `code:lib/services/sensors/sensor-cron/main.go#31` reads `RIMSKY_SENSOR_CRON_PORT` (default 9081)
- `code:lib/services/executors/claude-agent/src/main.ts#25` reads `RIMSKY_EXECUTOR_PORT_GRPC` (default 9090)
- `code:lib/services/executors/http-node/main.go#35` uses its own config

The only readers are test stubs: `code:lib/runtime/hostagent/testdata/stubchild/main.go#32`, `code:lib/runtime/hostagent/testdata/stub-service/main.go#20`, `code:cmd/rimsky/cli/compose/testdata/stub-executor/main.go#97`. If an operator runs `rimsky run --service sensor-cron=./sensor-cron`, the host-agent picks (say) port 53117, sensor-cron sees the env var but ignores it and binds 9081, the readiness poll on 53117 times out after 30s, and the dispatch returns `spawn_failed` with no useful diagnostic for the operator.

### Proposed concept / story / decision shape

- **`concept:spawn-contract`** — the env-var-and-poll handshake between spawner and spawned-child, with the binding port as the only coupling point and the TCP readiness poll as the synchronization point. Adjacent: `concept:host-agent`, `concept:host-agent-proxy`, `concept:service`.
- **`story:spawn-bundled-binary-via-cli`** — an operator binds a service name to a local binary path via `rimsky run --service <name>=<path>`; the binary comes up bound to a port the spawner picks, no manual port config required.
- **`decision:no-port-handshake-back`** — children bind the spawner-picked port instead of reporting back, trading flexibility for simplicity and fail-fast behavior. Rationale: keeps the spawn path proto-free and gives the spawner a single mechanical signal (port-bound by deadline) for "child is alive."

### Defects to fix before / alongside writing the docs

- **Make production binaries honor the contract.** Each production service binary checks `RIMSKY_AGENT_PORT` first and falls back to its own env var if unset. One if-statement per binary. After this, the readiness poll is mechanical enforcement of the contract — the concept doc describes what the code provably does, not aspiration.
- **Rename `RIMSKY_AGENT_PORT` → `RIMSKY_BIND_PORT`** (or similar). The "agent" framing was honest when only the host-agent daemon spawned; `compose run` is now a co-spawner and the name reads as agent-internal.
- **`compose run` skips the post-readiness gRPC handshake** that the daemon does via `code:lib/runtime/hostagent/spawn.go::handshakeCapabilities`. A non-gRPC squatter on the picked port passes the TCP-only readiness poll and poisons the manifest overlay. Add the handshake to `code:cmd/rimsky/cli/compose/run.go#395-414`.
- **`code:lib/runtime/hostagent/spawn.go::FreeLocalPort#319-327` has a TOCTOU window** — it closes the listening socket before the child binds. Under load another process can grab the port; the child's bind then fails, and the readiness poll either times out or hits the squatter. At minimum surface as a tension; ideally pass the listener fd to the child (fd-inheritance) or accept a small retry loop.
- **`compose run` hardcodes a 30s ready timeout** (`code:cmd/rimsky/cli/compose/run.go#398`); the daemon path honors `Spawn.ReadyTimeoutSeconds` from the wire frame. Make `compose run` configurable too (CLI flag or per-binding manifest field).
- **`stub-no-bind` fixture is referenced by `code:lib/runtime/hostagent/spawn_helper_test.go#90` but may not exist** in `testdata/`. Verify or fix the test.

---

## 2. Supervisor callback hostname — `concept:callback-advertise`

### Latent concept

The **externally-reachable URL** that executors dial to settle async-callback dispatches, distinct from the supervisor's bind address. The two are deliberately decoupled because container/k8s topologies routinely have bind ≠ advertise (bind `0.0.0.0:9100`, advertise `svc-name.ns:443` behind an ingress, etc).

### Implementation reality

The flow is clean and grounded: YAML/env → `SupervisorConfig.CallbackAdvertiseHost`/`Port` → `runtime.Config` → `code:lib/runtime/supervisor.go::advertisedCallbackURL` → `Handle.advertisedURL` → `RunArgs.CallbackURL` → proto `CallbackUrl` field consumed by executors. Env wins over YAML on each scalar independently (`code:lib/control/launch/supervisor.go#103-111`).

### Proposed concept / story / decision shape

- **`concept:callback-advertise`** — the externally-reachable URL stamped into `ExecuteRequest.callback_url` for executors that return `AwaitAsyncCallback`. Invariant: the URL must be reachable from where executors live; the supervisor's bind address is not in general a reachable URL.
- **`story:async-callback-settles-from-anywhere`** — an executor that returns `AwaitAsyncCallback` from any network location can POST to the callback URL later to settle the dispatch, without coordinating its return location with the supervisor.
- **`decision:bind-vs-advertise-separation`** — bind defaults to `0.0.0.0` (accept from anywhere) while advertise is operator-configured (express the externally-reachable identity). Operational topology requires the split; the supervisor cannot infer its own externally-reachable URL from a wildcard bind.

### Defects to fix before / alongside the docs

- **Silent broken URLs.** When `advertise_host` is empty and bind is wildcard, the supervisor stamps `http://0.0.0.0:9100` into ExecuteRequest and emits a Warn log (`code:lib/control/launch/supervisor.go#127`). Operators discover it as `TypeError: fetch failed` in executor logs. Fail-fast at startup when advertise is empty AND bind is wildcard.
- **Modeled as host+port instead of full URL.** The proto field is named `callback_url` and IS a URL — the YAML schema models it as four separate scalars (`host`, `port`, `advertise_host`, `advertise_port`) with `http://` hardcoded at `code:lib/runtime/supervisor.go#281`. No TLS path. No way to express a reverse-proxy path prefix. Replace with a single `callback.advertise_base_url` field (scheme + host + port + optional path prefix); the four scalars collapse into one.
- **Conformance vs production inconsistency.** The conformance receiver does a `0.0.0.0` → `127.0.0.1` loopback rewrite (`code:lib/protocols/conformance/executor/callback_receiver.go#44-50`); production has no equivalent. Pick one model and use it everywhere — production should either fail-fast or fall back to loopback, not Warn-and-ship-broken.
- **Verify an older bug-sketch is stale.** `file:.ok-planner/history/sketches/rimsky-bugs.md#23-29` flagged "persisted row uses bind values, not advertise values." The current diff calls `effectiveCallbackHostPort` on both the persistence and the dispatch paths, so the two should now agree — but confirm before treating the sketch as obsolete.

---

## 3. Entrypoint role + migrate — `concept:launch-topology`

### Latent concept

A **launch-topology** axis: unified (one process hosts all three roles — scheduler + supervisor + control-api — sharing one persistence driver and in-process state) vs split (each role is its own process, sharing only the database). The memory blob backend's validity is conditioned on the unified topology because the backend is a Go map that can't span processes.

### Implementation reality

`code:cmd/rimsky-entrypoint/main.go::runUnified#118-150` genuinely opens one driver via `launch.OpenDriverFromEnv` and threads it into all three role runners — "one process, one persistence layer" is true. `code:cmd/rimsky-entrypoint/main.go::runSingleRole#152-172` execs the role binary as a child. Migrate ownership is decided by `code:cmd/rimsky-entrypoint/main.go::shouldMigrate#44-58`: all-in-one always migrates; single-role only migrates when the role is `rimsky-control-api`; override via `RIMSKY_ENTRYPOINT_MIGRATE`. The three-container split migrates exactly once (asserted at `code:cmd/rimsky-entrypoint/main_test.go#347-362`).

### Proposed concept / story / decision shape

- **`concept:launch-topology`** — the unified-vs-split axis with the memory-blob-backend invariant. Adjacent: `concept:rimsky`, `concept:persistence-database`, `concept:blob-backend`.
- **`story:single-binary-all-in-one-deploy`** — operator runs the `rimsky-all-in-one` image with no command and gets a working stack with no infrastructure (the no-arg `rimsky-entrypoint` path).
- **`story:per-role-container-split`** — operator runs three containers (scheduler, supervisor, control-api) for production deploys, sharing only the database; migrations run exactly once.
- **`decision:unified-process-shares-persistence-driver`** — why all-in-one opens one driver instead of three: shared in-process state (memory blob map, prepared statement caches, advisory-lock state) only works when the roles share a process.
- **`decision:migrate-ownership-by-launcher`** — the migration-ownership rule (the launcher decides, based on topology, with the `RIMSKY_ENTRYPOINT_MIGRATE=1` init-container as the explicit escape hatch). Rationale: someone has to own it; an init-container is the production-clean topology, and the in-binary owner is a fallback for the no-init-container case.

### Defects to fix before writing the docs

- **`RIMSKY_PROCESS_ROLE` as the seam is the core defect.** A stringly-typed env var set by one specific binary that the persistence validator reads to gate the memory blob backend (`code:lib/foundation/persistence/blob_config.go#67-69`). Diagnostic: `code:cmd/rimsky/cli/compose/run.go#151` needed an `envMutex` snapshot/restore dance to set+unset the var around `OpenDriverFromEnv` because env is the only seam. Replace with a typed `Topology: Unified | Split` parameter on `BlobConfig` / `Driver.Open`; every launcher passes it explicitly. The `envWithoutProcessRole` scrub at `code:cmd/rimsky-entrypoint/main.go#218-228` goes away when nothing sets it.
- **Migrate-ownership is a literal string-match on `"rimsky-control-api"`** (`code:cmd/rimsky-entrypoint/main.go#57`). Rename the role and migrations silently break or double-run. The choice of control-api as migrator was arbitrary — control-api isn't semantically the schema owner; the init-container path is the clean topology. Make ownership a typed field on the launcher's plan instead of a string-match.
- **Asymmetric topology gate.** Memory-blob is a hard gate; SQLite + replicas>1 is only a `slog.Warn` (flagged in an older intent-vs-reality sketch under `.ok-planner/history/sketches/`). If launch-topology becomes a typed invariant, both gates should ride the same machinery.
- **Second, unrelated consumer of `RIMSKY_PROCESS_ROLE`.** `code:lib/control/launch/scheduler.go#171` reads the var to compute metrics-port offsets to avoid in-process port collisions — a separate concern from topology validity. When the env var goes away, that code path needs its own signal (or can compute the offset from `cfg` directly).

---

## Recommended sequencing

The fix-then-write order applies to all three. Recommended order across them:

1. **`RIMSKY_AGENT_PORT` first.** The fix is smallest (one if-statement per production binary, plus the rename, plus adding the handshake to `compose run`) and clears the most urgent honesty problem: the gotcha currently lies about a contract the code doesn't honor. After the fix, the readiness poll is mechanical enforcement — the concept doc describes what the code provably does.

2. **Then either of the other two**, depending on appetite. The callback-advertise fix is medium-sized (introduces the full-URL config, deprecates the four scalar knobs, adds the fail-fast gate, reconciles conformance vs production). The launch-topology fix is the largest (introduces a typed `Topology` parameter that threads through the persistence layer and several launchers, removes the env-var seam in three call sites, retypes migrate-ownership).

A single spec can carry all three or split them — they're independent enough to land separately. The CLAUDE.md gotcha entries come out one at a time as each concept doc lands.

## Out of scope for this sketch

- **The two code defects deferred from the audit pass:** the services-integration-harness `make core-images` dependency, and the gRPC test-client `Struct` camelCase footgun in the claude-agent executor. Both are real code defects, not gotchas-to-concepts. Separate sketch / spec.
- **Retired-tag-vocabulary residue in design docs.** The codebase sweep (commits `f11208f8`, `c300a7af`, `d4f35850`) is clean — no `@blessed-invariant:` / `@source:` / `@constraint:` / `@deliberate:` survivors in source. Bare-number `@blessed-invariant 4`-style references still live in some `.ok-planner/design/` files; those need the spec pipeline because the design docs are involved.
