# Rimsky Plan A — Foundation + Scheduler

**Goal:** Stand up the `/rimsky/` module with migrations, storage interfaces, pure-logic components (state machine, policy evaluator, template validator, quality rules), message helpers, dispatch queue, and a running scheduler process that ticks, handles timers, enqueues dispatch rows, and detects dead supervisors — all with full unit and scenario test coverage against a stub supervisor.

**Architecture:** TypeScript library under `/rimsky/src/` organized feature-first (per `docs/cell-graph-design.md` and `docs/specs/2026-04-21-rimsky-v1-design.md`). Postgres is the default (and only v1) storage and queue backend, accessed via explicit interfaces (`StorageBackend`, `DispatchQueue`) so the seams are ready for alternate implementations. No process is spawned in this plan except the scheduler; supervisors are stubbed in tests.

**Tech stack:** Node 20+, TypeScript (ES2022 target, NodeNext module resolution, strict), Postgres 14+ via `pg`, `pino` for logging, `vitest` for tests, `testcontainers` for scenario tests, no framework dependencies beyond those.

**Reference documents:**
- Spec: `docs/specs/2026-04-21-rimsky-v1-design.md`
- Design: `docs/cell-graph-design.md`

---

## Module layout this plan produces

```
rimsky/
├── package.json
├── tsconfig.json
├── vitest.config.ts
├── .eslintrc.cjs
├── README.md                          # minimal stub
├── migrations/
│   ├── runner.ts                      # migration-runner helper
│   └── 001-initial.sql
├── src/
│   ├── shared/
│   │   ├── types.ts                   # UUID, CellKind, CellState, ResourcePath, etc.
│   │   ├── errors.ts                  # RimskyError base + typed subclasses
│   │   ├── clock.ts                   # Clock, SystemClock, ControllableClock
│   │   └── logger.ts                  # Logger, pino wrapper, SilentLogger
│   ├── cell/
│   │   ├── template.ts                # CellTemplate types
│   │   ├── template-validator.ts
│   │   ├── template-validator_test.ts
│   │   ├── state-machine.ts           # state transition table + transition helper
│   │   ├── state-machine_test.ts
│   │   ├── policy-evaluator.ts        # on_error action advancement
│   │   └── policy-evaluator_test.ts
│   ├── resource/
│   │   ├── types.ts                   # ResourceId, ResourceVersionRow, quality rule types
│   │   ├── quality-rules.ts           # builtin rule evaluators
│   │   └── quality-rules_test.ts
│   ├── message/
│   │   └── types.ts                   # Message shape + helpers
│   ├── storage/
│   │   ├── interfaces.ts              # StorageBackend + all sub-store interfaces
│   │   ├── postgres/
│   │   │   ├── backend.ts             # PostgresStorageBackend factory
│   │   │   ├── template-store.ts
│   │   │   ├── instance-store.ts
│   │   │   ├── cell-store.ts
│   │   │   ├── resource-registry.ts
│   │   │   ├── resource-data-store.ts # InlineJsonbResourceDataStore
│   │   │   ├── event-store.ts
│   │   │   ├── timer-store.ts
│   │   │   └── supervisor-store.ts
│   │   └── postgres/integration_test.ts   # scenario tests per store
│   ├── queue/
│   │   ├── interface.ts               # DispatchQueue
│   │   └── postgres-queue.ts
│   ├── scheduler/
│   │   ├── scheduler.ts               # scheduler loop
│   │   ├── scheduler_test.ts          # unit-ish tests with fakes
│   │   ├── timer-ticker.ts            # cron scheduling logic
│   │   ├── timer-ticker_test.ts
│   │   ├── backoff.ts                 # backoff + jitter computation
│   │   └── backoff_test.ts
│   └── config/
│       └── scheduler.ts               # startScheduler entry point + SchedulerConfig
└── test/
    ├── harness.ts                     # testcontainers Postgres + migration bootstrap
    ├── fakes/
    │   ├── stub-supervisor.ts         # claims dispatch rows, reports outcomes
    │   └── captured-logger.ts
    └── scenarios/
        ├── scheduler-timer-fires_test.ts
        ├── scheduler-dispatch-enqueue_test.ts
        ├── scheduler-heartbeat-loss_test.ts
        ├── scheduler-concurrency-tag-limit_test.ts
        └── scheduler-backoff-respected_test.ts
```

---

## Phase 1: Module scaffold

### Task 1.1: Create `/rimsky/` directory with package metadata

**Files:**
- `/rimsky/package.json`
- `/rimsky/tsconfig.json`
- `/rimsky/vitest.config.ts`
- `/rimsky/.eslintrc.cjs`
- `/rimsky/README.md`

**Steps:**

1. Create `/rimsky/` directory.

2. Write `/rimsky/package.json`:
   ```json
   {
     "name": "@rimsky/core",
     "version": "0.1.0",
     "private": true,
     "type": "module",
     "main": "./dist/index.js",
     "types": "./dist/index.d.ts",
     "scripts": {
       "build": "tsc",
       "dev": "tsc --watch",
       "test": "vitest run",
       "test:watch": "vitest",
       "migrate": "tsx migrations/runner.ts",
       "lint": "eslint src test"
     },
     "dependencies": {
       "pg": "^8.11.3",
       "pino": "^8.17.0",
       "uuid": "^9.0.1",
       "cron-parser": "^4.9.0",
       "zod": "^3.22.4",
       "yaml": "^2.3.4"
     },
     "devDependencies": {
       "@types/node": "^20.10.0",
       "@types/pg": "^8.10.9",
       "@types/uuid": "^9.0.7",
       "typescript": "^5.3.2",
       "vitest": "^1.0.4",
       "testcontainers": "^10.4.0",
       "tsx": "^4.6.0",
       "eslint": "^8.55.0",
       "@typescript-eslint/parser": "^6.13.1",
       "@typescript-eslint/eslint-plugin": "^6.13.1"
     }
   }
   ```

3. Write `/rimsky/tsconfig.json`:
   ```json
   {
     "compilerOptions": {
       "target": "ES2022",
       "module": "NodeNext",
       "moduleResolution": "NodeNext",
       "strict": true,
       "esModuleInterop": true,
       "skipLibCheck": true,
       "forceConsistentCasingInFileNames": true,
       "declaration": true,
       "declarationMap": true,
       "sourceMap": true,
       "outDir": "./dist",
       "rootDir": "./src",
       "resolveJsonModule": true,
       "noUncheckedIndexedAccess": true
     },
     "include": ["src/**/*"],
     "exclude": ["node_modules", "dist", "**/*_test.ts", "test/**"]
   }
   ```

4. Write `/rimsky/vitest.config.ts`:
   ```ts
   import { defineConfig } from "vitest/config";
   export default defineConfig({
     test: {
       include: ["src/**/*_test.ts", "test/**/*_test.ts"],
       testTimeout: 60_000,          // scenario tests need time for testcontainers
       hookTimeout: 60_000,
     },
   });
   ```

