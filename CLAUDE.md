# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this repo is

Rimsky is a project-agnostic reactive node-graph orchestration platform. Three architectural collections, all currently in this repo but designed to separate cleanly:

1. **Orchestrator** — `core/`. One Go module: `github.com/fallguy/rimsky`. Ships as three independent long-running processes (`rimsky-scheduler`, `rimsky-supervisor`, `rimsky-control-api`) plus `rimsky-migrate`, `rimsky-conformance`, `rimsky-conformance-probe`. The three runtime processes communicate **only through Postgres** — they cannot import each other (enforced by package rules below).
2. **Stores** — `stores/`. **Out-of-process** under v3: standalone binaries that implement the 4+1-verb gRPC protocol (`Open / Commit / Abandon / Release` plus `Capabilities()`). v3 ships `filesystem` (direct mode, concrete-paths only), `postgres` (regional access + items-table queue semantics implemented store-internally), and `stub` (in-memory test fixture). Rimsky talks to them via `core/store/remote/` — the only concrete `store.Store` implementation in the rimsky module. Auto-terminal at holding-subgraph completion drives held-claim resolution: success → `Commit`; failure → `Abandon`; store disposition (commit-vs-release-vs-delete on the store's own state) is governed by per-store config. See `docs/glossary.md` for the authoritative vocabulary.
3. **Executors** — `executors/`. Peer services that speak the node-executor protocol (gRPC + HTTP+JSON bridge). v1 ships `http-node` (Go), `claude-agent` (TypeScript / npm), and `stub` (Go test fixture). Executors do **not** run in-process; supervisors dispatch to them over the wire.

Vocabulary: 2 message types (`invalidate`, `recalculate`), 4 node states (`fresh`, `stale`, `running`, `failed`), 3 error actions (`retry`, `invalidate(targets)`, `give_up`). Read `docs/node-graph-design.md` for the conceptual model and `docs/architecture.md` for the implementation shape before making non-trivial changes.

The stores-redesign-v3 spec at `docs/history/2026-04-27-stores-redesign-v3-design.md` is the foundational contract; the 2026-04-30 cleanup overlay at `docs/history/2026-04-30-stores-protocol-cleanup-design.md` supersedes specific v3 sections (§4.1, §4.5, §4.7 third paragraph, §4.10 invariant 13.1, §5.1, §5.2, §7.8 obligation #3). Read both before touching the store protocol, the supervisor's acquisition flow, or the `stores:` block in `rimsky.yml`. The earlier v2 spec (`docs/history/2026-04-27-stores-redesign-v2-design.md`) is fully superseded; treat references to it in older code/docs as stale. The post-cleanup wire surface is 4 runtime verbs + 1 startup handshake; store disposition is per-store-config-driven.

## Package import rules (enforced; violations break the build)

These are non-negotiable and matter because the runtime subsystems are independent processes:

- `core/shared/` — depends on stdlib only.
- `core/node/`, `core/message/` — pure logic, import `shared/` only.
- `core/persistence/` — driver protocol (`Driver`, `Coordinator`, `Queue`, `Store`, `FrameStore`, per-feature interfaces) plus per-driver impls. The protocol package itself depends on stdlib + `shared/` + `node/` only. Driver impls live in `postgres/` (uses `pgx/v5`, `modernc.org/sqlite` not allowed) and `sqlite/` (uses `modernc.org/sqlite`, `pgx` not allowed). SQLite is the dev-only driver per spec §1; multi-host deployments require Postgres.
- `core/persistence/` owns all storage interfaces and impls; pgx is forbidden outside the postgres driver, the test infrastructure helpers in `core/internal/pgtest/`, `core/scenario/`, and `test/smoke/`, plus the per-process cmd binaries; enforced by golangci-lint depguard in `.golangci.yml`.
- `core/store/` — declares the `Store` interface (4 verbs + `Capabilities`), `ClaimID` / `ClaimSpec` / `NamedLockSpec` / `Capabilities` (one field: `WriteSemantics`) / `ClaimResult` (Address/Payload/Region as opaque `json.RawMessage`), the pure `ModeCoexists` and `RegionsByteEqual` helpers, and the simple name → `Store` `Registry` (no factories, no per-kind dispatch). The only concrete impls in this module are `core/store/remote/` (gRPC client) and `core/store/storetest/` (unit-test fake). Store implementations live under `stores/<kind>/` as standalone binaries.
- `core/scheduler/`, `core/supervisor/`, `core/controlapi/`, `core/frame/` — pure logic; depend on `persistence/` for state and on `shared/` / `node/` / `message/`. **No `pgx`, `pgxpool`, or `pgconn` imports allowed** (enforced by golangci-lint depguard). They share state only through `persistence.Store` / `persistence.Queue` / `persistence.Coordinator`.
- `core/cmd/*` — the only packages permitted to import everything needed to wire a binary. Each binary opens a `persistence.Driver` via `persistence.Open` and passes it to the relevant `core/config/Start*` entry point.
- `core/config/` — library entry points (`StartScheduler`, `StartSupervisor`, `StartControlAPI`); the binaries in `cmd/` are thin shells around these.

If you need to share logic between scheduler and supervisor, it goes into `node/`, `message/`, `persistence/`, or `shared/` — not into one of them with the other importing it.

## Blessed invariants (annotated `@blessed-invariant` in source)

These are load-bearing safety properties. Every one has a scenario test that exercises it; do not add idempotency short-circuits or "ergonomic" guards that would break them:

1. **State machine rejects illegal transitions.** `running → running` under reason `dispatch_claimed` errors — it is **not** silently idempotent. (`core/node/state.go`)
2. **Dispatch claim brackets the running window.** Lock-eligibility counts (e.g. named-lock `mode: counting`) come from `rimsky_dispatch.claimed_by IS NOT NULL` joined against `rimsky_lock_holders`. (`core/persistence/postgres/queue.go`)
3. **Multi-lock acquisition uses deterministic sorted order.** All locks (named, region, claim) acquired in the v3 spec §4.10 invariant 3 sort order to prevent deadlock under contention. (`core/supervisor/runner.go`; `core/persistence/postgres/queue.go`)
4. **Claimant-guarded release.** Every `DELETE FROM rimsky_lock_holders` and every `UPDATE rimsky_dispatch SET claimed_by = NULL` is `AND … = supervisor_id`. Stale orphan sweeps cannot null or delete live ownership. (`core/persistence/postgres/queue.go`, `core/supervisor/runner.go`, `core/scheduler/scheduler.go`)
5. **Verify-before-run.** Supervisor re-reads `claimed_by` immediately before calling the executor; bails as `orphaned_claim_lost_race` if ownership moved. (`core/supervisor/runner.go`)
6. **Orphan-claim cutoff is `5 × heartbeat_interval`.** Same cutoff applies to `rimsky_lock_holders` orphan reap. (`core/scheduler/scheduler.go`)
7. **Advisory lock on scheduler tick.** Postgres uses `pg_try_advisory_lock(SCHEDULER_TICK_KEY)`; SQLite uses `sync.Mutex` (single-process is the only supported topology). Skips the tick when another replica holds it. (`core/scheduler/scheduler.go`, `core/persistence/postgres/coordinator.go`, `core/persistence/sqlite/coordinator.go`)
8. **Session advisory lock on migrations.** Held for the duration of the batch; released at session close. Postgres uses `pg_advisory_lock`; SQLite uses an in-process mutex. (`core/persistence/migrations.go`, `core/persistence/postgres/coordinator.go`, `core/persistence/sqlite/coordinator.go`)
9a. **Lock state lives only in the persistence layer.** No store implementation persists lock state; `rimsky_lock_holders` is the sole authority. (`core/store/interface.go` — on the `Store` interface comment)
9b. **Store implementations do not internally serialize on lock-shaped predicates.** The reader-lease serialization pattern is forbidden for `staged_async`; honest support requires snapshot delegation or native MVCC pass-through. (`core/store/interface.go`)
10. **Lock acquisition is atomic with dispatch claim (rimsky-side).** Per v3 spec §4.10: the §7.3 acquisition transaction either claims dispatch AND inserts all required `rimsky_lock_holders` rows AND records the `Store.Open`-returned address, or none of these. The store's own state mutations run in a store-internal transaction decoupled from rimsky's; store atomicity is the store's concern (spec §7.8). Single-writer-per-region (invariant 4b) holds because rimsky's conflict predicate gates lock-holder INSERTs against `rimsky_lock_holders` only — store orphan state is invisible to the predicate. (`core/supervisor/runner_acquire.go`)
11. **Userdata is opaque to rimsky.** No code path inspects, parses, substitutes, or validates `userdata`. (`core/attributes/substitution.go`; the `ExecuteRequest.userdata` proto comment)
12. **Attributes validate twice: at dispatch (post-substitution) and at commit (executor writeback).** Both gates mandatory. (`core/attributes/validate.go`)
13. **Held-claim resolution is auto-terminal, single, and aggregate-outcome-driven.** At holding-subgraph completion (all `rimsky_claim_holders` rows in `'completed'` or `'failed'`), the supervisor fires exactly one resolution per held claim based on aggregate outcome: all-completed → `on_commit`; any-failed → `on_give_up`. No partial resolutions; no first-delete-wins / last-released-wins reconciliation. (`core/supervisor/auto_terminal.go`)
14. *(Retired in v3.)* The v2 invariant `RegionsConflict and UnmarshalRegion are pure` no longer applies — both methods were removed. Region conflict is now byte-equal on canonical store-supplied region bytes (per v3 spec §7.7); canonicalization is the store's responsibility. (`core/store/conflict.go::RegionsByteEqual`)
15. **`Open` fires inside the rimsky-side acquisition transaction.** Per v3 spec §4.10: the supervisor's atomic acquisition transaction calls `Store.Open` between the lock-holder row INSERT and the rimsky-side COMMIT. The store's own state mutation runs in its own transaction (store-internal, decoupled from rimsky's). The atomicity guarantee on the rimsky side (dispatch claim + lock-holder INSERT + address UPDATE) holds; store atomicity is the store's concern. (`core/supervisor/runner_acquire.go`)
20. **Claim content (payload, address, region) is inert in Rimsky.** Rimsky reads claim content by named-field path only at substitution-leaf extraction (`core/attributes/substitution.go::walkPath`); does not log, validate, transform, normalize, decrypt, hash, index, pattern-match, attach to traces, include in errors, or otherwise act on claim content. Distinct from store-config bytes (operator-managed; not under invariant 20 — see v3 spec §13.3). Annotated at `core/store/types.go` on `ClaimResult` and `core/attributes/substitution.go::walkPath`.

Scenario tests in `test/scenarios/` (e.g. `verify_before_run_race_test.go`, `state_machine_same_state_rejected_test.go`) exist to catch regressions of these. The v3 cutover left the per-area suites under `test/scenarios/{locks, stores, attributes, claim_stores}/` as compile-passing placeholders — substantive replacements run against the new loopback gRPC fixture (`stores/<kind>/testfixture.Start`). When adding new invariant coverage, drive the supervisor through `core/scenario.Start` against pre-launched store-services on ephemeral ports (the smoke fixture in `test/smoke/setup.go` is the reference example).

## Build & test

The Go module is rooted at the repo root (`go.mod` here, **not** under `core/`). Standard:

```sh
make proto-gen        # regenerate proto bindings (run once after editing proto/v1/*.proto)
go build ./...
go test ./...
make lint             # golangci-lint (gofmt, goimports, govet, staticcheck, unused, ineffassign, errcheck, revive)
make tidy             # go mod tidy
```

Single-package or single-test runs:

```sh
go test ./core/scheduler/...
go test ./test/scenarios/ -run TestVerifyBeforeRunRace -v
go test ./test/scenarios/... -count=5 -race      # flake hunt
```

**Scenario and storage integration tests use testcontainers-go to spin up a real Postgres** (`core/internal/pgtest`). They will pull the `postgres:15` image and require a working Docker socket — they are not unit-test fast. Each scenario boots its own container.

The TypeScript executor (`executors/claude-agent/`) has its own build:

```sh
cd executors/claude-agent
npm install
npm test                # vitest
npm run build           # tsc → dist/
```

## Reference deployment & local stack

`deploy/docker-compose.yml` brings up Postgres + migrate + scheduler + supervisor + control-api + `http-node` + `claude-agent` (the executors run in stub mode by default via `RIMSKY_EXECUTOR_STUB_MODE=1`). Control API is on `:8080`; Postgres on `:5544`.

```sh
docker compose -f deploy/docker-compose.yml up -d
curl http://localhost:8080/health
```

The supervisor reads its config from `RIMSKY_SUPERVISOR_CONFIG=/etc/rimsky/supervisor-config.yml`; the YAML shape is documented in `core/cmd/rimsky-supervisor/main.go`'s package comment.

**The Helm chart at `deploy/kubernetes/rimsky-chart/` is known stale** (env-var names lag behind the binaries — see CHANGELOG entry "Polish T6"). Fix it as a follow-up if you touch deployment.

## Non-obvious gotchas

- **Two distinct callback hostnames.** The supervisor binds its async-callback HTTP listener on `0.0.0.0`, but executors need a peer-reachable hostname to dial back. Set `callback.advertise_host` (YAML) or `RIMSKY_SUPERVISOR_CALLBACK_ADVERTISE_HOST` (env) to the supervisor's service name (compose) or Service DNS (k8s). Empty → executors can't reach back.
- **TS claude-agent async-callback path.** The executor must POST to `${callback_url}/v1/callback/{async_ack_id}` with body keyed `type` (not `kind`) — this is enforced by the Go supervisor's chi route. There is an end-to-end test in `executors/claude-agent/src/server.test.ts` against a fake supervisor with the real chi route shape; do not regress it.
- **`changed: bool` is producer-declared, not content-hashed.** A node committing `changed: false` stops cascade propagation at itself; rimsky does not verify the claim. Trust + audit log, not hash-and-check. See `node-graph-design.md` §4.3.
- **Schedule cron advances from `row.NextFireAt`, not `clock.Now()`.** Missed fires are **not** backfilled — this is intentional. (`core/scheduler/schedule_ticker.go`)
- **Operator-originated invalidates do not preempt running work.** They go through `frame.EnqueueOrCoalesce` (in `core/scheduler/invalidate.go`'s `InvalidateNode`) and either enqueue a new frame (`serial_queue` mode) or join the pending coalesce row (`coalesce` mode). The `rimsky_nodes.kill_requested` column was removed; there is no kill-poll path anymore. In-flight work always runs to its terminal state.
- **Frames are the unit of cascade resolution.** Every `rimsky_dispatch` row carries `frame_id NOT NULL`; every non-fresh `rimsky_nodes` row carries `frame_id`. Frame-end is the SQL predicate "no `rimsky_nodes` rows in state stale or running for this instance" — evaluated on every scheduler tick by `frame.RunTick`. Templates declare `frame_resolution: coalesce | serial_queue` (required field, control-api rejects without it). At most one frame is in `running` state per instance at any time (enforced by `uq_rimsky_frames_running`). See `docs/history/2026-04-26-frame-resolution-design.md`.
- **Callback handler re-registers the async ack on `ApplyTerminalOutcome` failure** so executors can retry. Don't remove that retry path — it prevents stranding nodes in `running`.
- **`RIMSKY_CONFIG` is loaded by all four rimsky binaries: `rimsky-control-api`, `rimsky-supervisor`, `rimsky-scheduler`, and `rimsky-migrate`.** Per the 2026-05-01 control-plane spec §3.1 / §6.6 the unified `rimsky.yml` declares persistence, stores, named-locks, and executors in one file (default `/etc/rimsky/rimsky.yml`); the per-stores schema (§6.1) is a thin "name → endpoint + declared capabilities" form (no `kind`, no `connection`, no `pick_policies` — those live in each store-service's own config). The three runtime processes dial each entry, run the `Capabilities()` handshake, and validate strict equality against the operator-declared block; any failure (unreachable, mismatch) fails the rimsky process at startup. `rimsky-migrate` only consumes the `persistence:` block. Supervisor calls the 4-verb store interface (`Open` / `Commit` / `Abandon` / `Release`) over the wire; scheduler's orphan reaper deletes lock-holder rows without firing `Abandon` (the store's own TTL handles cleanup); control-api validates that every template-referenced store name is declared. Reference config is `deploy/rimsky.yml`. The v2 admin items endpoint (`POST /admin/stores/{name}/pick-policies/{selector}/items`) is gone — each store-service that supports pick policies owns its own admin surface (see operator-guide §8.4).
- **Userdata is never substituted or inspected by rimsky.** `{{...}}` directives only resolve inside the `attributes` schema's `properties[*].source` field; identical-looking text in `userdata` reaches the executor verbatim. Don't add a "convenience" substitution pass — `@blessed-invariant 11` forbids it. (`core/attributes/substitution.go`)
- **Held-claim resolution lives in `core/supervisor/auto_terminal.go::CheckAndFireResolution`** (auto-terminal mechanism per v3 spec §4.10 invariant 13, as amended by the 2026-04-30 cleanup). At every node terminal in a held subgraph, the supervisor: locks the lock-holder row (`SELECT … FOR UPDATE`); checks whether all `rimsky_claim_holders` rows for the lock-holder are non-active; if so, computes aggregate outcome (any failed → Abandon; else Commit); fires that store verb; deletes the lock-holder row claimant-guarded (cascade FK cleans up claim-holder rows). The store decides what Commit / Abandon mean for its own state per its own configuration (e.g. the postgres reference store-service's per-pick-policy `on_commit_default` / `on_give_up_default`). Rimsky carries only the success/failure binary; no template-level action vocabulary. Held claims are inserted at acquisition (in `runner_acquire.go::insertHeldClaimHoldersAtAcquire`), not at terminal.

- **Claim content is inert in Rimsky (blessed invariant 20).** Address, payload, and region from `ClaimResult` are opaque `json.RawMessage` bytes from Rimsky's perspective. The only sanctioned introspection site is `core/attributes/substitution.go::walkPath`, which lazy-unmarshals into a transient `map[string]any` only inside the leaf-extraction call. Don't `slog.Any("payload", ...)` or `%+v`-format any of these fields anywhere else; don't include them in error messages; don't attach to spans or events. See `docs/glossary.md` for the full vocabulary and `docs/history/2026-04-27-stores-redesign-v3-design.md` §13.3 for the v3 rationale and the claim-content vs. store-config-bytes distinction (v3 §13.3 supersedes the v2 §17.5 reference).

- **Stores are out-of-process under v3.** Standard impls live as standalone binaries under `stores/<kind>/` (filesystem, postgres, stub). Rimsky talks to them via gRPC through `core/store/remote/` — the only concrete `store.Store` impl in the rimsky module. The v2 in-process Factory pattern is gone; `core/store/tx.go` is gone (no more tx-sharing); `Store.Kind` / `Store.RegionsConflict` / `Store.UnmarshalRegion` are gone. Each rimsky verb takes a `claim_id` (rimsky-generated UUID, also the lock-holder row id) so stores can correlate state across verbs. See `docs/history/2026-04-27-stores-redesign-v3-design.md`.

- **Region conflict is byte-equal.** Rimsky compares `rimsky_lock_holders.region_data` byte-for-byte; stores canonicalize region bytes such that two claims that should conflict produce byte-equal regions (per spec §7.7). The standard filesystem store dropped v2's glob support — concrete paths only.

- **Atomicity is decoupled** between rimsky's bookkeeping tx and the store-service's tx. Per spec §7.3: rimsky opens a tx, claims dispatch, INSERTs lock-holder rows, RPCs `Store.Open` (the store-service runs in its own tx), UPDATEs lock-holder addresses, INSERTs claim-holders, COMMITs. Failures on either side are recovered via the orphan reaper (rimsky-side) and the store's own TTL/sweep (per the §7.8 obligations).
- **`POST /admin/scheduled-nodes/{node_id}/force-fire` is admin-only** — it bypasses the cron next-fire calculation and updates `rimsky_schedules.next_fire_at = now()` immediately, returning 204 without waiting for the cascade. The smoke fixture (`test/smoke/`) drives 100 sequential force-fires through it; do not expose it on a non-admin route.
- **Stub mode is required for conformance runs of LLM-calling executors.** `rimsky-conformance --require-stub-mode` issues a probe via `rimsky-conformance-probe` at startup; non-stubbed executors will fail.
- **`go.mod` lives at the repo root**, but the docs sometimes say "core is a single Go module" reflecting the *future* split layout (`core/go.mod`). Today, all packages share the root module; treat the architecture doc's `core/go.mod` mention as aspirational.

- **Templates are content-addressed.** `rimsky_templates.id` is `sha256-<64-hex>` over an RFC 8785 JCS-canonicalized spec (`core/canonical/CanonicalSpecHash`). Tags in `rimsky_template_tags` are movable aliases. Re-registering the same spec is a cheap no-op. Tag movement does not migrate live instances — instances bind to the resolved hash at creation. **Hash bytes are not pinned across pre-v1 changes**: the 2026-05-02 json-tags cleanup (`docs/history/2026-05-02-template-spec-json-tags-design.md`) changed the canonical bytes from capital-cased Go-field keys to lowercase-snake-case keys; under pre-v1 rules this required a dev-DB nuke (no production data to preserve). Future hash-bytes changes follow the same pre-v1 break-freely rule until v1 ships.

- **Lifecycle events fire from control-api, not the supervisor.** The six events (`OnTemplateRegistered/Deployed/Undeployed/Deregistered/OnInstanceCreated/Terminated`) are RPCed synchronously by control-api at state transitions; idempotency is tracked in `rimsky_store_lifecycle`. Instance-terminated events fire from a control-api background terminator goroutine that polls `rimsky_instances.terminated_at`.

- **All stores implement all six lifecycle methods.** No subscription model in `Capabilities`; stores that don't react just return `nil` from each method. The supervisor never fires lifecycle events.

- **Instances bind to `template_hash` at creation.** Old `template_id UUID` is gone; FK is now `template_hash TEXT`. `consumer_key` was renamed to `instance_key` and is nullable. The instance HTTP create body shape is now `{template, instance_key?, params}`; the legacy `template_id` and `consumer_key` field names are no longer accepted.

- **Compose owns project-prefixed names.** Tags `compose:<project>:<...>` and instance keys `compose:<project>:<...>` are reserved for `rimsky-cli compose`. Manual `rimsky-cli template register --tag compose:foo:bar` is rejected by the CLI (client-side validation). Manual `curl POST /tags` against the same prefix is **not** rejected by the control-api (the reserved-prefix check is CLI-side only — see spec §9 open question 6); operators sharing a control-api should pick distinct compose project names to avoid collision.

- **`rimsky-cli` is a thin client; v1 does not version the control-api.** The CLI talks to bare paths (no `/v1/` prefix) and does not check server version; rolling upgrades are operator-managed. Endpoints used by both versions work; endpoints only on one return 404 / 405. (`docs/history/2026-05-02-rimsky-cli-and-compose-design.md` §6.2.)

- **`RIMSKY_DB_URL` is gone.** All persistence config now lives under the `persistence:` block in `RIMSKY_CONFIG` (`deploy/rimsky.yml`). `rimsky-migrate`, `rimsky-scheduler`, `rimsky-supervisor`, and `rimsky-control-api` all open a `persistence.Driver` via `persistence.Open(ctx, cfg.Persistence)` at startup. See `docs/history/2026-05-02-persistence-pluggable-and-unified-image-design.md` §8.

- **SQLite is the dev-only driver.** Multi-process / multi-host SQLite is not supported. The startup banner and operator-guide say so loudly; do not "fix" the banner to be quieter. Production deployments must use the postgres driver.

- **The unified image (`rimsky/all`) bundles the three runtime processes under a single PID-1 entrypoint (`rimsky-entrypoint`).** It defaults to `driver: sqlite` with state at `/var/lib/rimsky/state.db`. Running it with replicas > 1 creates independent SQLite databases — broken. Use the per-process images (`rimsky/scheduler`, `rimsky/supervisor`, `rimsky/control-api`) plus the postgres driver for multi-replica deployments. See `deploy/Dockerfile.all` and `deploy/rimsky-all.yml`.

- **SQLite driver limitations.** The SQLite driver is fully wired (Store, Queue, Coordinator, Migrate all functional) and works end-to-end against the unified image, but it is dev-only by design: single-writer concurrency (`SetMaxOpenConns(1)` plus `_txlock=immediate`); no cross-process advisory locks (the coordinator's named-lock and region-lock methods are no-ops because the BEGIN IMMEDIATE writer-slot hold subsumes them); cannot be replicated. Production / multi-replica deployments must use the postgres driver.

## Where to look first

- Conceptual: `docs/node-graph-design.md`
- Implementation: `docs/architecture.md`
- Wire protocol: `docs/protocol.md` (authoritative source: `proto/v1/node_executor.proto`)
- Operating: `docs/operator-guide.md`
- Writing an executor: `docs/executor-author-guide.md`
- Writing a store impl: `docs/store-author-guide.md`
- Stores-redesign contract (current): `docs/history/2026-04-27-stores-redesign-v3-design.md` (v3; supersedes v2)
- Control-plane v1 + store lifecycle protocol: `docs/history/2026-05-01-control-plane-and-store-lifecycle-design.md`
- Recent changes & the rationale behind them: `CHANGELOG.md` (long but informative — has details no design doc captures)

## Code style

Follow the cold-read conventions in `.claude/rules/cold-read-cheatsheet.md` (organize by feature not layer; ~500-line file / ~100-line function guideline; max 3 levels of nesting via early returns; prefer tracked duplication over hidden coupling; `@source:` / `@diverged:` annotations for copies; `@agent-contract` / `@blessed-invariant` blocks for stable cross-cutting concerns).

Go specifics enforced by `.golangci.yml`: gofmt, goimports, govet, staticcheck, unused, ineffassign, errcheck, revive (without the `exported` rule). Logging is stdlib `log/slog`, JSON output, field-structured — no Zap, no Zerolog.
