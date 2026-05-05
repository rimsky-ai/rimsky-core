# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this repo is

Rimsky is a project-agnostic reactive node-graph orchestration platform. The codebase is organized into three Go modules plus a root modeling layer per the layer-crystallization design (`docs/specs/2026-05-04-layer-crystallization-design.md`):

1. **Foundation** — `foundation/` (own Go module: `github.com/fallguy/rimsky/foundation`). Cascade engine, claim/lock primitive types, integration-layer supervisor runner + sweeps, and foundation persistence (Postgres + SQLite drivers). The state machine, scope-byte conflict primitive, atomic acquisition transaction, worker-request lifecycle, orphan reapers, and the unified terminal-decision engine all live here.
2. **Protocols** — `protocols/` (own Go module: `github.com/fallguy/rimsky/protocols`). The three service-protocol Go interfaces (`ClaimProducer`, `Executor`, `LifecycleSubscriber`) plus the `.proto` sources and generated bindings. Stdlib + grpc + protobuf only.
3. **Modeling layer + bundled services** — root module (`github.com/fallguy/rimsky`). Templates, instances, frame-resolution, scheduling, control-api, attributes, quality rules, the rimsky binaries under `cmd/`, the bundled claim-producer reference impls under `stores/`, and the bundled executor reference impls under `executors/`.

Rimsky still ships as three independent long-running processes (`rimsky-scheduler`, `rimsky-supervisor`, `rimsky-control-api`) plus `rimsky-migrate`, `rimsky-conformance`, `rimsky-conformance-probe`, `rimsky-claim-producer-conformance`. The three runtime processes communicate **only through Postgres** — they cannot import each other (enforced by depguard).

**ClaimProducers** (the protocol-layer name; "stores" is the bundled-services-layer colloquial term) are out-of-process standalone binaries that implement the gRPC ClaimProducer interface (`Open / Commit / Abandon / Release` + the `Capabilities()` startup handshake). The reference impls ship `filesystem` (concrete-paths only), `postgres` (regional access + items-table queue semantics implemented store-internally), and `stub` (in-memory test fixture). Rimsky talks to them via `foundation/integration/remote/` — the only concrete `ClaimProducer` implementation in the rimsky module. Auto-terminal at holding-subgraph completion drives held-claim resolution: success → `Commit`; failure → `Abandon`; producer disposition (commit-vs-release-vs-delete on its own state) is governed by per-producer config.

**LifecycleSubscribers** are an opt-in second protocol on the same peer binaries (configured via `protocols: [claim_producer, lifecycle_subscriber]` per peer in `rimsky.yml`). The six methods (`OnTemplateRegistered/Deployed/Undeployed/Deregistered`, `OnInstanceCreated/Terminated`) are fired synchronously by control-api at state transitions; idempotency is tracked in `rimsky_lifecycle_idempotency`.

**Executors** are peer services that speak the executor protocol (gRPC + HTTP+JSON bridge). Reference impls: `http-node` (Go), `claude-agent` (TypeScript / npm), `stub` (Go test fixture). Executors do **not** run in-process; supervisors dispatch to them over the wire.

Vocabulary: 2 message types (`invalidate`, `recalculate`), 4 node states (`fresh`, `stale`, `running`, `failed`), 3 error actions (`retry`, `invalidate(targets)`, `give_up`). Read `docs/node-graph-design.md` for the conceptual model and `docs/architecture.md` for the implementation shape before making non-trivial changes.

The three contracts in `docs/specs/` define the layered architecture's responsibilities:

- `docs/specs/2026-05-04-foundation-contract.md` — what foundation owns (cascade, claim/lock primitives, atomic acquisition, persistence drivers, sweeps).
- `docs/specs/2026-05-04-modeling-layer-contract.md` — what modeling owns (templates, instances, frames, scheduling, control-api, attributes, quality rules).
- `docs/specs/2026-05-04-service-protocol-contract.md` — the three peer-service protocols and their boundaries.

Earlier dated design docs in `docs/history/` capture the path here (stores redesign v2/v3, control-plane v1, frame-resolution, persistence-pluggable, layer crystallization). The contracts above supersede the earlier docs; the historical docs remain for context but are not authoritative.

## Package import rules (enforced; violations break the build)

These are non-negotiable. The repo is organized as **three Go modules** plus the modeling layer at the root:

