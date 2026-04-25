# Rimsky v1 Execution Log

Append-only log maintained during unattended implementation runs of Plans A → B → C. Entry-point doc: `docs/plans/2026-04-21-rimsky-execution-chain.md`.

Do not delete prior entries. On halt, add a `## Halt: [reason]` section with full context.

---

## Pre-flight

- Kickoff date: 2026-04-21 (attended — user watching)
- Kickoff operator: Claude Code session driving subagent-dev
- Docker running: yes (other containers detected; rimsky-dev-pg will be created ad hoc)
- Node version: v22.2.0
- Working tree clean: no uncommitted tracked changes (only new untracked docs/); acceptable — no git commits will be made during this run per guardrails
- All three plans present: yes (Plan A, Plan B, Plan C under docs/plans/)

---

## Plan A — Foundation + Scheduler

**Plan file:** `docs/plans/2026-04-21-rimsky-plan-a-foundation-scheduler.md`
**Started:** 2026-04-21 (attended session)
**Status:** in_progress

### Task 1.1 — Create /rimsky/ package metadata
- Outcome: DONE
- Attempts: 1
- Notes: 6 files created, npm install succeeded (381 packages, warnings inherited from pinned deps), tsc --noEmit reports expected "no inputs found" since src/ doesn't exist yet.

### Task 1.2 — Create source directory skeleton
- Outcome: DONE
- Attempts: 1
- Notes: 11 directories under rimsky/ created; src/index.ts stub added; tsc --noEmit exits 0.

### Phase 2 (Tasks 2.1–2.4) — Shared utilities
- Outcome: DONE
- Attempts: 2
- Notes: First attempt surfaced two real plan bugs. Fix 1: `logger.ts` pino import — NodeNext refuses callable default import; changed to `import { pino, type Logger as PinoLogger } from "pino";`. Fix 2: `clock.ts` `advance` implementation needed more substantive rewrite — the verbatim plan version jumped `this.t` to target immediately, so chained `c.sleep(50)` after a first sleep fired would enqueue at `target+50` and never resolve. New implementation steps forward through due deadlines, flushes, yields 8 microtask turns per round for chained-await continuations to re-enqueue, then finalizes at target. All 3 clock tests pass; tsc exits 0. Plan A updated in-place so Plan B/C inherit the correct snippets.

### Phase 3 (Tasks 3.1–3.3) — Migrations
- Outcome: DONE
- Attempts: 1
- Notes: 001-initial.sql written with rimsky_migrations prepend + all 9 spec tables + 10 indexes (10 CREATE TABLE total including migrations tracker). runner.ts written verbatim. Live-DB verification: applied against fresh postgres:14 container → "Applied: 001-initial.sql"; \dt rimsky_* shows all 10 tables; re-run says "No migrations to apply" (idempotent). Container torn down.

