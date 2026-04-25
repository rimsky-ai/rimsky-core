# Rimsky Plan B — Deterministic Supervisor + End-to-End Execution

**Goal:** Build a real `Supervisor` process that claims dispatch rows from the queue, executes deterministic cells via a registered handler registry, writes heartbeats, polls `kill_requested`, wires `on_work_complete` and `on_error` against real storage, and runs the full deterministic-cell scenario suite end-to-end.

**Architecture:** Supervisor is a long-running process (library entry point `startSupervisor(config)`). It is a 1:N host — one process, configurable `concurrency` of concurrent cells, configurable `accepts: CellKind[]`. Each claim pulls one cell; deterministic cells run inline as async handler invocations. No subprocess spawning in this plan (agentic path is Plan C).

**Tech stack:** Same as Plan A (Node 20+, TypeScript, Postgres via `pg`, vitest, testcontainers). Plan A artifacts are prerequisites.

**Prerequisites:**
- Plan A delivered: `/rimsky/` module, storage interfaces + Postgres impls, dispatch queue, scheduler, state machine, policy evaluator, template validator, quality rules, `ControllableClock`, `SilentLogger`, scenario harness.
- `npm test` in `/rimsky/` passes on Plan A's scenarios.

**Reference documents:**
- Spec: `docs/specs/2026-04-21-rimsky-v1-design.md`, particularly §4.2, §6, §7, §9 (non-agentic portions), §12.
- Design: `docs/cell-graph-design.md`.
- Plan A (for context): `docs/plans/2026-04-21-rimsky-plan-a-foundation-scheduler.md`.

---

## New/changed files this plan produces

```
rimsky/src/
├── supervisor/
│   ├── types.ts                       # SupervisorConfig, HandlerRegistry, SupervisorHandle
│   ├── supervisor.ts                  # the supervisor loop (deterministic-only in Plan B)
│   ├── supervisor_test.ts             # unit-ish tests with fakes
│   ├── handler-registry.ts            # HandlerRegistry impl
│   ├── handler-registry_test.ts
│   ├── deterministic-runner.ts        # in-process deterministic execution + on_work_complete wiring
│   ├── deterministic-runner_test.ts
│   ├── on-error.ts                    # error handling path: policy lookup, dispatch re-enqueue, state transitions
│   ├── on-error_test.ts
│   └── commit.ts                      # quality-rule evaluation + resource commit flow
│       └── commit_test.ts
├── config/
│   └── supervisor.ts                  # startSupervisor entry point + SupervisorConfig wiring
└── index.ts                           # add startSupervisor to public exports

rimsky/test/
├── harness.ts                         # extend to start supervisors as well
├── fixtures/
│   └── templates/
│       ├── happy-path.yaml
│       ├── retry-then-succeed.yaml
│       ├── cascade-invalidate.yaml
│       ├── give-up.yaml
│       ├── double-buffering.yaml
│       ├── no-op-commit.yaml
│       └── rollback.yaml
└── scenarios/
    ├── deterministic-happy-path_test.ts
    ├── deterministic-retry-then-succeed_test.ts
    ├── deterministic-cascade-invalidate_test.ts
    ├── deterministic-give-up_test.ts
    ├── deterministic-double-buffering_test.ts
    ├── deterministic-no-op-commit_test.ts
    ├── deterministic-rollback_test.ts
    └── deterministic-concurrency-respected_test.ts
```

The stub supervisor from Plan A (`test/fakes/stub-supervisor.ts`) is **replaced** in scenarios by the real supervisor. The stub file itself stays for unit-style tests that want a fake; scenarios shift to the real implementation.

---

## Phase 0: Amendments to Plan A

Plan B requires two capabilities from Plan A's surface that Plan A did not expose. These are small, backward-compatible additions — they do not change any behavior Plan A's tests verify, so Plan A's test suite must stay green after these edits.

### Task 0.1: Add `CellStore.listDependentsOf(cell_id)` to Plan A's `CellStore`

**Files touched:**
- `rimsky/src/storage/interfaces.ts` (add method to `CellStore` interface)
- `rimsky/src/storage/postgres/cell-store.ts` (implement)
- `rimsky/src/storage/postgres/integration_test.ts` (add a test row)

**Steps:**

1. Add to `CellStore` interface:
   ```ts
   listDependentsOf(cell_id: UUID, tx?: StorageTx): Promise<CellRow[]>;
   ```

2. Implement in the Postgres cell store:
   ```sql
   SELECT * FROM rimsky_cells WHERE $1 = ANY(dependencies)
   ```

3. Add an integration test: create cells A → B → C; `listDependentsOf(A.id)` returns `[B]`; `listDependentsOf(B.id)` returns `[C]`; `listDependentsOf(C.id)` returns `[]`.