- **`foundation/`** — own Go module (`github.com/fallguy/rimsky/foundation`). Cascade engine + claim/lock primitives + integration + foundation persistence. Depends on `protocols` + stdlib + minimal third-party (`pgx`, `uuid`, `modernc.org/sqlite`).
  - `foundation/cascade/` — node-state machine + cascade signal.
  - `foundation/locks/` — `ClaimProducer` interface (4 verbs + `Capabilities`), `ClaimID`/`ClaimSpec`/`NamedLockSpec`/`ClaimResult`/`Registry`/`NamedLocksConfig`, the `ModeCoexists` and `ScopesByteEqual` helpers, and the in-Go `storetest` fake. (The legacy alias `Store = ClaimProducer` is kept temporarily; new code should use `ClaimProducer`.)
  - `foundation/integration/` — supervisor runner (atomic acquisition tx, verify-before-run, callback server, terminal handler), the foundation tick sweeps (`SweepStaleHeartbeats`, `SweepOrphanedClaims`, `SweepReady`, `SweepLockHolders`), the auto-terminal mechanism, and the unified terminal-decision engine (`ResolveClaimHandleTerminal`). Plus `remote/` (the only concrete gRPC client to ClaimProducer impls).
  - `foundation/persistence/` — driver protocol (`Driver`, `AdvisoryLocker`, `Queue`, `Store` umbrella, `LockHoldersStore`, `ClaimHoldersStore`, `FrameStore`, per-feature interfaces) plus the postgres + sqlite impls. SQLite is dev-only; multi-host deployments require Postgres.
  - `foundation/internal/` — private to foundation; modeling and bundled services CANNOT import (enforced by depguard `foundation-internal-isolation`).
- **`protocols/`** — own Go module (`github.com/fallguy/rimsky/protocols`). Service-protocol Go interfaces + protobuf bindings. Stdlib + grpc + protobuf only. Proto sources at `protocols/proto/v1/`: `claim_producer.proto`, `lifecycle.proto`, `executor.proto`. Generated bindings under `protocols/proto/v1/gen/`.
- **Root module (`github.com/fallguy/rimsky`)** — modeling layer + cmd binaries + bundled service reference impls. Imports `foundation` + `protocols` + stdlib.
  - `modeling/` — `attribute/`, `template/canonical/`, `controlapi/`, `frame/`, `observability/`, `qualityrule/`, `executor/`, `cli/`, `config/`, `scheduler/`, `shared/`, `node/`, `scenario/`, `internal/pgtest/`.
  - `cmd/` — every reference binary (rimsky-control-api, rimsky-supervisor, rimsky-scheduler, rimsky-migrate, rimsky-conformance, rimsky-conformance-probe, rimsky-claim-producer-conformance, rimsky-cli, rimsky-entrypoint).
  - `stores/` — bundled claim-producer reference impls (filesystem, postgres, stub) packaged as standalone binaries.
  - `executors/` — bundled executor reference impls (http-node, stub, claude-agent).
- **`go.work`** ties the three modules together for development.
- **`.golangci.yml` depguard** enforces (a) `pgx-isolation` — pgx allowed only in `foundation/persistence/postgres/`, `foundation/internal/pgtest/`, `cmd/`, `modeling/internal/pgtest/`, `modeling/scenario/`, `stores/`, `test/smoke/` — and (b) `foundation-internal-isolation` — only `foundation/` may import `foundation/internal/`.

