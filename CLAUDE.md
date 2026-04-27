# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this repo is

Rimsky is a project-agnostic reactive node-graph orchestration platform. Three architectural collections, all currently in this repo but designed to separate cleanly:

1. **Orchestrator** — `core/`. One Go module: `github.com/fallguy/rimsky`. Ships as three independent long-running processes (`rimsky-scheduler`, `rimsky-supervisor`, `rimsky-control-api`) plus `rimsky-migrate`, `rimsky-conformance`, `rimsky-conformance-probe`. The three runtime processes communicate **only through Postgres** — they cannot import each other (enforced by package rules below).
2. **Stores** — `core/store/`. In-process Go interface implementations of how data is persisted, locked, and claimed. v1 ships `filesystem` (direct mode over a real filesystem root, region-glob locking), `claimstorepg` (postgres-backed claim store with FIFO acquire / on-commit release actions / held-claim reference-counted resolution per §5.6.4), and `stub` (in-process test fixture).
3. **Executors** — `executors/`. Peer services that speak the node-executor protocol (gRPC + HTTP+JSON bridge). v1 ships `http-node` (Go), `claude-agent` (TypeScript / npm), and `stub` (Go test fixture). Executors do **not** run in-process; supervisors dispatch to them over the wire.

Vocabulary: 2 message types (`invalidate`, `recalculate`), 4 node states (`fresh`, `stale`, `running`, `failed`), 3 error actions (`retry`, `invalidate(targets)`, `give_up`). Read `docs/node-graph-design.md` for the conceptual model and `docs/architecture.md` for the implementation shape before making non-trivial changes.

The stores-redesign spec at `docs/specs/2026-04-25-stores-redesign-design.md` is the contract for the post-resources world the codebase has now adopted. Read it before touching template grammar, locks, claims, attributes, or the executor protocol.

## Package import rules (enforced; violations break the build)

These are non-negotiable and matter because the runtime subsystems are independent processes:

- `core/shared/` — depends on stdlib only.
- `core/node/`, `core/message/` — pure logic, import `shared/` only.
- `core/queue/`, `core/storage/` — interfaces + Postgres impls; import `shared/` and `pgx`.
- `core/store/` — declares the `Store`, `LockSpec`, `LockHandle`, `Capabilities`, `ReleaseAction`, `ClaimResult` interfaces plus the `Registry` and the shared `rimsky_lock_holders` postgres helpers. Concrete impls (`filesystem/`, `claimstorepg/`, `stub/`) live as subpackages. The `core/attributes/` package depends on `core/store/` types but no scheduler/supervisor/controlapi package may; store impls are wired in by the binaries in `core/cmd/`.
- `core/scheduler/`, `core/supervisor/`, `core/controlapi/` — **cannot import each other.** They share state through Postgres only.
- `core/cmd/*` — the only packages permitted to import everything needed to wire a binary.
- `core/config/` — library entry points (`StartScheduler`, `StartSupervisor`, `StartControlAPI`); the binaries in `cmd/` are thin shells around these.

If you need to share logic between scheduler and supervisor, it goes into `node/`, `message/`, `queue/`, `storage/`, or `shared/` — not into one of them with the other importing it.

## Blessed invariants (annotated `@blessed-invariant` in source)

These are load-bearing safety properties. Every one has a scenario test that exercises it; do not add idempotency short-circuits or "ergonomic" guards that would break them:

1. **State machine rejects illegal transitions.** `running → running` under reason `dispatch_claimed` errors — it is **not** silently idempotent. (`core/node/state.go`)
2. **Dispatch claim brackets the running window.** Lock-eligibility counts (e.g. named-lock `mode: counting`) come from `rimsky_dispatch.claimed_by IS NOT NULL` joined against `rimsky_lock_holders`. (`core/queue/postgres/queue.go`)
3. **Multi-lock acquisition uses deterministic sorted order.** All locks (named, region, claim) acquired in the §13.7 sort order to prevent deadlock under contention. (`core/supervisor/runner.go`; `core/queue/postgres/queue.go`)
4. **Claimant-guarded release.** Every `DELETE FROM rimsky_lock_holders` and every `UPDATE rimsky_dispatch SET claimed_by = NULL` is `AND … = supervisor_id`. Stale orphan sweeps cannot null or delete live ownership. (`core/queue/postgres/queue.go`, `core/supervisor/runner.go`, `core/scheduler/scheduler.go`)
5. **Verify-before-run.** Supervisor re-reads `claimed_by` immediately before calling the executor; bails as `orphaned_claim_lost_race` if ownership moved. (`core/supervisor/runner.go`)
6. **Orphan-claim cutoff is `5 × heartbeat_interval`.** Same cutoff applies to `rimsky_lock_holders` orphan reap. (`core/scheduler/scheduler.go`)
7. **Advisory lock on scheduler tick.** `pg_try_advisory_lock(SCHEDULER_TICK_KEY)` skips the tick if another replica holds it. (`core/scheduler/scheduler.go`)
8. **Session advisory lock on migrations.** Held for the duration of the batch; released at session close. Migration runner unlock uses `context.Background()` so a cancelled parent ctx does not strand the lock. (`core/migrations/runner.go`)
9. **Lock state lives only in postgres.** No store implementation persists lock state; `rimsky_lock_holders` is the sole authority. (`core/store/interface.go` — on the `Store` interface comment)
10. **Lock acquisition is atomic with dispatch claim.** The §13.3 step-3 transaction either claims dispatch AND inserts all required `rimsky_lock_holders` rows AND completes all store `AcquireLock` mutations, or none of these. (`core/supervisor/runner.go`; `core/queue/postgres/queue.go`)
11. **Userdata is opaque to rimsky.** No code path inspects, parses, substitutes, or validates `userdata`. (`core/attributes/substitution.go`; the `ExecuteRequest.userdata` proto comment)
12. **Attributes validate twice: at dispatch (post-substitution) and at commit (executor writeback).** Both gates mandatory. (`core/attributes/validate.go`)
13. **First-delete-wins, last-released-wins for held claims.** Reference-counted resolution per §5.6.4. (`core/store/claimstorepg/holders.go`)
14. **`RegionsConflict` and `UnmarshalRegion` are pure.** No side effects, no external state read; deterministic on inputs. (`core/store/interface.go`; `core/store/filesystem/region.go`)