5. Write `/rimsky/.eslintrc.cjs`:
   ```js
   module.exports = {
     parser: "@typescript-eslint/parser",
     parserOptions: { ecmaVersion: 2022, sourceType: "module" },
     plugins: ["@typescript-eslint"],
     extends: ["eslint:recommended", "plugin:@typescript-eslint/recommended"],
     rules: {
       "@typescript-eslint/no-unused-vars": ["error", { argsIgnorePattern: "^_" }],
       // Enforce no cross-feature imports by convention; reviewed manually for v1
     },
   };
   ```

6. Write `/rimsky/README.md`:
   ```markdown
   # Rimsky

   Project-agnostic reactive cell-graph orchestration platform.

   See `docs/cell-graph-design.md` and `docs/specs/2026-04-21-rimsky-v1-design.md` (one directory up) for design and spec.

   ## Development

       npm install
       npm test
   ```

7. Run `cd /Users/claude/Documents/projects/zonebase/rimsky && npm install` to verify package.json is well-formed.

**Verification:** `cd rimsky && npx tsc --noEmit` → exits 0 (no source files yet but config loads). `npm install` → completes without error.

---

### Task 1.2: Create source directory skeleton

**Files:** All empty placeholder `.ts` files matching the module layout above.

**Steps:**

1. Create directories `src/shared/`, `src/cell/`, `src/resource/`, `src/message/`, `src/storage/postgres/`, `src/queue/`, `src/scheduler/`, `src/config/`, `migrations/`, `test/fakes/`, `test/scenarios/`.

2. Create a temporary `src/index.ts` with `export {};` so `tsc` has something to compile.

3. Run `cd rimsky && npx tsc --noEmit` — should exit 0.

**Verification:** Directory tree matches the layout section of this plan. `npx tsc --noEmit` passes.

---

## Phase 2: Shared utilities

### Task 2.1: `src/shared/types.ts`

**Files:** `rimsky/src/shared/types.ts`

**Steps:**

1. Write the file:
   ```ts
   export type UUID = string;
   export type CellKind = "deterministic" | "agentic" | "timer";
   export type CellState = "fresh" | "stale" | "running" | "failed";
   export type Severity = "error" | "warning";
   export type BackoffKind = "linear" | "exponential";
   export type JitterKind = "none" | "plus_minus";
   export type AccessKind = "inline" | "sql" | "mcp" | "rest";
   export type MessageType = "invalidate" | "recalculate";

   export type ResourcePath = string[];            // e.g. ["production", "phoenix-az-city", "zoning_districts"]
   export const renderResourcePath = (p: ResourcePath): string => p.join(":");

   export interface RunOutcome {
     result: unknown;
     changed: boolean;
     change_summary: string | null;
   }

   export interface DispatchRow {
     id: UUID;
     cell_id: UUID;
     cell_kind: CellKind;
     concurrency_tags: string[];
     enqueued_at: Date;
     claimed_by: string | null;
     claimed_at: Date | null;
   }
   ```

**Verification:** `npx tsc --noEmit` passes.

---

### Task 2.2: `src/shared/errors.ts`

**Steps:**

1. Write:
   ```ts
   export class RimskyError extends Error {
     constructor(msg: string, public details?: Record<string, unknown>) {
       super(msg);
       this.name = this.constructor.name;
     }
   }

   export class TemplateValidationError extends RimskyError {}
   export class TemplateNotFoundError extends RimskyError {}
   export class InstanceNotFoundError extends RimskyError {}
   export class CellNotFoundError extends RimskyError {}
   export class ConsumerKeyConflictError extends RimskyError {}
   export class TemplateInUseError extends RimskyError {}
   export class CellRunningError extends RimskyError {}

   /**
    * Thrown by a deterministic handler to signal a structured, template-declared failure.
    * The error_class must match an entry in the cell's error_types.
    */
   export class CellApplicationError extends RimskyError {
     constructor(public error_class: string, details?: Record<string, unknown>) {
       super(`Cell application error: ${error_class}`, details);
     }
   }
   ```

**Verification:** `npx tsc --noEmit` passes.

---

### Task 2.3: `src/shared/clock.ts`

**Steps:**

1. Write:
   ```ts
   export interface Clock {
     now(): Date;
     sleep(ms: number): Promise<void>;
   }

   export const SystemClock: Clock = {
     now: () => new Date(),
     sleep: (ms) => new Promise((r) => setTimeout(r, ms)),
   };

   export class ControllableClock implements Clock {
     private t: number;
     private pending: { at: number; resolve: () => void }[] = [];

     constructor(start: Date = new Date(0)) {
       this.t = start.getTime();
     }

     now(): Date {
       return new Date(this.t);
     }

     sleep(ms: number): Promise<void> {
       return new Promise((resolve) => {
         this.pending.push({ at: this.t + ms, resolve });
       });
     }

     async advance(ms: number): Promise<void> {
       const target = this.t + ms;
       let rounds = 0;
       while (rounds < 1000) {
         const nextDue = this.pending
           .filter((p) => p.at <= target)
           .reduce<number | null>((min, p) => (min === null || p.at < min ? p.at : min), null);
         if (nextDue === null) {
           this.t = target;
           for (let i = 0; i < 8; i++) await Promise.resolve();
           if (this.pending.some((p) => p.at <= target)) {
             rounds++;
             continue;
           }
           return;
         }
         this.t = nextDue;
         this.flushDue();
         for (let i = 0; i < 8; i++) await Promise.resolve();
         rounds++;
       }
       throw new Error("ControllableClock.advance: pending sleeps did not stabilize after 1000 rounds");
     }

     async setNow(d: Date): Promise<void> {
       await this.advance(d.getTime() - this.t);
     }

     private flushDue(): boolean {
       const due = this.pending.filter((p) => p.at <= this.t);
       if (due.length === 0) return false;
       this.pending = this.pending.filter((p) => p.at > this.t);
       due.forEach((p) => p.resolve());
       return true;
     }
   }
   ```

2. Write `src/shared/clock_test.ts`:
   ```ts
   import { describe, it, expect } from "vitest";
   import { ControllableClock } from "./clock.js";

   describe("ControllableClock", () => {
     it("advances time", async () => {
       const c = new ControllableClock(new Date(1000));
       expect(c.now().getTime()).toBe(1000);
       await c.advance(500);
       expect(c.now().getTime()).toBe(1500);
     });

     it("resolves pending sleeps when time advances past deadline", async () => {
       const c = new ControllableClock(new Date(0));
       let done = false;
       const p = c.sleep(100).then(() => (done = true));
       await c.advance(99);
       expect(done).toBe(false);
       await c.advance(1);
       await p;
       expect(done).toBe(true);
     });

     it("resolves chained sleeps within one advance", async () => {
       // This simulates a loop that sleeps, wakes, and sleeps again.
       const c = new ControllableClock(new Date(0));
       let iterations = 0;
       (async () => {
         while (iterations < 3) {
           await c.sleep(50);
           iterations++;
         }
       })();
       await c.advance(160);      // should be enough for 3 iterations of 50ms
       expect(iterations).toBe(3);
     });
   });
   ```