If you need to share logic between modeling subsystems, it goes into `modeling/shared/` (or `foundation/cascade/`/`foundation/locks/` if it's strictly foundation-level). Foundation never imports modeling.

## Blessed invariants (annotated `@blessed-invariant` in source)

These are load-bearing safety properties. Every one has a scenario test that exercises it; do not add idempotency short-circuits or "ergonomic" guards that would break them. Locations updated to post-Phase-6 paths:

1. **State machine rejects illegal transitions.** `running → running` under reason `dispatch_claimed` errors — it is **not** silently idempotent. (`foundation/cascade/state.go`)
2. **Worker-request claim brackets the running window.** Lock-eligibility counts (e.g. named-lock `mode: counting`) come from `rimsky_worker_request.claimed_by IS NOT NULL` joined against `rimsky_claim_handle`. (`foundation/persistence/postgres/queue.go`)
3. **Multi-lock acquisition uses deterministic sorted order.** All locks (named, scope) acquired in the deterministic sort order to prevent deadlock under contention. (`foundation/integration/runner_acquire.go`; `foundation/persistence/postgres/queue.go`)
4. **Claimant-guarded release.** Every `DELETE FROM rimsky_claim_handle` and every `UPDATE rimsky_worker_request SET claimed_by = NULL` is `AND … = supervisor_id`. Stale orphan sweeps cannot null or delete live ownership. (`foundation/persistence/postgres/queue.go`, `foundation/integration/runner_acquire.go`, `foundation/integration/orphan_reaper.go`)
5. **Verify-before-run.** Supervisor re-reads `claimed_by` immediately before calling the executor; bails as `orphaned_claim_lost_race` if ownership moved. (`foundation/integration/runner_acquire.go`)
6. **Orphan-claim cutoff is `5 × heartbeat_interval`.** Same cutoff applies to `rimsky_claim_handle` orphan reap. (`foundation/integration/conductor.go`, `foundation/integration/orphan_reaper.go`)
7. **Advisory lock on scheduler tick.** Postgres uses `pg_try_advisory_lock(SCHEDULER_TICK_KEY)`; SQLite uses `sync.Mutex`. Skips the tick when another replica holds it. (`foundation/persistence/postgres/advisory_locker.go`, `foundation/persistence/sqlite/advisory_locker.go`)
8. **Session advisory lock on migrations.** Held for the duration of the batch; released at session close. Postgres uses `pg_advisory_lock`; SQLite uses an in-process mutex. (`foundation/persistence/migrations.go`, `foundation/persistence/postgres/advisory_locker.go`, `foundation/persistence/sqlite/advisory_locker.go`)
9a. **Lock state lives only in the persistence layer.** No claim-producer implementation persists lock state; `rimsky_claim_handle` is the sole authority. (`foundation/locks/interface.go` — on the `ClaimProducer` interface comment)
9b. **ClaimProducer implementations do not internally serialize on lock-shaped predicates.** The reader-lease serialization pattern is forbidden for `staged_async`; honest support requires snapshot delegation or native MVCC pass-through. (`foundation/locks/interface.go`)
10. **Lock acquisition is atomic with worker-request claim (rimsky-side).** The acquisition transaction either claims the worker-request AND inserts all required `rimsky_claim_handle` rows AND records the `ClaimProducer.Open`-returned address, or none of these. The producer's own state mutations run in a producer-internal transaction decoupled from rimsky's. Single-writer-per-scope (invariant 4b) holds because rimsky's conflict predicate gates claim-handle INSERTs against `rimsky_claim_handle` only. (`foundation/integration/runner_acquire.go`)
11. **Userdata is opaque to rimsky.** No code path inspects, parses, substitutes, or validates `userdata`. (`modeling/attribute/substitution.go`; the `ExecuteRequest.userdata` proto comment)
12. **Attributes validate twice: at dispatch (post-substitution) and at commit (executor writeback).** Both gates mandatory. (`modeling/attribute/validate.go`)
13. **Held-claim resolution is auto-terminal, single, and aggregate-outcome-driven.** At holding-subgraph completion (all `rimsky_claim_holders` rows in `'completed'` or `'failed'`), the supervisor fires exactly one resolution per held claim based on aggregate outcome: all-completed → Commit; any-failed → Abandon. The verb-fire-and-delete sequence is delegated to `ResolveClaimHandleTerminal` (the unified terminal-decision engine). (`foundation/integration/auto_terminal.go`, `foundation/integration/terminal_decision.go`)
15. **`Open` fires inside the rimsky-side acquisition transaction.** The supervisor's atomic acquisition transaction calls `ClaimProducer.Open` between the claim-handle row INSERT and the rimsky-side COMMIT. The producer's own state mutation runs in its own transaction (producer-internal, decoupled from rimsky's). (`foundation/integration/runner_acquire.go`)
20. **Claim content (payload, address, scope) is inert in Rimsky.** Rimsky reads claim content by named-field path only at substitution-leaf extraction (`modeling/attribute/substitution.go::walkPath`); does not log, validate, transform, normalize, decrypt, hash, index, pattern-match, attach to traces, include in errors, or otherwise act on claim content. Distinct from store-config bytes (operator-managed; not under invariant 20). Annotated at `foundation/locks/types.go` on `ClaimResult` and `modeling/attribute/substitution.go::walkPath`.

(Invariant 14 is retired post-v3.)

Scenario tests in `test/scenarios/` (e.g. `verify_before_run_race_test.go`, `state_machine_same_state_rejected_test.go`) and `test/scenarios/locks/`, `test/scenarios/stores/`, `test/scenarios/claim_stores/`, `test/scenarios/frame_resolution/`, `test/scenarios/lifecycle/` exist to catch regressions of these. When adding new invariant coverage, drive the supervisor through `modeling/scenario.Start` against pre-launched producer-services on ephemeral ports (the smoke fixture in `test/smoke/setup.go` is the reference example).

## Build & test

The repo has three Go modules tied together by `go.work`. Standard commands:

```sh
make proto-gen        # regenerate proto bindings (run after editing protocols/proto/v1/*.proto)
make build-all        # go build across all three modules (root + foundation + protocols)
make test-all         # go test across all three modules
make lint             # golangci-lint (gofmt, goimports, govet, staticcheck, unused, ineffassign, errcheck, revive)
make tidy             # go mod tidy across all modules
```

Single-package or single-test runs:

```sh
go test ./foundation/integration/...
go test ./test/scenarios/ -run TestVerifyBeforeRunRace -v
go test ./test/scenarios/... -count=5 -race      # flake hunt
```

**Scenario and storage integration tests use testcontainers-go to spin up a real Postgres** (`foundation/internal/pgtest/`, `modeling/internal/pgtest/`). They will pull the `postgres:14-alpine` or `postgres:15` image and require a working Docker socket — they are not unit-test fast. Each scenario boots its own container.

The TypeScript executor (`executors/claude-agent/`) has its own build:

```sh
cd executors/claude-agent
npm install
npm test                # vitest
npm run build           # tsc → dist/
```

## Reference deployment & local stack

`deploy/docker-compose.yml` brings up Postgres + migrate + scheduler + supervisor + control-api + bundled claim-producers + bundled executors (with `RIMSKY_EXECUTOR_STUB_MODE=1` by default). Control API is on `:8080`; Postgres on `:5544`.

```sh
docker compose -f deploy/docker-compose.yml up -d
curl http://localhost:8080/health
```

The unified `rimsky.yml` (default `/etc/rimsky/rimsky.yml`, env var `RIMSKY_CONFIG`) declares persistence, named-locks, claim-producers, and executors in one file. Reference config: `deploy/rimsky.yml`. The unified image (`rimsky/all`) bundles the three runtime processes under a single PID-1 entrypoint (`rimsky-entrypoint`); see `deploy/Dockerfile.all` and `deploy/rimsky-all.yml`.

**The Helm chart at `deploy/kubernetes/rimsky-chart/` may lag behind binary env-var renames.** Verify before deploying.

## Schema (post-Phase-5 layer-crystallization consolidation)

The Phase-5 cycle consolidated the legacy split tables:

- **`rimsky_worker_request`** — parent run-bookkeeping row. One per dispatched run. `phase` column drives the active+held lifecycle (`'pending' | 'active' | 'held' | 'completed'`). `claimed_by` carries the supervisor id while `phase='active'`. The orphan reaper covers `phase='active'` rows with stale heartbeat. Replaces the legacy `rimsky_dispatch`.
- **`rimsky_claim_handle`** — child of `rimsky_worker_request` (FK with `ON DELETE SET NULL` so held claim handles outlive their parent's active terminal until auto-terminal fires). One row per (worker_request, lock-or-claim acquired). `lock_kind` ∈ `{'named', 'scope'}`. `is_held BOOLEAN` marks claims that persist past active terminal. `realized_write_semantics` is the per-claim verdict from `ClaimProducer.Open`. Replaces the legacy `rimsky_lock_holders`.
- **`rimsky_claim_holders`** — held-claim subgraph state ledger. One row per (claim_handle, holder_node) for held subgraph members; `state` ∈ `{'active', 'completed', 'failed'}`. Auto-terminal fires when all rows for a claim_handle are non-active. The FK column is `claim_handle_id` (renamed from `lock_holder_id`).
- **`rimsky_lifecycle_idempotency`** — per-(producer, scope_kind, scope_id) idempotency for LifecycleSubscriber events.

## Non-obvious gotchas

- **Two distinct callback hostnames.** The supervisor binds its async-callback HTTP listener on `0.0.0.0`, but executors need a peer-reachable hostname to dial back. Set `callback.advertise_host` (YAML) or `RIMSKY_SUPERVISOR_CALLBACK_ADVERTISE_HOST` (env) to the supervisor's service name (compose) or Service DNS (k8s). Empty → executors can't reach back.
- **TS claude-agent async-callback path.** The executor must POST to `${callback_url}/v1/callback/{async_ack_id}` with body keyed `type` (not `kind`) — enforced by the Go supervisor's chi route. End-to-end test in `executors/claude-agent/src/server.test.ts`.
- **`changed: bool` is producer-declared, not content-hashed.** A node committing `changed: false` stops cascade propagation at itself; rimsky does not verify the claim. Trust + audit log, not hash-and-check.
- **Schedule cron advances from `row.NextFireAt`, not `clock.Now()`.** Missed fires are NOT backfilled — intentional. (`modeling/scheduler/schedule_ticker.go`)
- **Operator-originated invalidates do not preempt running work.** In-flight work always runs to its terminal state. Invalidates either enqueue a new frame (`serial_queue` mode) or join the pending coalesce row (`coalesce` mode).
- **Frames are the unit of cascade resolution.** Every `rimsky_worker_request` row carries `frame_id NOT NULL`; every non-fresh `rimsky_nodes` row carries `frame_id`. Frame-end is the SQL predicate "no `rimsky_nodes` rows in state stale or running for this instance". At most one frame is `running` per instance.
- **Foundation `internal/` is private; depguard enforces.** Only `foundation/` may import `foundation/internal/`.
- **Worker-request phase column drives lifecycle.** Respect active vs held distinction in any new scheduling code. The orphan reaper at the worker-request level covers ONLY `phase='active'` rows; held claims are auto-terminal concern.
- **Held-claim handles outlive their worker-request's active phase.** The `worker_request_id` FK uses `ON DELETE SET NULL` (not CASCADE) so held claim handles survive the worker-request's deletion until auto-terminal explicitly removes them via `ResolveClaimHandleTerminal`.
- **Userdata is never substituted or inspected by rimsky.** `{{...}}` directives only resolve inside the `attributes` schema's `properties[*].source` field; identical-looking text in `userdata` reaches the executor verbatim. Don't add a "convenience" substitution pass — `@blessed-invariant 11` forbids it.
- **Held-claim resolution fires from `foundation/integration/auto_terminal.go::CheckAndFireResolution`** which delegates to the unified `ResolveClaimHandleTerminal`. At every node terminal in a held subgraph: locks the claim_handle row (SELECT … FOR UPDATE); checks whether all claim-holders rows are non-active; if so, computes aggregate outcome (any failed → Abandon; else Commit); the engine fires the verb and deletes the row claimant-guarded.
- **Claim content is inert in Rimsky (blessed invariant 20).** Address, payload, and scope from `ClaimResult` are opaque `json.RawMessage` bytes from Rimsky's perspective. Don't `slog.Any("payload", ...)` or `%+v`-format any of these fields anywhere; don't include them in error messages; don't attach to spans or events.
- **ClaimProducers are out-of-process.** Reference impls live as standalone binaries under `stores/<kind>/` (filesystem, postgres, stub). Rimsky talks to them via gRPC through `foundation/integration/remote/` — the only concrete `ClaimProducer` impl in the rimsky module. Each rimsky verb takes a `claim_id` (rimsky-generated UUID, also the claim_handle row id) so producers can correlate state across verbs.
- **Scope conflict is byte-equal.** Rimsky compares `rimsky_claim_handle.scope_data` byte-for-byte; producers canonicalize scope bytes such that two claims that should conflict produce byte-equal scopes. The standard filesystem store is concrete-paths only.
- **Atomicity is decoupled** between rimsky's bookkeeping tx and the producer's tx. Rimsky opens a tx, claims the worker-request, INSERTs claim_handle rows, RPCs `ClaimProducer.Open` (the producer runs in its own tx), UPDATEs claim_handle addresses, INSERTs claim-holders, COMMITs. Failures on either side are recovered via the orphan reaper (rimsky-side) and the producer's own TTL/sweep.
- **`POST /admin/scheduled-nodes/{node_id}/force-fire` is admin-only** — bypasses the cron next-fire calculation. The smoke fixture drives 100 sequential force-fires through it.
- **Stub mode is required for conformance runs of LLM-calling executors.** `rimsky-conformance --require-stub-mode` issues a probe via `rimsky-conformance-probe` at startup; non-stubbed executors will fail.
- **Templates are content-addressed.** `rimsky_templates.id` is `sha256-<64-hex>` over an RFC 8785 JCS-canonicalized spec (`modeling/template/canonical/CanonicalSpecHash`). Tags in `rimsky_template_tags` are movable aliases. Re-registering the same spec is a cheap no-op. Pre-v1: hash bytes are not pinned across breaking changes — dev-DB nuke.
- **Lifecycle events fire from control-api, not the supervisor.** The six events are RPCed synchronously by control-api at state transitions; idempotency tracked in `rimsky_lifecycle_idempotency`.
- **LifecycleSubscriber is an opt-in protocol.** Peers declare `protocols: [claim_producer, lifecycle_subscriber]` in `rimsky.yml`; bundled producer binaries can ship a no-op LifecycleSubscriber via `enable_lifecycle: true` config without forking the binary. Peers referenced by a template but not subscribed silently skip fan-out.
- **Instances bind to `template_hash` at creation.** FK is `template_hash TEXT`. `instance_key` (formerly `consumer_key`) is nullable. The instance HTTP create body is `{template, instance_key?, params}`.
- **Compose owns project-prefixed names.** Tags `compose:<project>:<...>` and instance keys `compose:<project>:<...>` are reserved for `rimsky-cli compose`. The CLI rejects manual registration with this prefix client-side.
- **`rimsky-cli` is a thin client; v1 does not version the control-api.** Bare paths (no `/v1/` prefix); rolling upgrades are operator-managed.
- **`RIMSKY_DB_URL` is gone.** All persistence config lives under the `persistence:` block in `RIMSKY_CONFIG` (`deploy/rimsky.yml`).
- **SQLite is the dev-only driver.** Multi-process / multi-host SQLite is not supported. Production deployments must use the postgres driver.
- **The unified image (`rimsky/all`) defaults to `driver: sqlite`** with state at `/var/lib/rimsky/state.db`. Running with replicas > 1 creates independent SQLite databases — broken. Use the per-process images plus the postgres driver for multi-replica deployments.
- **YAML config: `claim_producers:` block (legacy alias `stores:`).** Each entry has optional `protocols: [...]` (default `[claim_producer]`); required `write_semantics_envelope: [...]` (legacy single-value `write_semantics:` accepted as a single-element envelope shortcut). Operator's declared envelope MUST be ⊆ producer's advertised envelope (validated at startup).

## Where to look first

For external-consumer-facing material (cite from agents and external docs):

- Public concepts reference: `docs/concepts/` (canonical per-noun reference)
- Protocol-implementation guides: `docs/protocols/` (custom claim-producer/executor/lifecycle-subscriber)
- Agent-shaped indices: `docs/agents/llms.txt`, `docs/agents/llms-full.txt`
- Human-shaped narrative onboarding: `docs/humans/landing.md`, `docs/humans/concepts.md`, `docs/humans/dashboard.md`
- Public glossary (auto-generated): `docs/glossary.md`
- Public vocabulary discipline / deprecated terms: `docs/vocabulary.md`

For internal/working engineering material (do NOT cite from public surfaces):

- Foundation contract: `docs/specs/2026-05-04-foundation-contract.md`
- Modeling contract: `docs/specs/2026-05-04-modeling-layer-contract.md`
- Service-protocol contract: `docs/specs/2026-05-04-service-protocol-contract.md`
- Conceptual: `docs/internal/node-graph-design.md`
- Implementation: `docs/internal/architecture.md`
- Operating: `docs/internal/operator-guide.md`
- Internal glossary (now superseded by `docs/glossary.md` for external use): `docs/internal/glossary.md`
- Writing a claim producer (internal predecessor of `docs/protocols/claim-producer.md`): `docs/internal/claim-producer-author-guide.md`
- Writing an executor (internal predecessor of `docs/protocols/executor.md`): `docs/internal/executor-author-guide.md`
- Recent changes & rationale: `CHANGELOG.md` (long but informative)

## Code style

Follow the cold-read conventions in `.claude/rules/cold-read-cheatsheet.md` (organize by feature not layer; ~500-line file / ~100-line function guideline; max 3 levels of nesting via early returns; prefer tracked duplication over hidden coupling; `@source:` / `@diverged:` annotations for copies; `@agent-contract` / `@blessed-invariant` blocks for stable cross-cutting concerns).

Go specifics enforced by `.golangci.yml`: gofmt, goimports, govet, staticcheck, unused, ineffassign, errcheck, revive (without the `exported` rule). Logging is stdlib `log/slog`, JSON output, field-structured — no Zap, no Zerolog. HTTP routing is `go-chi/chi`. Postgres is `jackc/pgx/v5`. SQLite is `modernc.org/sqlite` (pure-Go, no CGO). Cron parsing is `robfig/cron/v3`.