**Verification:** Plan A's existing storage integration tests still pass. New test passes.

---

### Task 0.2: Implement `src/scheduler/recalculate.ts`

**Goal:** Shared helper used by both the scheduler's ready-sweep and the supervisor's commit path to deliver `recalculate` semantics per spec §5.2 and §4.2 `on_recalculate`.

**Rationale:** Plan A's `invalidateCell` restore-version path (Task 8.2 step 3) "emits recalculate to dependents" without a concrete helper. Plan A's ready-sweep in the scheduler loop implicitly handles the case (stale cells with fresh deps get enqueued), but there is no synchronous helper that captures the semantics. Plan B introduces it, and the `invalidateCell` restore path updates to call it.

**Files:**
- `rimsky/src/scheduler/recalculate.ts` (new)
- `rimsky/src/scheduler/recalculate_test.ts`
- `rimsky/src/scheduler/invalidate.ts` (update to call `recalculateCell` on the restore path)

**Steps:**

1. Implement:
   ```ts
   import { StorageBackend } from "../storage/interfaces.js";
   import { DispatchQueue } from "../queue/interface.js";
   import { Clock } from "../shared/clock.js";
   import { UUID } from "../shared/types.js";

   export async function recalculateCell(opts: {
     storage: StorageBackend;
     queue: DispatchQueue;
     clock: Clock;
     target_cell_id: UUID;
     source_cell_id: UUID | null;
     new_version_id?: UUID;
   }): Promise<void> {
     const { storage, queue, clock, target_cell_id, source_cell_id, new_version_id } = opts;
     // Always log the receipt.
     await storage.events.append({
       cell_id: target_cell_id,
       kind: "message_received",
       payload: { type: "recalculate", source_cell_id, new_version_id },
     });
     const target = await storage.cells.get(target_cell_id);
     if (!target) return;
     if (target.state === "fresh" || target.state === "running" || target.state === "failed") {
       return;                                    // per spec §5.2: no-op for these states
     }
     // state === "stale": check deps
     const deps = await Promise.all(target.dependencies.map((d) => storage.cells.get(d)));
     if (deps.some((d) => !d || d.state !== "fresh")) {
       return;                                    // not ready yet; will be nudged again
     }
     // All deps fresh → enqueue dispatch row now
     await queue.enqueue({
       cell_id: target.id,
       cell_kind: target.kind,
       concurrency_tags: target.concurrency_tags,
       enqueued_at: clock.now(),
     });
   }
   ```