**Verification:** `npx vitest run src/shared/clock_test.ts` → passes. `npx tsc --noEmit` passes.

---

### Task 2.4: `src/shared/logger.ts`

**Steps:**

1. Write:
   ```ts
   import { pino, type Logger as PinoLogger } from "pino";

   export interface Logger {
     debug(msg: string, fields?: Record<string, unknown>): void;
     info(msg: string, fields?: Record<string, unknown>): void;
     warn(msg: string, fields?: Record<string, unknown>): void;
     error(msg: string, fields?: Record<string, unknown>): void;
     child(bindings: Record<string, unknown>): Logger;
   }

   export function createPinoLogger(opts?: { level?: string }): Logger {
     const p = pino({ level: opts?.level ?? "info" });
     return wrapPino(p);
   }

   function wrapPino(p: PinoLogger): Logger {
     return {
       debug: (msg, fields) => p.debug(fields ?? {}, msg),
       info: (msg, fields) => p.info(fields ?? {}, msg),
       warn: (msg, fields) => p.warn(fields ?? {}, msg),
       error: (msg, fields) => p.error(fields ?? {}, msg),
       child: (bindings) => wrapPino(p.child(bindings)),
     };
   }

   export const SilentLogger: Logger = {
     debug: () => {},
     info: () => {},
     warn: () => {},
     error: () => {},
     child: () => SilentLogger,
   };

   export class CapturingLogger implements Logger {
     readonly entries: { level: string; msg: string; fields?: Record<string, unknown> }[] = [];
     debug(msg: string, fields?: Record<string, unknown>) { this.entries.push({ level: "debug", msg, fields }); }
     info(msg: string, fields?: Record<string, unknown>) { this.entries.push({ level: "info", msg, fields }); }
     warn(msg: string, fields?: Record<string, unknown>) { this.entries.push({ level: "warn", msg, fields }); }
     error(msg: string, fields?: Record<string, unknown>) { this.entries.push({ level: "error", msg, fields }); }
     child() { return this; }
   }
   ```

**Verification:** `npx tsc --noEmit` passes.

---

## Phase 3: Migrations

### Task 3.1: Write `001-initial.sql`

**Files:** `rimsky/migrations/001-initial.sql`

**Steps:**

1. Copy the full schema from spec §3.1 (all tables from `rimsky_templates` through `rimsky_timers`, including the `rimsky_supervisors` table, all indexes, and the `rimsky_migrations` bookkeeping table).

2. Prepend a `rimsky_migrations` table:
   ```sql
   CREATE TABLE IF NOT EXISTS rimsky_migrations (
       filename    TEXT PRIMARY KEY,
       applied_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
   );
   ```

3. Include every `CREATE TABLE` and `CREATE INDEX` from spec §3.1 verbatim. Verify the final file ends with `rimsky_timers` and its index.

**Verification:** File exists; can be opened and the SQL parses by eye. Syntax will be validated in Task 3.3 when the runner applies it.

---

### Task 3.2: Migration runner

**Files:** `rimsky/migrations/runner.ts`

**Steps:**