Scenario tests in `test/scenarios/` (e.g. `verify_before_run_race_test.go`, `state_machine_same_state_rejected_test.go`, `locks/lock_atomic_acquisition_test.go`, `locks/lock_claimant_guarded_release_test.go`, `claim_stores/claim_hold_fan_out_first_delete_wins_test.go`) exist explicitly to catch regressions of these.

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
- **Frames are the unit of cascade resolution.** Every `rimsky_dispatch` row carries `frame_id NOT NULL`; every non-fresh `rimsky_nodes` row carries `frame_id`. Frame-end is the SQL predicate "no `rimsky_nodes` rows in state stale or running for this instance" — evaluated on every scheduler tick by `frame.RunTick`. Templates declare `frame_resolution: coalesce | serial_queue` (required field, control-api rejects without it). At most one frame is in `running` state per instance at any time (enforced by `uq_rimsky_frames_running`). See `docs/specs/2026-04-26-frame-resolution-design.md`.
- **Callback handler re-registers the async ack on `ApplyTerminalOutcome` failure** so executors can retry. Don't remove that retry path — it prevents stranding nodes in `running`.
- **`RIMSKY_STORES_CONFIG` is loaded by `rimsky-control-api`, `rimsky-supervisor`, and `rimsky-scheduler`.** All three need the registry: control-api validates templates against store kinds and exposes `POST /admin/claim-stores/{name}/items`; supervisor calls `Store.AcquireLock` / `OpenHandle` / `Commit` / `ReleaseLock`; scheduler runs the §13.5 step-4 visibility-timeout sweep over claim-store-postgres instances. Reference config is `deploy/stores.yml`. Empty / missing → store-touching control paths return 503 and supervisor cannot dispatch nodes that declare any store.
- **Userdata is never substituted or inspected by rimsky.** `{{...}}` directives only resolve inside the `attributes` schema's `properties[*].source` field; identical-looking text in `userdata` reaches the executor verbatim. Don't add a "convenience" substitution pass — `@blessed-invariant 11` forbids it. (`core/attributes/substitution.go`)
- **Held-claim resolution algorithm lives in `core/store/claimstorepg/holders.go`.** `ResolveOnTerminal` runs inside the supervisor's outer release transaction (via `store.TxFromContext`) so claim-holder mutations and the items-table row update commit or roll back atomically with the lock-holder delete. The first `delete` action wins (§5.6.4); subsequent commits at sibling terminals see a `completed` row and no-op. Don't move this logic into the supervisor.
- **`POST /admin/scheduled-nodes/{node_id}/force-fire` is admin-only** — it bypasses the cron next-fire calculation and updates `rimsky_schedules.next_fire_at = now()` immediately, returning 204 without waiting for the cascade. The smoke fixture (`test/smoke/`) drives 100 sequential force-fires through it; do not expose it on a non-admin route.
- **Stub mode is required for conformance runs of LLM-calling executors.** `rimsky-conformance --require-stub-mode` issues a probe via `rimsky-conformance-probe` at startup; non-stubbed executors will fail.
- **`go.mod` lives at the repo root**, but the docs sometimes say "core is a single Go module" reflecting the *future* split layout (`core/go.mod`). Today, all packages share the root module; treat the architecture doc's `core/go.mod` mention as aspirational.

## Where to look first

- Conceptual: `docs/node-graph-design.md`
- Implementation: `docs/architecture.md`
- Wire protocol: `docs/protocol.md` (authoritative source: `proto/v1/node_executor.proto`)
- Operating: `docs/operator-guide.md`
- Writing an executor: `docs/executor-author-guide.md`
- Writing a store impl: `docs/store-author-guide.md`
- Stores-redesign contract: `docs/specs/2026-04-25-stores-redesign-design.md`
- Recent changes & the rationale behind them: `CHANGELOG.md` (long but informative — has details no design doc captures)

## Code style

Follow the cold-read conventions in `.claude/rules/cold-read-cheatsheet.md` (organize by feature not layer; ~500-line file / ~100-line function guideline; max 3 levels of nesting via early returns; prefer tracked duplication over hidden coupling; `@source:` / `@diverged:` annotations for copies; `@agent-contract` / `@blessed-invariant` blocks for stable cross-cutting concerns).

Go specifics enforced by `.golangci.yml`: gofmt, goimports, govet, staticcheck, unused, ineffassign, errcheck, revive (without the `exported` rule). Logging is stdlib `log/slog`, JSON output, field-structured — no Zap, no Zerolog.