### Phase 4 (Tasks 4.1–4.6) — Pure logic (template types/validator, state machine, policy evaluator, quality rules, backoff)
- Outcome: DONE
- Attempts: 1
- Notes: 51/51 new unit tests pass across the 5 test files; full suite 54/54 (incl. Phase 2's clock tests). Template validator uses zod for structural validation + semantic checks (dep refs, cycles, policy targets, timer emit targets). Policy evaluator implementation: subagent advanced action_index by 1 in invalidate's returned new_state rather than the plan's literal "action_index stays" phrasing — functionally equivalent for every tested case; note for review. feature-index.md / CHANGELOG deferred to Task 10.1 per plan.

### Phase 5 (Task 5.1) — Message types
- Outcome: DONE
- Attempts: 1
- Notes: Plain types file, verbatim per plan.

### Phase 6 (Tasks 6.1–6.11) — Storage interfaces + Postgres implementations + integration tests
- Outcome: DONE
- Attempts: 1
- Notes: Task 6.1 added `StorageBackend` + all 8 sub-store interfaces. Tasks 6.2–6.10 implemented 9 Postgres files (backend factory + 8 stores) with the `executor(tx)` helper pattern throughout. **Spec/migration amendment applied**: added `kind TEXT NOT NULL` column to `rimsky_cells` (both in 001-initial.sql and in the spec doc) — needed by dispatch-row population without a full template load. Task 6.11 ran 11 scenarios against testcontainers Postgres; all pass (cold ~14s; warm ~3s). Full suite 65/65. No store bugs found.

### Phase 7 (Tasks 7.1–7.2) — Dispatch queue
- Outcome: DONE
- Attempts: 1
- Notes: Interface + PostgresDispatchQueue + 7 integration scenarios. ON CONFLICT predicate preserves backoff-future-dated rows and claimed rows. Full suite 72/72. Harness extended with `queue` field.

### Phase 8 (Tasks 8.1–8.3 + Plan B's recalculate-helper pre-adopted) — Scheduler
- Outcome: DONE
- Attempts: 1
- Notes: timer-ticker (cron + processTimers), invalidate helper (state transition + restore_version + dependent propagation), recalculate helper (message_received event + conditional re-enqueue), main scheduler loop (timers + heartbeat sweep + ready sweep), config/scheduler.ts entry point, src/index.ts rewritten as public surface. 16 new tests (6 timer-ticker unit, 6 recalculate integration, 4 scheduler integration). Full suite 88/88. `npm run build` succeeds. **Gap flagged**: `runMigrations` re-export dropped because `migrations/runner.ts` is outside `rootDir`. Fix applied after: migrations moved under `src/migrations/`, harness re-wired, runMigrations exported.

### Post-phase fix — migration relocation + harness robustness
- Outcome: DONE
- Notes: Moved migrations under `src/migrations/` so `runMigrations` can be re-exported from the library surface. Harness now uses `Wait.forLogMessage("database system is ready to accept connections", 2)` plus a `waitForReady` SELECT-1 loop before running migrations — fixes parallel-run testcontainers flakiness. Full suite 88/88 with parallel execution enabled.

### Phase 9 (Tasks 9.1–9.7) — Scenario tests + stub supervisor
- Outcome: DONE
- Attempts: 1
- Notes: Harness extended with `ScenarioHarness`, `startScenarioHarness`, `deployTemplateFromYaml`, `deployTemplateFromSpec`, `createInstance`, `waitFor`. Stub supervisor under `test/fakes/stub-supervisor.ts`. Five scenario tests under `test/scenarios/`: timer-fires, dispatch-enqueue, heartbeat-loss, concurrency-tag-limit, backoff-respected. All 5 pass. Full suite 93/93.

### Phase 10 (Task 10.1) — Definition of done
- Outcome: DONE
- Notes: `npm run build` exit 0. Full suite 93/93 passing. `npm run lint` clean (fixed 5 small issues: one `let→const`, four `any` type replacements in timer-ticker_test.ts). Live-DB migration via fresh postgres:14 → "Applied: 001-initial.sql"; re-run says "No migrations to apply" (idempotent). All 10 required exports present in `src/index.ts` (startScheduler, createPostgresStorage, PostgresDispatchQueue, runMigrations, Clock, Logger, SystemClock, ControllableClock, createPinoLogger, SilentLogger). CHANGELOG entry appended with 2026-04-21 date. Docs unchanged except the `kind` column amendment in spec §3.1 noted during Phase 6.

**Plan A completed:** 2026-04-21 (attended session; real-time wall-clock ~unknown, a few hours of subagent dispatch)
**Gate check:** build ✓ · tests 93/93 ✓ · lint ✓ · migrations ✓ · exports ✓ · CHANGELOG ✓

---

## Plan B — Deterministic Supervisor

**Plan file:** `docs/plans/2026-04-21-rimsky-plan-b-deterministic-supervisor.md`
**Started:** 2026-04-21
**Status:** completed

### Phase 0 — Amendments (listDependentsOf + recalculate helper)
- Outcome: DONE (listDependentsOf + recalculate pre-adopted during Plan A Phases 6 and 8; added missing integration test row for listDependentsOf during Plan B kickoff)

### Phases 1–3 — Supervisor types + handler registry + commit flow + on-error path
- Outcome: DONE
- Notes: 7 files (types, handler-registry, commit, on-error + 3 test files). Phase 1-3 added 19 new tests. Full suite 113/113.

### Phases 4–6 — Deterministic runner + supervisor process + harness extension
- Outcome: DONE
- Notes: 6 files created/modified. startSupervisor rejects agentic+timer acceptance. Deterministic kill_requested cannot interrupt in-process handler (documented limitation). Full suite 121/121.

### Phase 7 — 8 deterministic scenario tests
- Outcome: DONE
- Notes: Scenarios cover happy path, retry-then-succeed, cascade-invalidate, give-up, double-buffering, no-op commit, rollback, concurrency respected. All use SystemClock (not ControllableClock) because Postgres NOW() governs claim eligibility. Full suite 129/129.

### Phase 8 — Definition of done
- Outcome: DONE
- Notes: `npm run build` exit 0. Fixed one flaky timer-firing assertion in scheduler_test.ts (poll on `last_fired_at` like we do on cell state). Full suite 129/129 passes reliably. `npm run lint` clean. All 8 required Plan B exports present in src/index.ts. Fresh-DB migration applies (Plan A migration unchanged). CHANGELOG entry appended.

**Plan B completed:** 2026-04-21
**Gate check:** build ✓ · tests 129/129 ✓ · lint ✓ · migrations ✓ · exports ✓ · CHANGELOG ✓

---

## Plan C — Agentic + Control API + Reference Binaries

**Plan file:** `docs/plans/2026-04-21-rimsky-plan-c-agentic-control-api.md`
**Started:** 2026-04-21
**Status:** completed

### Phase 0 — Amendments
- validateConfig now permits agentic with cliRunner+callback. Dispatch branches by cell_kind. kill_requested clearing pre-adopted in Plan A. work_rejected is a free-form event-kind string.

### Phase 1 — CLI runner abstraction
- CliRunner interface, createClaudeCliRunner real impl (spawns `claude` with system-prompt/mcp-config tmpfiles), FakeCliRunner for tests. 5 new tests; full suite 134/134.

### Phase 2 — Callback MCP server
- Implemented as plain Node HTTP server speaking JSON-RPC 2.0 (MCP SDK not installed; fallback path per plan). token-registry, tools (zod schemas + TOOL_DEFINITIONS), server.ts with scheduleTeardown-after-response via `res.on("finish")`. mcp-client test helper. 28 new tests; full suite 162/162.

### Phase 3 — Agentic runner + shared terminal-outcome helper
- applyTerminalOutcome extracted to src/supervisor/terminal-outcome.ts (deterministic runner refactored to call it). agentic-runner.ts with 6-path handling (complete/blocked/error/silence/exit-before-complete/quality-failed-post-commit). Tests: 6 agentic-runner + 2 supervisor wiring. Full suite 170/170.

### Phase 4 — Supervisor config extension
- Callback server spawned per-supervisor when agentic accepted; shutdown tears it down. Supervisor registers callback host/port in rimsky_supervisors row.

### Phase 5 — Agentic scenario tests
- 5 scenario files (happy, invalid-result-retry, blocked, silence-timeout, error-class) that drive the full stack (scheduler + supervisor + FakeCliRunner + callback server). Full suite 175/175.

### Phase 6 — Control API
- Fastify app with routes for templates, instances, cells (incl. operator invalidate/reset/kill), events, resources, health. zod-based request/response schemas. Error handler maps typed errors to HTTP status. createInstance helper moved from test/harness.ts into src/control-api/instance-factory.ts (back-compat re-export). fastify@^4 added. End-to-end test via HTTP. Full suite 205/205.

### Phase 7 — Reference binaries
- Three env-var binaries (`rimsky-scheduler`, `rimsky-supervisor`, `rimsky-control-api`) with shebang headers preserved through tsc. `package.json` bin entries point at `dist/entrypoints/*.js`. Supervisor binary supports `RIMSKY_HANDLERS_MODULE` dynamic import for consumer handlers.

### Phase 8 — Definition of done
- `npm run build` exit 0. Full suite 205/205 passing. `npm run lint` clean. Fresh-DB migration applies idempotently. All three binaries start and report missing env vars cleanly. All required public exports present in `src/index.ts`. CHANGELOG + feature-index updated.

**Plan C completed:** 2026-04-21
**Gate check:** build ✓ · tests 205/205 ✓ · lint ✓ · migrations ✓ · binaries ✓ · exports ✓ · CHANGELOG ✓

---

## Final state

- Final status: **ALL THREE PLANS COMPLETE**
- Wall-clock duration: several hours of attended session
- Tests: 205 passing / 0 failing across 45 files (unit + integration + scenarios)
- Rimsky v1 is feature-complete per the spec: migrations, storage (8 Postgres stores), dispatch queue, pure logic, scheduler, deterministic + agentic supervisor, callback MCP, HTTP control API, three reference binaries, library public surface.
- Notes for returning operator: v1 rimsky is ready for zonebase's ingestion layer to consume as a library. Open-source extraction is now a matter of copying `/rimsky/` out, renaming the package, and publishing. No git commits were made during this run — review `git status`/`git diff` and commit what you want to keep.

---

## Plan B — Deterministic Supervisor

**Plan file:** `docs/plans/2026-04-21-rimsky-plan-b-deterministic-supervisor.md`
**Started:** _____
**Status:** pending

---

## Plan C — Agentic + Control API + Reference Binaries

**Plan file:** `docs/plans/2026-04-21-rimsky-plan-c-agentic-control-api.md`
**Started:** _____
**Status:** pending

---

## Final state (fill in when all plans complete or chain halts)

- Final status: _____
- Wall-clock duration: _____
- Tests: _____ passing / _____ failing
- Notes for the returning operator: _____