1. Write:
   ```ts
   import pg from "pg";
   import fs from "node:fs";
   import path from "node:path";
   import { fileURLToPath } from "node:url";

   const __dirname = path.dirname(fileURLToPath(import.meta.url));

   export async function runMigrations(opts: { connectionString: string; directory?: string }): Promise<string[]> {
     const dir = opts.directory ?? __dirname;
     const client = new pg.Client({ connectionString: opts.connectionString });
     await client.connect();
     try {
       await client.query(`
         CREATE TABLE IF NOT EXISTS rimsky_migrations (
           filename TEXT PRIMARY KEY,
           applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
         )
       `);
       const applied = await client.query<{ filename: string }>("SELECT filename FROM rimsky_migrations");
       const appliedSet = new Set(applied.rows.map((r) => r.filename));
       const files = fs.readdirSync(dir).filter((f) => f.endsWith(".sql")).sort();
       const ran: string[] = [];
       for (const f of files) {
         if (appliedSet.has(f)) continue;
         const sql = fs.readFileSync(path.join(dir, f), "utf8");
         await client.query("BEGIN");
         try {
           await client.query(sql);
           await client.query("INSERT INTO rimsky_migrations (filename) VALUES ($1)", [f]);
           await client.query("COMMIT");
           ran.push(f);
         } catch (e) {
           await client.query("ROLLBACK");
           throw e;
         }
       }
       return ran;
     } finally {
       await client.end();
     }
   }

   if (import.meta.url === `file://${process.argv[1]}`) {
     const conn = process.env.RIMSKY_DB_URL;
     if (!conn) {
       console.error("RIMSKY_DB_URL is required");
       process.exit(1);
     }
     runMigrations({ connectionString: conn })
       .then((ran) => {
         console.log(ran.length > 0 ? `Applied: ${ran.join(", ")}` : "No migrations to apply");
         process.exit(0);
       })
       .catch((e) => {
         console.error(e);
         process.exit(1);
       });
   }
   ```

**Verification:** `npx tsc --noEmit` passes.

---

### Task 3.3: Verify migration applies to a live DB

**Steps:**

1. Start a local Postgres (use docker):
   ```bash
   docker run -d --name rimsky-dev-pg -e POSTGRES_PASSWORD=rimsky -p 5433:5432 postgres:14
   ```

2. Wait a few seconds, then run:
   ```bash
   cd rimsky
   RIMSKY_DB_URL=postgres://postgres:rimsky@localhost:5433/postgres npm run migrate
   ```

3. Expect output: `Applied: 001-initial.sql`.

4. Verify schema:
   ```bash
   docker exec rimsky-dev-pg psql -U postgres -c "\dt rimsky_*"
   ```
   Expect rows for every table declared in §3.1.

5. Running migrate again should say `No migrations to apply`.

6. Tear down: `docker rm -f rimsky-dev-pg`.

**Verification:** All tables exist; re-run is idempotent.

---

## Phase 4: Pure logic components

### Task 4.1: Cell template types — `src/cell/template.ts`

**Steps:**

1. Write TypeScript types mirroring the YAML schema from spec §4.3: `CellTemplateSpec`, `TemplateCellDef`, `ExecutionDeterministic`, `ExecutionAgentic`, `ExecutionTimer`, `ResourceDef`, `QualityRuleDef`, `ErrorTypePolicy`, `PolicyAction` (discriminated union). Use `zod` schemas alongside so we can validate from YAML.

2. Define `type PolicyAction = RetryAction | InvalidateAction | GiveUpAction`. Each carries its specific fields.

3. Export both TS types (for internal use) and zod schemas (for validation).

**Verification:** `npx tsc --noEmit` passes.

---

### Task 4.2: Template validator — `src/cell/template-validator.ts`

**Steps:**

1. Implement `validateTemplate(yamlOrObj: string | unknown): CellTemplateSpec`:
   - Parse YAML if string input.
   - Validate against zod schema.
   - Check: all `dependencies` reference declared cell types; all `error_types[*].policy[*].invalidate.targets` reference declared cell types; timer `execution.emit.target` references declared cell type; no dependency cycles (topological sort); `kind`-specific execution config is well-formed.
   - Throw `TemplateValidationError` with a structured list of issues on failure.

2. Write `src/cell/template-validator_test.ts` with cases:
   - Valid minimal template → passes.
   - Dependency referencing non-existent cell → fails with specific message.
   - Cycle → fails.
   - Invalid policy target → fails.
   - Timer with non-existent `emit.target` → fails.
   - Non-YAML input → fails.

**Verification:** `npx vitest run src/cell/template-validator_test.ts` passes.

---

### Task 4.3: State machine — `src/cell/state-machine.ts`

**Steps:**

1. Define the transition function:
   ```ts
   export type TransitionReason =
     | { kind: "invalidate_received" }
     | { kind: "dispatch_claimed" }
     | { kind: "work_completed" }
     | { kind: "policy_retry" }
     | { kind: "policy_invalidate" }
     | { kind: "policy_give_up" }
     | { kind: "operator_reset" }
     | { kind: "operator_invalidate" };

   export function nextState(current: CellState, reason: TransitionReason): CellState {
     // implement the table from spec §4.1
   }
   ```

2. Write `state-machine_test.ts` covering every row of the transition table, plus illegal transitions (e.g., `fresh` + `work_completed`) throwing.

**Verification:** Tests pass.

---

### Task 4.4: Policy evaluator — `src/cell/policy-evaluator.ts`

**Steps:**

1. Implement `evaluatePolicy(policy: ErrorTypePolicy, cellState: { action_index: number, retry_counter: number, current_error_class: string | null }, error_class: string)`:
   - Returns `{ action: ResolvedAction, updatedState }` where `ResolvedAction` is one of `{ kind: "retry", delay_ms: number, new_state } | { kind: "invalidate", targets, new_state } | { kind: "give_up", reason, new_state }`.
   - Implements the reset-on-different-class behavior from spec §4.2 `on_error`.
   - Computes backoff delay via a helper from `scheduler/backoff.ts` (which Task 4.6 will implement).

2. Write `policy-evaluator_test.ts`:
   - Retry within count → delay advances linearly/exponentially.
   - Retry exhausts → advances `action_index` → next action applied.
   - Same class recurrence → action_index advances.
   - Different class → counters reset.
   - `give_up` → returns give_up action.
   - Missing error class in policy → returns implicit give_up with `unknown_error_class`.

**Verification:** Tests pass.

---

### Task 4.5: Quality rules — `src/resource/quality-rules.ts`

**Steps:**

1. Define `QualityRuleEvaluator = (newData: unknown, previousData: unknown | null) => { passed: boolean; details?: string }`.

2. Implement builtin evaluators:
   - `row_count_ratio` — expects array input; pass if `new.length >= previous.length * min_ratio` or no previous.
   - `no_nulls` — for an array of objects, checks specified fields are non-null in every record.
   - `nullable_fields_present` — checks specified field paths exist in the output schema (or record zero's shape).
   - `custom` — looks up the named handler in a registered map.

3. Export `evaluateQualityRules(rules: QualityRuleDef[], newData, previousData, customRegistry)`:
   - Runs each rule; returns `{ errorFailures: RuleFailure[], warnings: RuleFailure[] }`.

4. Write `quality-rules_test.ts` covering each builtin + severity handling.

**Verification:** Tests pass.

---

### Task 4.6: Backoff helper — `src/scheduler/backoff.ts`

**Steps:**

1. Implement:
   ```ts
   export interface BackoffConfig {
     kind: BackoffKind;
     base_delay_ms: number;
     jitter: JitterKind;
     max_delay_ms: number;
   }

   export function computeBackoffDelay(cfg: BackoffConfig, retry_counter: number, rng: () => number = Math.random): number {
     let delay =
       cfg.kind === "linear"
         ? cfg.base_delay_ms * (retry_counter + 1)
         : cfg.base_delay_ms * Math.pow(2, retry_counter);
     if (cfg.jitter === "plus_minus") {
       delay = delay * (0.5 + rng());
     }
     return Math.min(delay, cfg.max_delay_ms);
   }
   ```

2. Write `backoff_test.ts`:
   - Linear with no jitter → exact multiples.
   - Exponential with no jitter → powers of 2.
   - `plus_minus` jitter with injected rng → deterministic output.
   - Clamp to `max_delay_ms`.

**Verification:** Tests pass.

---

## Phase 5: Messages

### Task 5.1: Message types — `src/message/types.ts`

**Steps:**

1. Write:
   ```ts
   import { UUID, MessageType } from "../shared/types.js";

   export interface InvalidateParams {
     reason: string;
     restore_version?: UUID | "previous" | null;
   }

   export interface RecalculateParams {
     new_version_id: UUID;
   }

   export interface Message {
     id: UUID;
     type: MessageType;
     source_cell_id: UUID | null;
     target_cell_id: UUID;
     occurred_at: Date;
     params: InvalidateParams | RecalculateParams;
   }

   export function isInvalidateParams(p: Message["params"]): p is InvalidateParams {
     return "reason" in p;
   }
   ```

**Verification:** `npx tsc --noEmit` passes.

---

## Phase 6: Storage interfaces

### Task 6.1: Interfaces file — `src/storage/interfaces.ts`

**Steps:**

1. Define the interfaces from spec §12.1, plus each sub-store's method list. Include every operation the scheduler, template validator, and later phases will need. Key interfaces:

   - `TemplateStore` — `deploy`, `get`, `list`, `delete`.
   - `InstanceStore` — `create(template_id, consumer_key, params)`, `get(id_or_key)`, `list(filter)`, `delete(id)`.
   - `CellStore` — `create`, `get`, `listByInstance`, `listReadyForDispatch`, `listRunning`, `updateState`, `updateError` (action_index, retry_counter, current_error_class), `updateHeartbeat`, `setKillRequested`, `clearSupervisorAssignment`, `listWithStaleHeartbeat(cutoff)`, `deleteByInstance`.
   - `ResourceRegistry` — `create`, `get`, `listByOwner`, `commitVersion(resource_id, version_row, newCurrent)`, `noOpCommit(resource_id)`, `gcOldVersions(resource_id, keep)`, `restoreVersion(resource_id, version_id)`.
   - `ResourceDataStore` — `write`, `read`, `delete` (from spec §5.4, rename consistent with refactor).
   - `EventStore` — `append(row)`, `list(filter)`, `tail(cursor, limit)`. Shape of an appended event:
     ```ts
     export interface EventRow {
       instance_id?: UUID | null;       // nullable for system-level events (rare)
       cell_id?: UUID | null;           // nullable for instance-level events (rare)
       kind: string;                    // one of the kinds enumerated in spec §3.2
       payload: Record<string, unknown>;
       occurred_at?: Date;              // defaulted to clock.now() if omitted
     }
     ```
     `list(filter)` supports filters: `{ cell_id?, instance_id?, kind?, since?, until? }`, returning `{ events: EventRow[]; next_cursor: string | null }`. When the caller provides only `cell_id`, the Postgres implementation derives `instance_id` automatically (either via a JOIN on insert, or a trigger, or by having the caller resolve it — implementer's choice; the invariant is that the row's `instance_id` column is populated whenever a `cell_id` is, so the `(instance_id, occurred_at)` index remains useful for per-instance queries).
   - `TimerStore` — `register`, `dueBefore(cutoff)`, `recordFired(cell_id, next_fire_at)`, `listAll`.
   - `SupervisorStore` — methods from spec §12.1.

2. All methods return `Promise<...>`. All writes take optional `(tx?: StorageTx)` parameter for transactional composition.

3. Define:
   ```ts
   export interface StorageTx {
     readonly _brand: "StorageTx";
   }

   export interface StorageBackend {
     templates: TemplateStore;
     instances: InstanceStore;
     cells: CellStore;
     resources: ResourceRegistry;
     resourceData: ResourceDataStore;
     events: EventStore;
     timers: TimerStore;
     supervisors: SupervisorStore;
     transaction<T>(fn: (tx: StorageTx) => Promise<T>): Promise<T>;
   }
   ```

**Verification:** `npx tsc --noEmit` passes.

---

### Task 6.2: Postgres backend factory — `src/storage/postgres/backend.ts`

**Steps:**

1. Write the factory:
   ```ts
   import pg from "pg";
   import { StorageBackend, StorageTx } from "../interfaces.js";
   import { Clock } from "../../shared/clock.js";
   import { Logger } from "../../shared/logger.js";
   // ... imports for each sub-store

   export interface PostgresStorageConfig {
     pool: pg.Pool;
     clock: Clock;
     logger: Logger;
   }

   class PgTx implements StorageTx {
     readonly _brand = "StorageTx" as const;
     constructor(public client: pg.PoolClient) {}
   }

   export function createPostgresStorage(cfg: PostgresStorageConfig): StorageBackend {
     const { pool, clock, logger } = cfg;
     const templates = new PostgresTemplateStore(pool, clock);
     // ... construct each store
     return {
       templates,
       // ...
       transaction: async <T>(fn: (tx: StorageTx) => Promise<T>): Promise<T> => {
         const client = await pool.connect();
         try {
           await client.query("BEGIN");
           const tx = new PgTx(client);
           const result = await fn(tx);
           await client.query("COMMIT");
           return result;
         } catch (e) {
           await client.query("ROLLBACK");
           throw e;
         } finally {
           client.release();
         }
       },
     };
   }
   ```

2. Each store's methods accept an optional `tx?: StorageTx` and run queries via either `pool.query` or `(tx as PgTx).client.query` — a helper function selects the right executor.

**Verification:** `npx tsc --noEmit` passes after sub-stores exist (Task 6.3+).

---

### Task 6.3–6.10: Implement each Postgres sub-store

Each of these tasks follows the same pattern:

**Files:** `src/storage/postgres/<store>.ts`

**Steps (per store):**

1. Import the interface.
2. Implement each method with a prepared SQL statement matching the schema in spec §3.1.
3. For writes that emit events, take an optional `tx` argument and defer to the event store when an `EventStore.append` call is needed — but do NOT cross-call between stores in this task; that's the scheduler's job in Phase 7. Each store does only its own table.
4. Use UUIDs from `crypto.randomUUID()` where the schema expects UUID.
5. Every query result is mapped to the TypeScript row shape; null-vs-undefined converted at the boundary.

**Specific stores:**

- 6.3: `template-store.ts` — deploy (INSERT + `UNIQUE (name, version)` conflict → return existing), get, list, delete (throws `TemplateInUseError` if instances reference it).
- 6.4: `instance-store.ts` — create (INSERT into `rimsky_instances`), get by id or by `(template_id, consumer_key)`, list, delete (cascades via FK).
- 6.5: `cell-store.ts` — methods enumerated in 6.1. The `listReadyForDispatch` query needs to find cells where `state='stale'` AND all dependency cells are `state='fresh'` AND no existing dispatch row — SQL uses `NOT EXISTS` subqueries.
- 6.6: `resource-registry.ts` — all methods from 6.1. `commitVersion` takes the version row + updates `current_version_id`/`previous_version_id` in a transaction; `noOpCommit` is a no-op returning the existing version id; `gcOldVersions` deletes rows where `committed_at` is older than the Nth-most-recent.
- 6.7: `resource-data-store.ts` — `InlineJsonbResourceDataStore` (writes to `rimsky_resource_versions.data` column; `data_ref` stays null).
- 6.8: `event-store.ts` — `append(evt)` inserts; `list(filter)` with cursor pagination by `id`; `tail(cursor, limit)` returns `{ events, next_cursor }`.
- 6.9: `timer-store.ts` — `register` inserts; `dueBefore(cutoff)` selects rows with `next_fire_at <= cutoff`; `recordFired` updates; `listAll` for debugging.
- 6.10: `supervisor-store.ts` — register (upsert), heartbeat (UPDATE), list, listStale, unregister. **Note on Plan A:** no scheduler code in Plan A *calls* this store (supervisors register themselves in Plan B). The integration test in Task 6.11 exercises it directly. It is included in Plan A so Plan B's supervisor implementation has a stable interface to target without reaching back into Plan A scope. The scheduler's heartbeat-loss sweep operates on `cells.listWithStaleHeartbeat` (per-cell granularity, which is what the spec §8.1 step 4 prescribes); `SupervisorStore.listStale` is for operator tooling and will be used by Plan C's control API.

**Verification per store:** After all stores exist, `npx tsc --noEmit` passes. Integration tests come in Task 6.11.

---

### Task 6.11: Storage integration tests — `src/storage/postgres/integration_test.ts`

**Files:** `rimsky/src/storage/postgres/integration_test.ts`, `rimsky/test/harness.ts` (preliminary — enhanced in Phase 10).

**Steps:**

1. Write `test/harness.ts`:
   ```ts
   import { GenericContainer, StartedTestContainer } from "testcontainers";
   import pg from "pg";
   import { runMigrations } from "../migrations/runner.js";
   import { createPostgresStorage } from "../src/storage/postgres/backend.js";
   import { SystemClock } from "../src/shared/clock.js";
   import { SilentLogger } from "../src/shared/logger.js";

   export interface TestHarness {
     storage: ReturnType<typeof createPostgresStorage>;
     pool: pg.Pool;
     shutdown: () => Promise<void>;
   }

   export async function startTestHarness(): Promise<TestHarness> {
     const container = await new GenericContainer("postgres:14")
       .withEnvironment({ POSTGRES_PASSWORD: "rimsky" })
       .withExposedPorts(5432)
       .start();
     const url = `postgres://postgres:rimsky@${container.getHost()}:${container.getMappedPort(5432)}/postgres`;
     await runMigrations({ connectionString: url });
     const pool = new pg.Pool({ connectionString: url });
     const storage = createPostgresStorage({ pool, clock: SystemClock, logger: SilentLogger });
     return {
       storage,
       pool,
       shutdown: async () => {
         await pool.end();
         await container.stop();
       },
     };
   }
   ```

2. Write `integration_test.ts` with `beforeAll`/`afterAll` spinning up one harness per test file, then scenarios:
   - Deploy a template → list returns it → get by id returns the spec → delete works.
   - Create instance with valid consumer_key → get by key and by id both work; duplicate consumer_key → `ConsumerKeyConflictError`.
   - Create cell → listByInstance returns it.
   - CellStore: mark cell running, update heartbeat, listWithStaleHeartbeat returns it after cutoff.
   - ResourceRegistry: commitVersion → current_version_id updates; second commit → previous_version_id tracks prior; gcOldVersions respects `keep_versions`.
   - EventStore: append N events → list with cursor returns them paginated.
   - TimerStore: register with future next_fire_at → dueBefore(cutoff in past) empty; dueBefore(cutoff in future) returns it.
   - SupervisorStore: register → heartbeat → listStale finds when past cutoff.
   - Transaction: failing callback rolls back inserts.

**Verification:** `npx vitest run src/storage/postgres/integration_test.ts` — all scenarios pass. Container starts/stops cleanly.

---

## Phase 7: Dispatch queue

### Task 7.1: Queue interface — `src/queue/interface.ts`

**Steps:**

1. Write:
   ```ts
   import { UUID, CellKind, DispatchRow } from "../shared/types.js";

   export interface DispatchRequest {
     cell_id: UUID;
     cell_kind: CellKind;
     concurrency_tags: string[];
     enqueued_at: Date;           // may be future-dated for backoff
   }

   export interface DispatchQueue {
     enqueue(req: DispatchRequest): Promise<void>;
     claim(
       supervisorId: string,
       accepts: CellKind[],
       limits: Record<string, number>
     ): Promise<DispatchRow | null>;
     complete(dispatchId: UUID): Promise<void>;
     fail(dispatchId: UUID, reason: string): Promise<void>;
     removeForCell(cell_id: UUID): Promise<void>;  // used on invalidate to clear pending row
   }
   ```

**Verification:** `npx tsc --noEmit` passes.

---

### Task 7.2: Postgres queue — `src/queue/postgres-queue.ts`

**Steps:**

1. Write `PostgresDispatchQueue` implementing `DispatchQueue`. Use the illustrative SQL from spec §8.2 as a starting point; the tag-counts CTE must run in the same transaction as the `UPDATE ... RETURNING`. `SELECT FOR UPDATE SKIP LOCKED` on `rimsky_dispatch`.

2. `enqueue` honors the `UNIQUE (cell_id)` constraint with this exact semantic — **do not overwrite backoff-future-dated rows**:
   ```sql
   INSERT INTO rimsky_dispatch (id, cell_id, cell_kind, concurrency_tags, enqueued_at)
   VALUES ($1, $2, $3, $4, $5)
   ON CONFLICT (cell_id) DO UPDATE
     SET enqueued_at = EXCLUDED.enqueued_at,
         cell_kind = EXCLUDED.cell_kind,
         concurrency_tags = EXCLUDED.concurrency_tags
     WHERE rimsky_dispatch.claimed_by IS NULL
       AND rimsky_dispatch.enqueued_at <= NOW()      -- never clobber a live backoff row
   ```
   Semantics: if a pending row exists that is *eligible to claim now* (not future-dated), overwrite it (latest enqueue wins among already-ready rows). If a future-dated row exists (a backoff row), the conflict update matches no rows — the new INSERT is discarded and the backoff is preserved. If a claimed row exists, the update predicate fails and the new enqueue is discarded (a running cell doesn't get re-queued until it completes and the row is deleted).

3. `claim`: the SQL from spec §8.2 with `$tag_limits` passed as JSONB.

4. `complete`, `fail`: DELETE the dispatch row by id.

5. `removeForCell`: DELETE rows matching the cell_id. Used by the scheduler when a cell is invalidated.

6. Add integration tests at `src/queue/postgres-queue_test.ts`:
   - enqueue + claim → returns row; re-claim → null (already claimed).
   - enqueue two, claim → returns oldest.
   - enqueue future-dated → claim returns null until NOW() passes the enqueue time.
   - enqueue with tag `t=1` already at limit → claim returns null.
   - complete → row deleted; next enqueue to same cell succeeds.

**Verification:** `npx vitest run src/queue/postgres-queue_test.ts` passes.

---

## Phase 8: Scheduler

### Task 8.1: Timer ticker — `src/scheduler/timer-ticker.ts`

**Steps:**

1. Import `cron-parser`. Export:
   ```ts
   export function nextFireAt(cronExpr: string, from: Date): Date {
     const iter = CronParser.parseExpression(cronExpr, { currentDate: from, utc: true });
     return iter.next().toDate();
   }

   export async function processTimers(opts: {
     storage: StorageBackend;
     messaging: MessageDispatcher;
     clock: Clock;
     logger: Logger;
   }): Promise<number> {
     const { storage, messaging, clock, logger } = opts;
     const now = clock.now();
     const due = await storage.timers.dueBefore(now);
     for (const t of due) {
       await storage.events.append({
         kind: "timer_fired",
         cell_id: t.cell_id,
         payload: { timer_cell_id: t.cell_id, target_cell_id: t.target_cell_id },
       });
       await messaging.emitInvalidate({
         source_cell_id: t.cell_id,
         target_cell_id: t.target_cell_id,
         reason: t.reason ?? "timer_fired",
       });
       await storage.timers.recordFired(t.cell_id, nextFireAt(t.schedule_cron, now));
     }
     return due.length;
   }
   ```

2. `MessageDispatcher` is a small helper used by Scheduler (defined in Task 8.3). For the unit test, inject a fake.

3. Write unit test `timer-ticker_test.ts` using `ControllableClock`, a fake `storage.timers`, a fake `messaging`:
   - Nothing due → returns 0.
   - One due → invalidate emitted; recordFired called with next fire time.

**Verification:** Tests pass.

---

### Task 8.2: Scheduler module — `src/scheduler/scheduler.ts`

**Steps:**

1. Define `SchedulerConfig`:
   ```ts
   export interface SchedulerConfig {
     storage: StorageBackend;
     queue: DispatchQueue;
     clock: Clock;
     logger: Logger;
     tickIntervalMs: number;        // default 1500
     heartbeatTimeoutMs: number;    // default 15000
     concurrencyLimits: Record<string, number>;
   }
   ```

2. Define the loop:
   ```ts
   export interface SchedulerHandle {
     shutdown(): Promise<void>;
   }

   export function startScheduler(cfg: SchedulerConfig): SchedulerHandle {
     let stopped = false;
     const log = cfg.logger.child({ component: "scheduler" });

     async function tick() {
       // 1. Process timers
       await processTimers({ storage: cfg.storage, messaging: dispatcher, clock: cfg.clock, logger: log });
       // 2. Supervisor-health sweep: cells with stale heartbeat
       const cutoff = new Date(cfg.clock.now().getTime() - cfg.heartbeatTimeoutMs);
       const stale = await cfg.storage.cells.listWithStaleHeartbeat(cutoff);
       for (const cell of stale) {
         await cfg.storage.events.append({
           cell_id: cell.id,
           kind: "heartbeat_lost",
           payload: { supervisor_id: cell.assigned_supervisor_id, last_heartbeat_at: cell.last_heartbeat_at },
         });
         await cfg.storage.cells.clearSupervisorAssignment(cell.id);
         // Re-enqueue dispatch row (no retry_counter increment — infra event)
         await cfg.queue.enqueue({
           cell_id: cell.id,
           cell_kind: cell.kind,
           concurrency_tags: cell.concurrency_tags,
           enqueued_at: cfg.clock.now(),
         });
       }
       // 3. Ready cells: find newly-stale cells with all deps fresh
       const ready = await cfg.storage.cells.listReadyForDispatch();
       for (const cell of ready) {
         await cfg.queue.enqueue({
           cell_id: cell.id,
           cell_kind: cell.kind,
           concurrency_tags: cell.concurrency_tags,
           enqueued_at: cfg.clock.now(),
         });
       }
     }

     async function loop() {
       while (!stopped) {
         try {
           await tick();
         } catch (e) {
           log.error("scheduler tick failed", { error: String(e) });
         }
         await cfg.clock.sleep(cfg.tickIntervalMs);
       }
     }

     const dispatcher: MessageDispatcher = {
       emitInvalidate: async ({ source_cell_id, target_cell_id, reason, restore_version }) => {
         // On invalidate: transition target to stale, log, propagate to dependents.
         await invalidateCell(cfg.storage, cfg.queue, cfg.clock, { source_cell_id, target_cell_id, reason, restore_version });
       },
     };

     loop();

     return {
       shutdown: async () => {
         stopped = true;
       },
     };
   }
   ```

3. Implement `invalidateCell` helper in `src/scheduler/invalidate.ts`. Plan A scope for this helper:
   - If target is already `stale` or `running`: no-op (but still log `message_received` for audit).
   - If target is `fresh` or `failed`: transition to `stale` (log `state_transition`), remove any existing dispatch row for this cell via `queue.removeForCell(cell_id)`, emit `invalidate` to all dependents (recursive `invalidateCell` calls), and enqueue a fresh dispatch row **only if** dependencies are all fresh (otherwise the ready-sweep will pick it up when deps resolve).
   - Handle `restore_version`: when `params.restore_version` is set, call `storage.resources.restoreVersion(...)` for each owned resource using either the named version id or `"previous"` (resolved inside the registry). After a successful restore, emit `recalculate` to dependents instead of `invalidate`, and return to `fresh` — do NOT re-enqueue for execution.
   - Log every action as `message_received` / `state_transition` / `message_emitted` events.

   Does not implement: quality rule evaluation, commit flow, error-class handling. Those are scheduler-invoked via `on_work_complete` (Plan B) or via policy chain (Plan B). Plan A's `invalidateCell` covers only the cascade-and-state-transition aspect.

4. Write `scheduler_test.ts` with in-memory fakes for storage and queue:
   - Tick with no activity → no enqueues.
   - Tick with a ready cell → dispatch row enqueued.
   - Tick with a stale-heartbeat cell → supervisor cleared, dispatch re-enqueued.
   - Tick with a due timer → invalidate emitted.

**Verification:** Tests pass.

---

### Task 8.3: `startScheduler` entry point — `src/config/scheduler.ts`

**Steps:**

1. Re-export `startScheduler` from `scheduler/scheduler.ts`. Provide default values for `tickIntervalMs` (1500) and `heartbeatTimeoutMs` (15000).

2. Add `src/index.ts` re-exports for the library's public surface: `startScheduler`, `createPostgresStorage`, `PostgresDispatchQueue`, types.

**Verification:** `npx tsc --noEmit` passes.

---

## Phase 9: Scenario test harness + scheduler scenarios

### Task 9.1: Enhance `test/harness.ts`

**Steps:**

1. Add `startScheduler` wiring to the harness:
   ```ts
   export interface ScenarioHarness extends TestHarness {
     clock: ControllableClock;
     queue: PostgresDispatchQueue;
     startScheduler: (overrides?: Partial<SchedulerConfig>) => SchedulerHandle;
   }

   export async function startScenarioHarness(): Promise<ScenarioHarness> {
     // ... container + migrations + pool
     const clock = new ControllableClock(new Date(2026, 0, 1));
     const storage = createPostgresStorage({ pool, clock, logger });
     const queue = new PostgresDispatchQueue(pool);
     let schedulerHandle: SchedulerHandle | null = null;
     return {
       // ...
       clock,
       queue,
       startScheduler: (overrides) => {
         schedulerHandle = startScheduler({ storage, queue, clock, logger, tickIntervalMs: 50, heartbeatTimeoutMs: 500, concurrencyLimits: {}, ...overrides });
         return schedulerHandle;
       },
       shutdown: async () => {
         await schedulerHandle?.shutdown();
         await pool.end();
         await container.stop();
       },
     };
   }
   ```

2. Add a helper `deployTemplateFromYaml(storage, yamlText): Promise<UUID>` used by scenarios.

3. Add a helper `createInstance(storage, queue, template_id, consumer_key, params): Promise<{ instance_id, cells: CellRow[] }>` that does the instantiation from spec §4.5.

**Verification:** Harness compiles; quick smoke test (scenario that just starts and shuts down) passes.

---

### Task 9.2: Stub supervisor — `test/fakes/stub-supervisor.ts`

**Steps:**

1. Write a minimal in-test "supervisor" that:
   - Polls the dispatch queue at a configurable interval.
   - Claims rows using a provided `accepts` list.
   - Reports outcomes via a callback the test provides (e.g., `(cell_id) => Promise<RunOutcome | { error_class: string }>`).
   - On success: updates cell state to `fresh`, commits resource version (calls `on_work_complete`), calls `queue.complete`.
   - On error: updates cell error tracking, transitions to `stale` or `failed` per policy, calls `queue.complete`.
   - Writes heartbeat to the cell row while "running."

2. This stub is enough to test scheduler behavior without implementing the real supervisor (which is Plan B).

**Verification:** `npx tsc --noEmit` passes.

---

### Task 9.3: Scenario — timer fires invalidate

**Files:** `test/scenarios/scheduler-timer-fires_test.ts`

**Steps:**

1. Write test:
   ```ts
   it("timer cell fires invalidate on target when schedule elapses", async () => {
     const h = await startScenarioHarness();
     try {
       // Deploy template: one timer cell + one deterministic target cell
       const tplId = await deployTemplateFromYaml(h.storage, /* YAML with timer every minute */);
       const { cells } = await createInstance(h.storage, h.queue, tplId, "test-instance", {});
       const timerCell = cells.find((c) => c.cell_type === "ticker")!;
       const targetCell = cells.find((c) => c.cell_type === "target")!;
       // Target is initially stale (first-run) — mark fresh for this test
       await h.storage.cells.updateState(targetCell.id, "fresh", { kind: "work_completed" });

       h.startScheduler();
       await h.clock.advance(70_000);   // cross 1-minute cron boundary
       await waitFor(() => h.storage.events.list({ cell_id: targetCell.id, kind: "message_received" }), (rows) => rows.length > 0);

       const target = await h.storage.cells.get(targetCell.id);
       expect(target.state).toBe("stale");
     } finally {
       await h.shutdown();
     }
   });
   ```

2. `waitFor` is a small polling helper (max 5s wall-clock, 20ms interval) used because the scheduler loop is async-advancing the controllable clock.

**Verification:** Test passes.

---

### Task 9.4: Scenario — dispatch enqueued when deps satisfied

**Files:** `test/scenarios/scheduler-dispatch-enqueue_test.ts`

**Steps:**

1. Deploy two-cell template (A → B). Instantiate. Mark A fresh manually. Expect scheduler to enqueue a dispatch row for B within a few ticks.

2. Assert: `SELECT * FROM rimsky_dispatch WHERE cell_id = $1` returns one row.

**Verification:** Test passes.

---

### Task 9.5: Scenario — heartbeat loss re-enqueues

**Files:** `test/scenarios/scheduler-heartbeat-loss_test.ts`

**Steps:**

1. Deploy a one-cell template. Instantiate. Manually put the cell in `running` state with `last_heartbeat_at = clock.now()` and `assigned_supervisor_id = "dead-supervisor"`.

2. Start scheduler with `heartbeatTimeoutMs: 500`. Advance clock by 1000ms.

3. Assert: `heartbeat_lost` event appended; dispatch row re-enqueued; `assigned_supervisor_id` cleared on the cell.

**Verification:** Test passes.

---

### Task 9.6: Scenario — concurrency tag limit respected

**Files:** `test/scenarios/scheduler-concurrency-tag-limit_test.ts`

**Steps:**

1. Deploy a template whose deterministic cells carry `concurrency_tags: ["scarce-resource"]`.

2. Create two instances. Both have cells with `kind: deterministic` and the same tag.

3. Start a stub supervisor with `accepts: ["deterministic"]` and set scheduler `concurrencyLimits: { "scarce-resource": 1 }`.

4. Enqueue both cells' dispatch rows directly (or drive via scheduler ticks). Have the stub supervisor call `queue.claim` twice in quick succession.

5. Assertions:
   - First `claim` returns one of the two rows (and the stub supervisor marks the cell `running`, writing a heartbeat so that the tag count reflects 1 in-flight).
   - Second `claim` returns `null` — because the tag limit of 1 is hit (a running cell already holds the tag).
   - Stub supervisor completes the first cell (transitions back to `fresh`, calls `queue.complete(dispatch_id)`). After this, the tag count drops to 0.
   - Next `claim` returns the second row.

**Verification:** Test passes.

---

### Task 9.7: Scenario — backoff respected

**Files:** `test/scenarios/scheduler-backoff-respected_test.ts`

**Steps:**

1. Deploy a template with a retry policy. Instantiate. Manually enqueue a dispatch row with `enqueued_at = clock.now() + 2000ms` (future).

2. Start a stub supervisor. Call `claim` — returns null.

3. Advance clock by 3000ms. Call `claim` — returns the row.

**Verification:** Test passes.

---

## Phase 10: Definition of done

### Task 10.1: Final verification

**Steps:**

1. `cd rimsky && npm run build` → exits 0 (full TypeScript compile).

2. `npm test` → all tests pass (unit + integration + scenarios).

3. `npm run lint` → no errors.

4. `RIMSKY_DB_URL=... npm run migrate` against a fresh Postgres instance → applies 001-initial.sql, idempotent on re-run.

5. `src/index.ts` exports: `startScheduler`, `createPostgresStorage`, `PostgresDispatchQueue`, `runMigrations`, `Clock`, `Logger`, `SystemClock`, `ControllableClock`, `createPinoLogger`, `SilentLogger`, and all relevant types.

6. `docs/cell-graph-design.md` and `docs/specs/2026-04-21-rimsky-v1-design.md` are unchanged (this plan only adds new files under `/rimsky/`).

7. Update `CHANGELOG.md` (one directory up at repo root) with an entry:
   ```
   - Added rimsky platform foundation: storage, queue, state machine, policy evaluator, scheduler process. See /rimsky/ and docs/plans/2026-04-21-rimsky-plan-a-foundation-scheduler.md.
   ```

**Deliverables at this point:**
- `/rimsky/` is a self-contained library with migrations, storage, queue, pure-logic components, and a scheduler process.
- Scheduler can be started against a Postgres DB, ticks the timer + supervisor-health + ready-cell sweeps, and enqueues dispatch rows.
- Scenario tests cover all reactive behaviors that don't require a real supervisor.
- Stub supervisor exists in `test/fakes/` for Plan B to replace with the real one.

Plan B (deterministic supervisor) can begin once this plan's tests are green.

---

## Notes for the implementer

- **Do not cross-feature import.** `cell/` never imports from `scheduler/`; `storage/` never imports from `cell/`. Shared types live in `shared/`. If you feel a pull to break this rule, stop and put the shared thing in `shared/`.
- **Tests co-located as `*_test.ts`.** Scenario tests in `test/scenarios/` because they're cross-feature.
- **Every migration is forward-only and idempotent (via `CREATE TABLE IF NOT EXISTS` where applicable, and the `rimsky_migrations` tracking table).**
- **All SQL goes in `src/storage/postgres/`** — no SQL literals in other feature folders.
- **Transactions**: use `storage.transaction(tx => ...)` when multiple writes must be atomic. Individual store methods take optional `tx`.
- **IDs**: use `crypto.randomUUID()` at the boundary where a new UUID is minted. Don't generate UUIDs deep in helpers.
- **Errors**: throw typed errors from `shared/errors.ts`; don't use generic `Error` outside low-level utilities.
- **Logging levels**: `debug` for verbose tick-level traces, `info` for lifecycle events (scheduler started/stopped), `warn` for recoverable issues, `error` for tick failures.
- **The scheduler's loop should be robust to individual tick failures** — a thrown exception in tick must be caught and logged, then the loop continues.
- **Clock interaction in scenarios.** The scheduler loop calls `cfg.clock.sleep(tickIntervalMs)` between ticks. `ControllableClock.advance(ms)` resolves due sleeps in rounds, yielding microtasks between rounds so that loop iterations can re-register their next sleep before `advance` returns. Scenarios should `await h.clock.advance(...)` rather than call it synchronously. Scenario tests still use a short wall-clock `waitFor` polling helper as a belt-and-suspenders safety net for database writes that happen asynchronously within an iteration.