2. Update `src/scheduler/invalidate.ts`: on the `restore_version` branch (where the cell's resources swap back to a previous version without re-executing), emit `recalculate` to dependents by calling `recalculateCell` for each dependent. Use `storage.cells.listDependentsOf(cell_id)` (from Task 0.1).

3. Write `recalculate_test.ts`:
   - Target `fresh` → no-op (only `message_received` event appended).
   - Target `stale` with all deps fresh → dispatch row enqueued.
   - Target `stale` with at least one dep stale → no dispatch row.
   - Target `failed` → no-op.
   - Target `running` → no-op.

4. Run Plan A's full scenario suite to verify nothing regressed after the `invalidate.ts` update.

**Verification:** New tests pass; Plan A's scenario suite still passes.

---

## Phase 1: Supervisor types and handler registry

### Task 1.1: `src/supervisor/types.ts`

**Files:** `rimsky/src/supervisor/types.ts`

**Steps:**

1. Write the type definitions:
   ```ts
   import { Clock } from "../shared/clock.js";
   import { Logger } from "../shared/logger.js";
   import { StorageBackend } from "../storage/interfaces.js";
   import { DispatchQueue } from "../queue/interface.js";
   import { CellKind, UUID, RunOutcome } from "../shared/types.js";

   export interface DeterministicHandlerCtx {
     cell_id: UUID;
     instance_params: Record<string, unknown>;
     cell_params: Record<string, unknown>;
     deps_data: Record<string, unknown>;      // key: dep cell type, value: that dep's current version data
     clock: Clock;
     logger: Logger;
   }

   export type DeterministicHandler = (ctx: DeterministicHandlerCtx) => Promise<RunOutcome>;

   export interface HandlerRegistry {
     register(name: string, handler: DeterministicHandler): void;
     replace(name: string, handler: DeterministicHandler): void;  // test-support; fails if not already registered
     get(name: string): DeterministicHandler | undefined;
     has(name: string): boolean;
   }

   export interface SupervisorConfig {
     supervisorId: string;
     accepts: CellKind[];                     // Plan B: ["deterministic"] only (agentic → Plan C)
     concurrency: number;
     storage: StorageBackend;
     queue: DispatchQueue;
     clock: Clock;
     logger: Logger;
     heartbeatIntervalMs: number;             // default 5000
     claimPollIntervalMs: number;             // default 1000
     handlerRegistry: HandlerRegistry;
     concurrencyLimits: Record<string, number>;  // deployment-level tag limits, passed to queue.claim
   }

   export interface SupervisorHandle {
     supervisorId: string;
     shutdown(): Promise<void>;
     activeCellCount(): number;
   }
   ```

2. Export all from `src/supervisor/types.ts`.

**Verification:** `npx tsc --noEmit` passes.

---

### Task 1.2: `src/supervisor/handler-registry.ts`

**Steps:**

1. Write:
   ```ts
   import { DeterministicHandler, HandlerRegistry } from "./types.js";

   export class InMemoryHandlerRegistry implements HandlerRegistry {
     private readonly handlers = new Map<string, DeterministicHandler>();
     register(name: string, handler: DeterministicHandler): void {
       if (this.handlers.has(name)) {
         throw new Error(`handler already registered: ${name} (use replace() to overwrite in tests)`);
       }
       this.handlers.set(name, handler);
     }
     replace(name: string, handler: DeterministicHandler): void {
       if (!this.handlers.has(name)) {
         throw new Error(`cannot replace unregistered handler: ${name}`);
       }
       this.handlers.set(name, handler);
     }
     get(name: string): DeterministicHandler | undefined {
       return this.handlers.get(name);
     }
     has(name: string): boolean {
       return this.handlers.has(name);
     }
   }

   export function createHandlerRegistry(): HandlerRegistry {
     return new InMemoryHandlerRegistry();
   }
   ```

2. Write `handler-registry_test.ts`:
   - `register` + `get` returns the handler.
   - Duplicate `register` throws.
   - `has` returns false for unregistered name.

**Verification:** Unit tests pass.

---

## Phase 2: Commit flow (quality rules + resource write)

### Task 2.1: `src/supervisor/commit.ts`

**Goal:** Given a cell and a `RunOutcome`, run quality rules across all the cell's owned resources, commit or reject each, and produce a set of resulting events + state transitions. This is the `on_work_complete` logic from spec §4.2.

**Steps:**

1. Sketch the interface:
   ```ts
   import { StorageBackend } from "../storage/interfaces.js";
   import { RunOutcome, UUID } from "../shared/types.js";
   import { Clock } from "../shared/clock.js";
   import { Logger } from "../shared/logger.js";
   import { evaluateQualityRules } from "../resource/quality-rules.js";

   export interface CommitResult {
     outcome: "committed" | "no_op" | "quality_failed";
     failed_rule?: { rule_type: string; details: string };
     warnings: { rule_type: string; details: string }[];
   }

   export async function commitCellOutcome(opts: {
     storage: StorageBackend;
     cell_id: UUID;
     outcome: RunOutcome;
     clock: Clock;
     logger: Logger;
   }): Promise<CommitResult> { /* ... */ }
   ```

2. Behavior (per spec §4.2 `on_work_complete` and §5.2):
   - Load the cell, then load its owned resources.
   - For each owned resource:
     - Find its entry in `outcome.result` (the cell's handler returns a single object; convention is `{ "<resource_path_joined>": <data> }` — but since a cell may own multiple resources, Plan B supports the simpler form where a cell owns zero or one resource. **Add validation**: if the cell owns more than one resource in Plan B, throw an implementation-clarity error until Plan C revisits the multi-resource case).
     - Evaluate its quality rules against `(newData, previousVersionData)`. `previousVersionData` = read of `current_version_id` via `resourceData.read(current_version_row)` if set, else null.
     - If any `severity: error` rule fails → return `{ outcome: "quality_failed", failed_rule: ... }`. No commit.
     - Warning-severity failures collect into `warnings: [...]`.
   - If `outcome.changed === false`:
     - Call `storage.resources.noOpCommit(resource_id)` → append `no_op_commit` event via `storage.events.append({ cell_id, kind: "no_op_commit", payload: { reason: outcome.change_summary ?? null } })`.
     - Do not emit `recalculate`.
     - Return `{ outcome: "no_op", warnings }`.
   - If `outcome.changed === true`:
     - Transactionally: write the new version row via `resourceData.write(...)`; update `rimsky_resources.current_version_id`; GC old versions past `keep_versions`.
     - Append `commit` event with `{ resource_id, version_id, change_summary: outcome.change_summary }`.
     - For each warning, append a `quality_rule_failed` event with `severity: "warning"`.
     - Return `{ outcome: "committed", warnings }`.

3. The caller (`deterministic-runner`) is responsible for emitting `recalculate` to dependents on `committed` outcome and for wiring `on_error` on `quality_failed`.

4. Write `commit_test.ts`:
   - Cell with no owned resources + `changed: true` → commit succeeds (degenerate path used for future non-data cells; in Plan B it's an edge case).
   - Quality rule fails → `quality_failed` returned; no version written; no event appended beyond `quality_rule_failed`.
   - `changed: false` → `no_op_commit` event written; no new version.
   - `changed: true` with previous version → new version written; `previous_version_id` tracks prior; old versions past keep_versions deleted.
   - Warning-severity rule fails → commit proceeds; `quality_rule_failed` event with severity warning logged.

**Verification:** Unit tests pass against a postgres-backed harness (since the test exercises real storage).

---

## Phase 3: On-error path

### Task 3.1: `src/supervisor/on-error.ts`

**Goal:** Wire the policy evaluator (Plan A) against real storage and queue: when a cell errors, evaluate the policy, perform the action (retry re-enqueue with backoff, invalidate targets, give_up), update cell state, append events.

**Steps:**

1. Interface:
   ```ts
   export async function handleCellError(opts: {
     storage: StorageBackend;
     queue: DispatchQueue;
     clock: Clock;
     logger: Logger;
     cell_id: UUID;
     error_class: string;
     payload?: Record<string, unknown>;
   }): Promise<void>;
   ```

2. Behavior (per spec §4.2 `on_error`, §7.3, §8.3):
   - Load cell. Load cell's template spec to find `error_types[error_class].policy`.
   - If class missing: treat as `give_up` with reason `unknown_error_class`. Transition cell to `failed`. Emit `error` event. Return.
   - Call `evaluatePolicy(policy, cellErrorState, error_class)` from Plan A's policy evaluator.
   - Result branches:
     - **retry**: update cell `retry_counter` and `action_index` per evaluator output; transition state `running → stale`; enqueue dispatch row with `enqueued_at = clock.now() + delay_ms` (from evaluator); append `error` event with `action_taken: retry`.
     - **invalidate(targets)**: for each target, resolve target cell id (targets in the policy are referenced by cell *type* within the template; resolve against the cell's instance to get target cell IDs); emit `invalidate` message to each target via `invalidateCell` (from Plan A's `scheduler/invalidate.ts`); transition self to `stale`; reset `retry_counter = 0`; do not advance `action_index` (advances on next-same-class recurrence); append `error` event with `action_taken: invalidate`. **Important**: do not re-enqueue self — the cell waits for upstream to re-complete, at which point scheduler's ready-sweep picks it up.
     - **give_up**: transition state `running → failed`; append `error` event with `action_taken: give_up`; do not re-enqueue.
   - Always clear `assigned_supervisor_id` on transition out of `running`.

3. Infrastructure errors (`silence_timeout`, `operator_kill`, `supervisor_crash`, `subprocess_exit_before_complete`, `supervisor_shutdown`): treat as re-enqueue without retry_counter increment. Specifically:
   - Transition cell `running → stale`. Clear `assigned_supervisor_id`. Re-enqueue with `enqueued_at = clock.now()` (no backoff).
   - Append `error` event with `action_taken: infra_reenqueue`.
   - Do not consult the policy chain. `retry_counter` and `action_index` are not touched.
   - This is the path the scheduler's heartbeat-loss sweep uses for dead supervisors (Plan A already does this); the supervisor uses it when its own local error detection fires an infra class.

4. Write `on-error_test.ts` with real storage harness:
   - Retry path: errors twice then succeeds; counters advance as expected; backoff delays honored.
   - Invalidate path: emits invalidate to named targets; self goes to stale; does not re-enqueue self.
   - Give_up path: cell transitions to failed; no dispatch row enqueued.
   - Unknown error class: give_up + `unknown_error_class` reason.
   - Infra error: cell stale, dispatch re-enqueued with zero delay, counters untouched.

**Verification:** Tests pass.

---

## Phase 4: Deterministic runner

### Task 4.1: `src/supervisor/deterministic-runner.ts`

**Goal:** Glue — load cell + template + deps, call handler, interpret outcome (success → commit flow; throw → on-error).

**Steps:**

1. Interface:
   ```ts
   export async function runDeterministicCell(opts: {
     storage: StorageBackend;
     queue: DispatchQueue;
     clock: Clock;
     logger: Logger;
     cell_id: UUID;
     supervisor_id: string;
     handlerRegistry: HandlerRegistry;
     kind: CellKind;                         // runtime assertion
   }): Promise<void>;
   ```

2. Behavior:
   1. Assert `kind === "deterministic"`. Throw if not (future: dispatch by kind).
   2. Load cell. Load instance. Load template spec.
   3. Find the cell's execution config (deterministic handler name).
   4. Resolve `handler = handlerRegistry.get(name)`. If missing → `handleCellError(cell_id, "unknown_handler")`. The cell's template must declare `unknown_handler` in its error_types for this to not be an unknown-class give_up; otherwise it gives up anyway. Note this as an operator concern in the plan: missing handlers are a deployment bug, not a cell-author concern.
   5. Resolve `deps_data`: for each dep cell, look up its owned resources (if any) and read current version via `resourceData.read(current_version_row)`. Shape as `Record<dep_cell_type, dep_current_data>`.
   6. Log `work_started` event. Transition cell `stale → running`. Set `assigned_supervisor_id`. Set `last_heartbeat_at = clock.now()`.
   7. Call `handler({ cell_id, instance_params, cell_params, deps_data, clock, logger })` inside a try/catch.
   8. On success: call `commitCellOutcome(storage, cell_id, outcome, clock, logger)`.
      - `committed`: log `work_completed` with outcome `committed`; transition `running → fresh`; for each dependent cell, emit `recalculate` (direct: set a message row via `storage.events.append({ kind: "message_emitted", ... })` plus the ready-sweep in the scheduler will pick up the dependent). Plan A's scheduler already handles ready-sweep; Plan B only needs to log the emission and rely on the sweep.
         - Exception: for a newly-committed parent, the dependent won't enter the ready-sweep until its own state is `stale`. So the supervisor must also invoke the per-dep "on_recalculate" logic: if a dependent is already stale and all of its other deps are now fresh, it's already ready; if a dependent is `fresh`, it stays fresh (recalculate on fresh = no-op per spec §5.2); if `running`, dependent is running — no-op. This logic lives in `src/scheduler/recalculate.ts` (new file, shared between scheduler and supervisor).
      - `no_op`: log `work_completed` with outcome `no_op`; transition `running → fresh`; **do not** emit recalculate.
      - `quality_failed`: treat as `handleCellError(cell_id, "quality_rule_failed", { rule: ... })`.
   9. On handler throw:
      - If `err instanceof CellApplicationError` and `err.error_class` is a declared class in the cell's template → `handleCellError(cell_id, err.error_class, err.details)`.
      - Otherwise → `handleCellError(cell_id, "unknown_error_class", { error: String(err) })` (which will give_up).
   10. Whatever the outcome: call `queue.complete(dispatch_id)` to delete the claimed dispatch row (unless `handleCellError` already re-enqueued via `enqueue`, in which case the old row should be deleted by the enqueue's ON CONFLICT semantics — but to be safe, `complete` after every terminal execution).

3. Write `src/scheduler/recalculate.ts` (a helper used by scheduler and supervisor):
   ```ts
   export async function recalculateCell(opts: {
     storage: StorageBackend;
     queue: DispatchQueue;
     clock: Clock;
     target_cell_id: UUID;
     source_cell_id: UUID | null;
   }): Promise<void> {
     // Per spec §5.2, §4.2 on_recalculate:
     // - If target is fresh or running: append message_received event, no action.
     // - If target is stale: check all deps fresh? if yes, enqueue dispatch; if no, no-op.
     // - If target is failed: no-op (failed cells only exit via explicit invalidate).
   }
   ```

4. Write `deterministic-runner_test.ts` with real storage harness:
   - Handler success with `changed: true` → commit; state fresh; dependent cell gets recalculate + dispatch enqueued if its other deps are fresh.
   - Handler throws `CellApplicationError` → `on-error` path.
   - Handler throws generic Error → treated as unknown_error_class → give_up.
   - Handler missing from registry → unknown_handler error.

**Verification:** Tests pass.

---

## Phase 5: Supervisor process

### Task 5.1: `src/supervisor/supervisor.ts`

**Steps:**

1. Implement the supervisor lifecycle (per spec §9.2 and the fixes from the prior reviewer):
   ```ts
   export function startSupervisor(cfg: SupervisorConfig): SupervisorHandle {
     validateConfig(cfg);       // throws if accepts includes "agentic" in Plan B
     const log = cfg.logger.child({ component: "supervisor", supervisor_id: cfg.supervisorId });
     let stopped = false;
     const active = new Map<UUID, { cell_id: UUID; startedAt: Date }>();

     async function register() {
       await cfg.storage.supervisors.register({
         id: cfg.supervisorId,
         accepts: cfg.accepts,
         concurrency: cfg.concurrency,
       });
     }

     async function heartbeat() {
       await cfg.storage.supervisors.heartbeat(cfg.supervisorId, active.size);
       // Also update per-cell heartbeats and check kill_requested.
       for (const { cell_id } of active.values()) {
         await cfg.storage.cells.updateHeartbeat(cell_id, cfg.clock.now());
         const cell = await cfg.storage.cells.get(cell_id);
         if (cell?.kill_requested) {
           // v1: mark cell for infra-error termination on next iteration
           await markForInfraError(cell_id, "operator_kill");
         }
       }
     }

     async function tryClaim() {
       if (active.size >= cfg.concurrency) return;
       const row = await cfg.queue.claim(cfg.supervisorId, cfg.accepts, deploymentLimits(cfg));
       if (!row) return;
       active.set(row.cell_id, { cell_id: row.cell_id, startedAt: cfg.clock.now() });
       // Fire-and-forget the run (track the promise so shutdown can await).
       runInBackground(row);
     }

     async function runInBackground(row: DispatchRow) {
       try {
         await runDeterministicCell({
           storage: cfg.storage, queue: cfg.queue, clock: cfg.clock, logger: log,
           cell_id: row.cell_id, supervisor_id: cfg.supervisorId,
           handlerRegistry: cfg.handlerRegistry, kind: row.cell_kind,
         });
         await cfg.queue.complete(row.id);
       } catch (e) {
         log.error("runDeterministicCell failed unexpectedly", { cell_id: row.cell_id, error: String(e) });
         // Treat as infra error (supervisor crash-equivalent)
         await handleCellError({ ...cfg, cell_id: row.cell_id, error_class: "supervisor_exception", payload: { error: String(e) } });
         await cfg.queue.fail(row.id, String(e));
       } finally {
         active.delete(row.cell_id);
       }
     }

     async function loop() {
       await register();
       while (!stopped) {
         try {
           await heartbeat();
           await tryClaim();
         } catch (e) {
           log.error("supervisor loop iteration failed", { error: String(e) });
         }
         await cfg.clock.sleep(Math.min(cfg.heartbeatIntervalMs, cfg.claimPollIntervalMs));
       }
       log.info("supervisor draining");
       while (active.size > 0) {
         await cfg.clock.sleep(100);
       }
       await cfg.storage.supervisors.unregister(cfg.supervisorId);
     }

     loop();

     return {
       supervisorId: cfg.supervisorId,
       activeCellCount: () => active.size,
       shutdown: async () => {
         stopped = true;
         // drain loop handles unregister
       },
     };
   }
   ```

2. `validateConfig` throws `Error("Plan B supervisor does not accept agentic kind; use Plan C supervisor")` if `accepts` contains `"agentic"`. Plan C replaces this with real agentic support.

3. `deploymentLimits(cfg)` returns `cfg.concurrencyLimits` (declared in `SupervisorConfig`, Task 1.1). This map is passed to `queue.claim` on every attempt. Tag enforcement happens at claim time inside the queue's SQL (per spec §8.2); both scheduler and supervisor consult the same deployment-level map.

4. Write `supervisor_test.ts` with fakes for storage and queue:
   - Starts → registers supervisor row.
   - Claims a dispatch row → runs it → marks complete.
   - Heartbeat writes per-cell heartbeats.
   - Kill-requested polled during heartbeat fires infra error.
   - Shutdown drains active cells.

**Verification:** Unit tests pass.

---

### Task 5.2: `src/config/supervisor.ts` entry point

**Steps:**

1. Re-export `startSupervisor` with defaults:
   ```ts
   export function startSupervisor(config: SupervisorConfig & { heartbeatIntervalMs?: number; claimPollIntervalMs?: number; }): SupervisorHandle {
     const withDefaults = {
       heartbeatIntervalMs: 5000,
       claimPollIntervalMs: 1000,
       ...config,
     };
     return startSupervisorCore(withDefaults);
   }
   ```

2. Add exports to `src/index.ts`: `startSupervisor`, `createHandlerRegistry`, `CellApplicationError`, types from `supervisor/types.ts`.

**Verification:** `npx tsc --noEmit` passes; `src/index.ts` exports the new surface.

---

## Phase 6: Scenario test harness updates

### Task 6.1: Extend `test/harness.ts`

**Steps:**

1. Add a supervisor-starting helper:
   ```ts
   export interface ScenarioHarness {
     // ... existing fields
     startSupervisor: (opts: {
       supervisorId: string;
       accepts?: CellKind[];
       concurrency?: number;
       handlerRegistry: HandlerRegistry;
       concurrencyLimits?: Record<string, number>;
     }) => SupervisorHandle;
     registerHandler: (name: string, handler: DeterministicHandler) => void;
     handlerRegistry: HandlerRegistry;
   }

   export async function startScenarioHarness(): Promise<ScenarioHarness> {
     // ...
     const handlerRegistry = createHandlerRegistry();
     const supervisors: SupervisorHandle[] = [];
     return {
       // ...
       handlerRegistry,
       registerHandler: (n, h) => handlerRegistry.register(n, h),
       startSupervisor: (opts) => {
         const s = startSupervisor({
           supervisorId: opts.supervisorId,
           accepts: opts.accepts ?? ["deterministic"],
           concurrency: opts.concurrency ?? 1,
           storage, queue, clock, logger: SilentLogger,
           heartbeatIntervalMs: 50, claimPollIntervalMs: 50,
           handlerRegistry: opts.handlerRegistry,
           concurrencyLimits: opts.concurrencyLimits ?? {},
         });
         supervisors.push(s);
         return s;
       },
       shutdown: async () => {
         for (const s of supervisors) await s.shutdown();
         // ... existing shutdown
       },
     };
   }
   ```

2. Add a `waitForCellState` polling helper:
   ```ts
   export async function waitForCellState(storage: StorageBackend, cell_id: UUID, state: CellState, timeoutMs = 5000): Promise<void> {
     const start = Date.now();
     while (Date.now() - start < timeoutMs) {
       const cell = await storage.cells.get(cell_id);
       if (cell?.state === state) return;
       await new Promise((r) => setTimeout(r, 20));
     }
     const cell = await storage.cells.get(cell_id);
     throw new Error(`Timed out waiting for cell ${cell_id} to reach state ${state}; current: ${cell?.state}`);
   }
   ```

**Verification:** `npx tsc --noEmit` passes.

---

## Phase 7: Scenario tests

Each of the following scenarios follows the same shape: deploy a fixture YAML template, register the handlers it references, create an instance, start scheduler + supervisor, drive with the clock, assert on state and events. All use `await h.clock.advance(...)`.

### Task 7.1: `happy-path.yaml` + `deterministic-happy-path_test.ts`

**Fixture** (`test/fixtures/templates/happy-path.yaml`): two deterministic cells A → B; A produces resource `a-out`; B reads A's output and produces `b-out`. No error types declared (unused in happy path).

**Test:** Register handlers that return `changed: true` with trivial payloads. Start scheduler + supervisor. Advance clock enough ticks. Assert: both cells reach `fresh`; both resources have a current version; event log contains `commit` for both.

**Verification:** Test passes.

---

### Task 7.2: `retry-then-succeed.yaml` + test

**Fixture:** One deterministic cell with `error_types.transient: policy: [{action: retry, count: 3, backoff: linear, base_delay_ms: 50, max_delay_ms: 200}]`.

**Test:** Register a handler that throws `CellApplicationError("transient")` on first two calls, then succeeds. Start scheduler + supervisor. Advance clock. Assert: cell eventually reaches `fresh`; event log shows three `work_started`, two `error` events with `action_taken: retry`, one `commit`.

**Verification:** Test passes.

---

### Task 7.3: `cascade-invalidate.yaml` + test

**Fixture:** Three cells A → B → C. A is a deterministic handler. B declares `error_types.needs_reconfig: policy: [{action: invalidate, targets: [A]}, {action: give_up}]`. C is straightforward.

**Test:** Register handlers — B's handler reads a test-controlled flag (`let bShouldFail = true;`) and throws `CellApplicationError("needs_reconfig")` on the first run, then returns success on subsequent runs (or use `h.handlerRegistry.replace("b-handler", newHandler)` at the test's discretion — `replace` is supported for this exact use case). Start scheduler + supervisor. Let B's first attempt fail; assert B's invalidate cascades to A; then flip the flag (or `replace`); assert A re-runs, B succeeds, cascade completes.

**Verification:** Test passes.

---

### Task 7.4: `give-up.yaml` + test

**Fixture:** One cell with `error_types.always_fails: policy: [{action: retry, count: 2, backoff: linear, base_delay_ms: 10}, {action: give_up}]`.

**Test:** Handler always throws `CellApplicationError("always_fails")`. Start scheduler + supervisor. Advance clock to allow retries + give_up. Assert: cell state = `failed`; action_index advanced past policy length; `error` event with `action_taken: give_up` present.

**Verification:** Test passes.

---

### Task 7.5: `double-buffering.yaml` + test

**Fixture:** One cell with quality rule `{type: row_count_ratio, min_ratio: 0.5, severity: error}`.

**Test:** First run: handler returns 10 rows → commits as v1. Second run (via operator-invalidate): handler returns 3 rows (30% of 10, below 0.5 ratio) → commit rejected; `quality_rule_failed` event; policy runs `retry → give_up`. Assert: `current_version_id` still points to v1 (unchanged); `previous_version_id` still null or points to the original first version; cell eventually `failed`; readers reading the resource still get the 10-row v1 data.

**Verification:** Test passes.

---

### Task 7.6: `no-op-commit.yaml` + test

**Fixture:** Two cells A → B. Both deterministic. No error types.

**Test:** Register A's handler to return `{result: {...}, changed: false, change_summary: "no upstream changes"}` on second invocation. First run: both commit; B has v1. Operator invalidates A. Second run of A: returns changed=false. Assert: A's state = `fresh` (after no-op); no new resource version for A; B's state unchanged — stays `fresh` (no recalculate emitted); event log has a `no_op_commit` for A.

**Verification:** Test passes.

---

### Task 7.7: `rollback.yaml` + test

**Fixture:** One cell with two committed versions (by running it twice before the test's assertion phase).

**Test:** Start scheduler + supervisor. Let cell commit v1, then v2. Issue operator invalidate with `restore_version: "previous"`. Assert: cell's resource `current_version_id` reverts to v1 (the one pointed to by `previous_version_id` before the invalidate); cell state returns to `fresh` without re-executing; event log has `message_received` with reason indicating rollback; no `work_started` event after the rollback invalidate.

**Verification:** Test passes.

---

### Task 7.8: `deterministic-concurrency-respected_test.ts`

**Fixture:** Reuse `happy-path.yaml` but parameterize two instances.

**Test:** Per-instance tags are resolved at instantiation time to concrete tag strings `per-instance:{instance_id}` (spec §8.2). For each instance, the scheduler deployment's `concurrencyLimits` map includes an entry for that instance's resolved tag with limit 1. For the test: create two instances, capture their IDs, construct `concurrencyLimits = { [`per-instance:${inst1}`]: 1, [`per-instance:${inst2}`]: 1 }`. Start one supervisor with `concurrency: 4`, `concurrencyLimits` passed through. Register handlers that `await h.clock.sleep(200)` before returning. Drive two parallel runs (one per instance, two cells per instance). Advance clock in small increments and sample `storage.cells.listRunning()` at each step. Assert: at every sampled moment, no more than one cell per instance is `running`; the second cell of each instance waits until the first completes. Also verify that cross-instance concurrency is unconstrained (both instances can have one running cell each at the same time).

**Verification:** Test passes.

---

## Phase 8: Definition of done

### Task 8.1: Final verification

**Steps:**

1. `cd rimsky && npm run build` → exits 0.

2. `npm test` → all tests pass (Plan A scenarios still green + all new Plan B scenarios green).

3. `npm run lint` → no errors.

4. `src/index.ts` exports: Plan A surface + `startSupervisor`, `createHandlerRegistry`, `CellApplicationError`, `DeterministicHandler`, `SupervisorConfig`, `SupervisorHandle`, `HandlerRegistry`.

5. Update `CHANGELOG.md` (repo root) with an entry:
   ```
   - Added rimsky deterministic supervisor: handler registry, on_work_complete commit flow, on_error policy execution, and full deterministic scenario suite (happy path, retry, cascade, give_up, double-buffering, no-op, rollback, concurrency). See /rimsky/ and docs/plans/2026-04-21-rimsky-plan-b-deterministic-supervisor.md.
   ```

6. Confirm the stub supervisor from Plan A is NOT deleted (other tests may still use it; delete only if unused after grep).

**Deliverables:**
- `startSupervisor` produces a runnable supervisor process.
- Deterministic cells execute end-to-end against real storage and queue.
- All v1 reactive behaviors (cascade, retry, give_up, rollback, no-op) are exercised by scenario tests.
- Plan C can begin: all that's left is the agentic execution path and the HTTP control API.

---

## Notes for the implementer

- **Do not import Plan A internals outside the module paths.** Supervisor code imports from `storage/interfaces`, `queue/interface`, `shared/*`, `cell/policy-evaluator`, `scheduler/invalidate` (for cascade) and `scheduler/recalculate` (newly added helper in this plan). It does not reach into Plan A's scheduler loop internals.
- **Agentic is explicitly rejected.** `startSupervisor` throws on `accepts: ["agentic"]` until Plan C ships.
- **The `recalculate` helper is shared.** Added in this plan under `src/scheduler/recalculate.ts`. Both the scheduler's timer/invalidate path and the supervisor's commit path call it. It replaces any inline recalculate logic that might have existed in Plan A's `invalidateCell`.
- **Scenario flakiness is unacceptable.** If a scenario test passes only sometimes, fix the underlying race rather than adding retries to the test.
- **Each handler is stateless.** The supervisor does not maintain handler-local state across invocations. Any durable state goes through resources.
- **`CellApplicationError` is the only way a handler can signal a declared error class.** A raw `throw new Error("transient")` will be treated as unknown_error_class. Handlers must `throw new CellApplicationError("transient", {...})`.
- **Event log discipline:** append one event per meaningful lifecycle point. Under-logging hides bugs; over-logging (every variable read) is noise. Target: every state transition, every message, every commit/no-op/reject, every error, every claim/complete is logged. That's the full v1 vocabulary.
