# Instance Lifecycle: Durable-by-Default + Frame-End Correctness + Trace Retention — Implementation Plan

**Spec:** .ok-planner/specs/2026-06-03-instance-lifecycle-durable-by-default-design.md
**Goal:** Make instances durable by default (opt-in `terminate_after_run`), fix frame-end so a `parked` node_run holds its frame open, and bring the per-instance execution trace (frames + node_runs + event logs) under one retention policy.
**Architecture:** Three coupled changes in the root-module `lib/graph` → `lib/runtime` → `lib/control` stack plus `lib/foundation/persistence`. The lifecycle flag threads create-request → persistence column → projection (mirroring the existing `paused` flag). The frame-end and instance-terminal predicates (both Postgres + SQLite drivers) gain a parked-aware clause. The retention sweep is reshaped to reap whole traces (frames cascade their node_runs; event logs reaped by time). Three concept docs are mutated to match.
**Tech Stack:** Go; `jackc/pgx/v5` (Postgres), `modernc.org/sqlite` (pure-Go SQLite); `go-chi/chi` HTTP; `robfig/cron`; testcontainers-go for Postgres + real-image end-to-end scenarios.

> **Driver parity:** every persistence change lands in BOTH `lib/foundation/persistence/postgres/` and `lib/foundation/persistence/sqlite/`. Postgres SQL stamps `now()`; SQLite passes a `nowUTC()` value and uses `?` placeholders. Preserve each driver's existing idiom.
>
> **Docker required:** Postgres-driver tests, `test/scenarios/...`, and `lib/services/test/...` use testcontainers-go (Docker socket). SQLite-driver tests run in-process (fast, no Docker). The services/acceptance scenarios consume **locally-built** images — run `make core-images` and `make service-images` first (per CLAUDE.md "Services integration harness").

---

## Pass 1: Frame-end correctness — a parked node_run holds its frame open

**Goal:** A running frame ends only when every node_run is resolved (none `stale`, `running`, or `parked`). Today `ListRunningFramesNoPendingNodes` excludes `parked`, so a frame drains to `completed` while a node is parked. Fix both drivers, proven red→green.
**Scope:** Tasks 1–3
**End state:** working
**Verification:** `go test ./lib/foundation/persistence/sqlite/... -run 'Frame|Park' -count=1 && go test ./lib/foundation/persistence/postgres/... -run 'Frame|Park' -count=1`

### Task 1 (RED): Add a failing test that a parked-only frame stays running

**Files:** `lib/foundation/persistence/sqlite/frames_parked_hold_test.go` (new)

**Context:** `ListRunningFramesNoPendingNodes` (`lib/foundation/persistence/sqlite/frames.go`, mirror in `postgres/frames.go`) selects running frames whose node_runs are all resolved, using `NOT EXISTS (... phase IN ('pending','active','held') AND state IN ('stale','running'))`. A parked node_run is `phase='parked', state='parked'` (set by `applyTerminalPark` / `ParkActiveInTx`), so it is NOT matched — the frame is wrongly reported as ended.

**Steps:**
1. For the in-process SQLite test harness pattern, read `lib/foundation/persistence/sqlite/queue_park_test.go` — it seeds the full template → instance → run_scope → frame → node → node_run chain (including parked rows) and shows how to build a `Tables` and open a tx. (`frames_retention_test.go` runs against an empty DB and is not a seeding model; `observability_test.go` only writes event rows.)
2. Write `TestParkedNodeRunHoldsFrameOpen`: seed one instance, one node, one `running` frame, and one node_run for that frame in `phase='parked', state='parked'` (and NO `stale`/`running` runs). Call `Frames().ListRunningFramesNoPendingNodes`. Assert the returned slice does **not** contain the frame (a parked node holds it open).
3. Run it and confirm it FAILS against current code (current predicate excludes parked, so the frame is returned).

**Verification:** `! go test -run TestParkedNodeRunHoldsFrameOpen ./lib/foundation/persistence/sqlite/...`

### Task 2 (GREEN): Make parked node_runs hold the frame open in both drivers

**Files:** `lib/foundation/persistence/sqlite/frames.go`, `lib/foundation/persistence/postgres/frames.go`

**Steps:**
1. In `ListRunningFramesNoPendingNodes` (both drivers), extend the `NOT EXISTS` node_run predicate so a parked row also counts as unresolved. Replace the inner condition with:
   ```sql
   AND (
        (r.phase IN ('pending','active','held') AND r.state IN ('stale','running'))
     OR r.phase = 'parked'
     OR r.state = 'parked'
   )
   ```
2. Run the Task 1 test; confirm it now PASSES.
3. Run the existing frame tests in both drivers to confirm no regression in the parked-aware direction (a frame with a stale/running run is still reported as not-ended only when truly drained).

**Verification:** `go test -run TestParkedNodeRunHoldsFrameOpen ./lib/foundation/persistence/sqlite/... && go test ./lib/foundation/persistence/sqlite/... -run Frame -count=1`

### Task 3: Parked-consistency audit of sibling frame/node-run predicates

**Files:** `lib/foundation/persistence/postgres/frames.go`, `lib/foundation/persistence/sqlite/frames.go`

**Context:** Several predicates use a phase/state set; each must be correct for its intent. "Does this frame still have unresolved work" predicates must count `parked`; "is there work eligible to dispatch right now" predicates must not (a parked row is not dispatchable until woken).

**Steps:**
1. Enumerate every predicate in both `frames.go` files that filters node_runs by `phase`/`state` (the stuck-frame warning query, the queued-advance path, `CountHeldFrames`, and any others). For each, add a one-line comment stating its intent (`-- unresolved-work: counts parked` or `-- dispatch-eligible: excludes parked`).
2. If any predicate whose intent is "unresolved work" omits `parked` (a latent bug of the same family as Task 2), fix it and add a focused test asserting the corrected behavior (red→green: add the test gated `! <test>`, then fix, then `<test>`). If none are found, state that in the commit-free working tree via the comments only.
3. Confirm `CountHeldFrames` already includes `parked` (it queries `d.phase = 'parked'`) and is consistent.

**Verification:** `go build ./... && go test ./lib/foundation/persistence/sqlite/... -run Frame -count=1`

---

## Pass 2: `terminate_after_run` flag — column + thread-through

**Goal:** Add a per-instance `terminate_after_run` boolean sourced from the create request, persisted, and surfaced on the GET/list projection — mirroring the existing `paused` flag end to end. No termination-behavior change yet (the predicate still ignores the column until Pass 3).
**Scope:** Tasks 4–7
**End state:** working
**Verification:** `go build ./... && go test ./lib/control/controlapi/... -run 'TerminateAfterRun|Instance' -count=1 && go test ./lib/foundation/persistence/sqlite/... -run Instance -count=1`

### Task 4 (RED): Add a failing API round-trip test for the flag

**Files:** `lib/control/controlapi/instances_test.go`

**Context:** The create handler decodes `createInstanceRequest`; the GET projection is `instanceItem` via `toInstanceItem`. Existing tests POST/GET with JSON maps (see `messages_test.go`, `instances_test.go` `httpJSON` helper).

**Steps:**
1. Add `TestTerminateAfterRunRoundTrip`: POST `/instances` with a deployed template and body including `"terminate_after_run": true`; GET `/instances/{id}`; assert the response JSON field `terminate_after_run` is `true`. Add a second instance created without the field and assert its `terminate_after_run` is `false`.
2. Run it; confirm it FAILS today (the field is absent from request decode and from the GET projection, so the assertion on `true` fails).

**Verification:** `! go test -run TestTerminateAfterRunRoundTrip ./lib/control/controlapi/...`

### Task 5: Migration 005 — add the column in both drivers

**Files:** `lib/foundation/persistence/postgres/migrations/005-instance-terminate-after-run.sql` (new), `lib/foundation/persistence/sqlite/migrations/005-instance-terminate-after-run.sql` (new)

**Context:** Migrations are auto-embedded via `//go:embed *.sql`; `004-frame-delivery-default.sql` is the current highest. `paused` is declared in `001-schema.sql` in each driver — read its column type there and mirror it (Postgres `BOOLEAN NOT NULL DEFAULT FALSE`; SQLite uses its own boolean storage form).

**Steps:**
1. Postgres `005`: `ALTER TABLE rimsky_instances ADD COLUMN terminate_after_run boolean NOT NULL DEFAULT false;` (with the standard copyright header comment block other migrations carry).
2. SQLite `005`: `ALTER TABLE rimsky_instances ADD COLUMN terminate_after_run <paused's type> NOT NULL DEFAULT 0;` — match the exact type token `paused` uses in the sqlite `001-schema.sql`. (Unlike `004`, this is a real `ADD COLUMN`, which SQLite supports with a literal `DEFAULT`.)
3. Run the SQLite persistence suite, which opens a fresh DB and applies all migrations in-process, to confirm `005` applies cleanly.

**Verification:** `go test ./lib/foundation/persistence/sqlite/... -run Instance -count=1`

### Task 6: Persistence structs + driver Create/scan

**Files:** `lib/foundation/persistence/instances.go`, `lib/foundation/persistence/postgres/instances.go`, `lib/foundation/persistence/sqlite/instances.go`

**Steps:**
1. `instances.go`: add `TerminateAfterRun bool` to `InstanceCreateInput`, and `TerminateAfterRun bool \`json:"terminate_after_run"\`` to `InstanceRow` (place near `Paused`).
2. Both drivers: add `terminate_after_run` to the `instanceCols` constant (append after `paused`, keeping the column order consistent between the const, the `RETURNING`, and the scan).
3. Both drivers' `Create`: add `terminate_after_run` to the INSERT column list and bind `in.TerminateAfterRun` in the matching `$N`/`?` position.
4. Both drivers: update every scan function that reads `instanceCols` (`scanInstance` and, in Postgres, `scanInstanceRows`; in SQLite `scanInstance`) to scan the new column into `out.TerminateAfterRun`, in the same position it occupies in `instanceCols`.
5. Run the persistence instance tests in both drivers.

**Verification:** `go build ./... && go test ./lib/foundation/persistence/sqlite/... -run Instance -count=1`

### Task 7 (GREEN): Control-api plumbing — request → projection

**Files:** `lib/control/controlapi/instances.go`

**Steps:**
1. Add `TerminateAfterRun bool \`json:"terminate_after_run,omitempty"\`` to `createInstanceRequest` (near `Paused`).
2. Add `TerminateAfterRun bool` to `provisionArgs`; in `handleCreateInstance`, pass `body.TerminateAfterRun` into the `provisionArgs` literal; in `provisionInstanceTx`, pass `args.TerminateAfterRun` into the `InstanceCreateInput` literal.
3. Add `TerminateAfterRun bool \`json:"terminate_after_run"\`` to `instanceItem`; in `toInstanceItem`, set `out.TerminateAfterRun = r.TerminateAfterRun`.
4. Run the Task 4 round-trip test; confirm it now PASSES.

**Verification:** `go test -run TestTerminateAfterRunRoundTrip ./lib/control/controlapi/... && go build ./...`

---

## Pass 3: Strict terminal semantics — durable by default

**Goal:** Rewrite `MarkInstanceTerminatedIfDone` so only `terminate_after_run` instances self-terminate, and only after the next frame ends (strict — no waiting on queued frames); drop the publisher-subscription coupling; never terminate while a node_run is parked. Guard queued-frame promotion against terminated instances. Rework existing tests that assumed auto-terminate-on-drain.
**Scope:** Tasks 8–12
**End state:** working
**Verification:** `go build ./... && go test ./lib/graph/... ./lib/control/controlapi/... ./lib/foundation/persistence/sqlite/... -count=1` (plus the Postgres + scenario suites under Docker — see Manual checks)

### Task 8 (RED): Add a failing durable-vs-terminate-after-run engine test

**Files:** `lib/graph/frame/engine_test.go`

**Context:** `engine_test.go` drives the real frame engine (`RunTick` / `transitionFrameEnd`) against a testcontainers Postgres. `transitionFrameEnd` calls `MarkInstanceTerminatedIfDone` at every frame-end. Today that predicate terminates ANY instance on drain (no `terminate_after_run` gate), gated only by the publisher-subscription and queued/running-frame clauses.

**Steps:**
1. Add `TestDurableByDefaultVsTerminateAfterRun`: provision two instances of a simple single-node template — instance A with no flag, instance B with `terminate_after_run = true`. NOTE: `engine_test.go` seeds instances via the local `seedTemplateAndInstance` SQL helper, NOT the control-api create path — so set `terminate_after_run = true` for instance B by passing it through that seed helper (extend the helper to set the column, or issue a direct `UPDATE rimsky_instances SET terminate_after_run = true WHERE id = …` after seeding). Drive each through one frame to terminal via the engine tick (enqueue a frame, advance, run the node to resolved, frame-end). After both frames end, read `rimsky_instances.terminated_at`.
2. Assert `terminated_at IS NULL` for A (durable default — survives its drain) and `terminated_at IS NOT NULL` for B.
3. Run it; confirm it FAILS today (current predicate terminates A on drain, so A's NULL assertion fails).

**Verification:** `! go test -run TestDurableByDefaultVsTerminateAfterRun ./lib/graph/frame/...`

### Task 9 (GREEN): Rewrite the terminal predicate in both drivers

**Files:** `lib/foundation/persistence/postgres/frames.go`, `lib/foundation/persistence/sqlite/frames.go`

**Steps:**
1. Rewrite `MarkInstanceTerminatedIfDone` (both drivers) to:
   - Add `AND i.terminate_after_run = true` (durable instances are never touched).
   - **Remove** the `rimsky_publisher_subscriptions` `NOT EXISTS` clause entirely.
   - **Remove** the queued/running-frames `NOT EXISTS` clause (strict semantics: do not wait for queued frames).
   - **Keep and extend** the in-flight node_run guard so it also excludes `parked` (defensive restatement of Pass 1 at the instance level):
   ```sql
   AND NOT EXISTS (
       SELECT 1 FROM rimsky_node_runs r
       JOIN rimsky_nodes n ON n.id = r.node_id
       WHERE n.instance_id = i.id
         AND (
              (r.phase IN ('pending','active','held') AND r.state IN ('stale','running'))
           OR r.phase = 'parked'
           OR r.state = 'parked'
         )
   )
   ```
   Preserve each driver's now-handling (Postgres `SET terminated_at = now()`; SQLite `SET terminated_at = ?` bound to `nowUTC()`) and update the function's doc comment to describe the durable-by-default + parked-aware semantics, removing the old publisher-subscription rationale.
2. Run the Task 8 test; confirm it now PASSES.

**Verification:** `go test -run TestDurableByDefaultVsTerminateAfterRun ./lib/graph/frame/...`

### Task 10 (RED): Add a failing test that a terminated instance's queued frame is not promoted

**Files:** `lib/foundation/persistence/sqlite/frames_terminated_guard_test.go` (new)

**Context:** Under strict semantics an instance can terminate while a frame is still `queued` (a mid-run message). `ListQueuedFramesReadyToStart` (both drivers) has no terminated-instance guard today.

**Steps:**
1. Add `TestQueuedFrameNotPromotedForTerminatedInstance`: seed an instance with `terminated_at` set and a `queued` frame; call `Frames().ListQueuedFramesReadyToStart`; assert the queued frame is NOT returned.
2. Run it; confirm it FAILS today (no guard, so the frame is returned).

**Verification:** `! go test -run TestQueuedFrameNotPromotedForTerminatedInstance ./lib/foundation/persistence/sqlite/...`

### Task 11 (GREEN): Guard queued-frame promotion against terminated instances

**Files:** `lib/foundation/persistence/postgres/frames.go`, `lib/foundation/persistence/sqlite/frames.go`

**Steps:**
1. In `ListQueuedFramesReadyToStart` (both drivers), add a guard so frames for terminated instances are excluded — join `rimsky_instances` and add `AND i.terminated_at IS NULL` (or an equivalent `NOT EXISTS` against a terminated instance row), matching each driver's query idiom.
2. Run the Task 10 test; confirm it now PASSES.

**Verification:** `go test -run TestQueuedFrameNotPromotedForTerminatedInstance ./lib/foundation/persistence/sqlite/...`

### Task 12: Rework blast-radius tests assuming auto-terminate-on-drain

**Files:** `lib/graph/frame/engine_test.go`, `lib/control/controlapi/app_test.go`, `lib/control/controlapi/assets_test.go`, `lib/control/controlapi/instances_test.go`, `lib/control/controlapi/instance_terminator_test.go`, `lib/control/controlapi/templates_test.go`, and any persistence frame test asserting the old drain/publisher-subscription behavior

**Context:** Many tests assumed an instance reaches terminal after its work settles, or set up the publisher-subscription carve-out the terminal predicate no longer reads. Under durable-by-default these break.

**Steps:**
1. In each named file, `rg` for `terminated_at`, `TerminatedAt`, `MarkInstanceTerminatedIfDone`, and any auto-terminate-on-drain assertion. For each hit, decide: does the test mean to assert termination (then provision the instance with `terminate_after_run: true`) or did it incidentally rely on the old default (then assert durability — `terminated_at` stays NULL after drain)? Tests that drive force-terminate / DELETE directly (e.g. `instance_terminator_test.go`, terminate-handler tests) are unaffected — leave them.
2. `rg` for `publisher_subscription` in the persistence frame tests and any terminal-predicate test; remove assertions that the terminal predicate withholds termination because an active publisher-subscription exists (that clause is gone).
3. Run the affected suites and fix each failure by the rule in step 1 until green. Do not weaken a test to pass — re-target its intent.

**Verification:** `go build ./... && go test ./lib/control/controlapi/... ./lib/graph/... ./lib/foundation/persistence/sqlite/... -count=1`

---

## Pass 4: Trace retention — frames + node_runs + event logs under one policy

**Goal:** Replace the run-only retention prune with a whole-trace reaper: terminal frame rows (cascading their node_runs) reaped by the lesser of a trailing time window and a most-recent-frames count cap; audit + named event logs reaped by the same time window; in-flight (including parked-held) frames always exempt. Add the `trace_trailing` config knob and the event-log retention methods that do not exist today.
**Scope:** Tasks 13–17
**End state:** working
**Verification:** `go build ./... && go test ./lib/foundation/persistence/sqlite/... ./lib/runtime/... ./lib/graph/scheduler/... -run 'Retention|Trace|Sweep' -count=1`

### Task 13 (RED): Add a failing whole-trace retention test

**Files:** `lib/foundation/persistence/sqlite/trace_retention_test.go` (new)

**Context:** Today `PruneOldRunsForRetention` keeps frame ROWS forever (deletes only their node_runs beyond `recentFramesKept`); the audit log (`rimsky_events`) and named events (`rimsky_node_events`) have NO time-based retention method at all (`EventTable` exposes `Append`/`List`/`LastTerminalByNodes`; `NodeEventTable` exposes `Insert`/`LatestByName`/`DeleteByInstance`).

**Steps:**
1. Using the row-seeding pattern from `lib/foundation/persistence/sqlite/queue_park_test.go` (NOT `frames_retention_test.go`, which seeds nothing), seed one instance with: several old terminal frames (each with node_runs, `ended_at` in the past), one recent terminal frame, one in-flight `running` frame whose only non-terminal run is `parked` (the parked-held exemption case), plus old and recent rows in `rimsky_events` (`occurred_at`) and `rimsky_node_events` (`emitted_at`).
2. Add `TestTraceRetentionReapsWholeTrace` that exercises the whole-trace reap and asserts: old terminal frame ROWS and their node_runs are deleted; old audit + named events are deleted; the recent terminal frame, the in-flight parked-held frame, and recent events all survive. IMPORTANT: the reap spans THREE persistence methods — `Frames().PruneTraceForRetention(ctx, recentFramesKept, cutoff)` (frames + cascade node_runs ONLY — it does NOT touch events), `Events().DeleteOlderThan(ctx, cutoff)` (audit log), and `NodeEvents().DeleteOlderThan(ctx, cutoff)` (named events). The test must call all three (the orchestration that calls them together is `SweepRunTreeRetention`, reshaped in Task 17; here we assert the persistence layer directly). These methods don't exist yet — land their signatures as no-op stubs in this task (added to the `FrameTable`/`EventTable`/`NodeEventTable` interfaces alongside the existing methods) so the test compiles and fails on assertions; Tasks 15/16 fill the bodies and Task 15 removes the old `PruneOldRunsForRetention`.
3. Run it; confirm it FAILS today (the stub bodies do nothing, so no rows are deleted and the "old rows gone" assertions fail).

**Verification:** `! go test -run TestTraceRetentionReapsWholeTrace ./lib/foundation/persistence/sqlite/...`

### Task 14: Add the `TraceTrailing` config knob

**Files:** `lib/runtime/retention_sweeps.go`, `lib/control/config/stores.go`

**Steps:**
1. `retention_sweeps.go`: add `TraceTrailing time.Duration` to `RetentionConfig` (document: trailing window for the whole trace — frames, node_runs, event logs; `<= 0` disables the time dimension; `RecentFramesKept` is the count dimension; reaping is the lesser of the two).
2. `stores.go`: add `TraceTrailing *time.Duration \`yaml:"trace_trailing"\`` to `yamlRetention`; add `defaultRetentionTraceTrailing = 30 * 24 * time.Hour` alongside the existing defaults; in `parseRetention`, seed `out.TraceTrailing = defaultRetentionTraceTrailing` and add the `if in.TraceTrailing != nil { non-negative check; out.TraceTrailing = *in.TraceTrailing }` block mirroring `lineage_trailing`.

**Verification:** `go build ./... && go test ./lib/control/config/... -count=1`

### Task 15: Replace the run-only prune with a whole-trace frame reaper

**Files:** `lib/foundation/persistence/frames.go` (interface), `lib/foundation/persistence/postgres/frames.go`, `lib/foundation/persistence/sqlite/frames.go`, `lib/foundation/persistence/sqlite/frames_retention_test.go` (retarget the stale caller)

**Steps:**
1. In the `FrameTable` interface (`lib/foundation/persistence/frames.go`), replace `PruneOldRunsForRetention(ctx, recentFramesKept int) (int, error)` with `PruneTraceForRetention(ctx context.Context, recentFramesKept int, cutoff time.Time) (int, error)` (document: deletes terminal frame ROWS older than `cutoff` OR beyond the `recentFramesKept` most-recent terminal frames per instance — the lesser-of bound; node_runs go via `ON DELETE CASCADE`; queued/running frames exempt; `recentFramesKept <= 0` disables the count bound and a zero `cutoff` disables the time bound).
2. Both drivers: implement `PruneTraceForRetention` by deleting from `rimsky_frames` (not `rimsky_node_runs`) where the frame is terminal (`state IN ('completed','failed')`) AND (`ended_at < cutoff` OR per-instance recency rank `> recentFramesKept`), combining the existing `PARTITION BY instance_id ORDER BY ended_at` window with the time predicate. Rely on the existing frame→node_run `ON DELETE CASCADE`. Keep the standalone-no-tx execution idiom the current method uses.
3. **Blast-radius sweep (compile break otherwise):** `rg -n 'PruneOldRunsForRetention' lib/` and update EVERY caller. Known callers: `SweepRunTreeRetention` (handled in Task 17) and `lib/foundation/persistence/sqlite/frames_retention_test.go::TestPruneOldRunsForRetention_NoTxPanic`, which calls the removed method directly — retarget it to `PruneTraceForRetention(ctx, 2, time.Time{})` (preserving its no-nil-tx-panic regression assertion against the new method). If `rg` surfaces any other caller, update it too. Removing an interface method without retargeting every caller breaks the whole package build.
4. Make the Task 13 test's frame/node_run assertions pass (events still pending Task 16).

**Verification:** `go build ./... && go test ./lib/foundation/persistence/sqlite/... -run 'Trace|Frame|Retention' -count=1`

### Task 16: Add time-based retention to the event accessors

**Files:** `lib/foundation/persistence/events.go`, `lib/foundation/persistence/node_events.go`, `lib/foundation/persistence/postgres/{events.go,node_events.go}`, `lib/foundation/persistence/sqlite/{events.go,node_events.go}`, `lib/foundation/signal/audit/audit_test.go` (test fake — add the new method)

**Steps:**
1. Add `DeleteOlderThan(ctx context.Context, cutoff time.Time) (int, error)` to the `EventTable` interface (deletes `rimsky_events` rows with `occurred_at < cutoff`) and to the `NodeEventTable` interface (deletes `rimsky_node_events` rows with `emitted_at < cutoff`), modeled on `LineageTable.DeleteOlderThan` (standalone, no caller tx).
2. Implement both in both drivers. (Named-event payloads may be blob-spilled — return any orphaned blob handles the same way `DeleteByInstance` does if that method already collects them; otherwise a plain row delete by timestamp is sufficient for the audit/named ledgers, matching the lineage sweep's row-only delete.)
3. **Blast-radius sweep (compile break otherwise):** widening an interface breaks every non-driver implementor. `rg -n 'persistence.EventTable|persistence.NodeEventTable' lib/ --type go` and find every type (including test fakes) that satisfies these interfaces. Known: `lib/foundation/signal/audit/audit_test.go::fakeEvents` implements `EventTable` (passed to `EmitSignal`, whose param is `persistence.EventTable`) — add a no-op `DeleteOlderThan(context.Context, time.Time) (int, error) { return 0, nil }` to it. Update any other implementor `rg` surfaces.
4. Make the Task 13 test's event assertions pass once the sweep (Task 17) calls these.

**Verification:** `go build ./... && go test ./lib/foundation/persistence/sqlite/... -run 'Event|Trace' -count=1 && go test ./lib/foundation/signal/... -count=1`

### Task 17 (GREEN): Reshape the retention sweep to drive the whole trace

**Files:** `lib/runtime/retention_sweeps.go`, `lib/runtime/retention_sweeps_test.go` (focused sweep test), `lib/graph/scheduler/scheduler.go`

**Steps:**
1. Reshape `SweepRunTreeRetention` (`retention_sweeps.go`) to: compute `cutoff = now.Add(-cfg.TraceTrailing)` when `TraceTrailing > 0` (else a zero `cutoff` disabling the time bound); call `tables.Frames().PruneTraceForRetention(ctx, cfg.RecentFramesKept, cutoff)`; then, when `TraceTrailing > 0`, call `tables.Events().DeleteOlderThan(ctx, cutoff)` and `tables.NodeEvents().DeleteOlderThan(ctx, cutoff)`. Log per-table counts. NOTE: the function signature currently takes `_ time.Time` (the `now` argument is passed by the scheduler at `scheduler.go` but discarded in the body) — rename the parameter from `_` to `now` so the cutoff can be computed; the call site already supplies it.
2. **Fix the internal early-return guard.** The body currently begins `if cfg.RecentFramesKept <= 0 { return 0, nil }`. Replace it with `if cfg.RecentFramesKept <= 0 && cfg.TraceTrailing <= 0 { return 0, nil }` — otherwise a config with ONLY `trace_trailing` set (no count cap) passes the scheduler gate but silently reaps nothing. This is a load-bearing property: "either retention dimension alone must reap."
3. In `scheduler.go`, change the run-tree retention gate (currently `if cfg.Persist != nil && cfg.Retention.RecentFramesKept > 0`) to fire when EITHER dimension is enabled: `if cfg.Persist != nil && (cfg.Retention.RecentFramesKept > 0 || cfg.Retention.TraceTrailing > 0)`.
4. Add `TestSweepRunTreeRetention_TraceTrailingOnly` in `lib/runtime/retention_sweeps_test.go`: build a sqlite-backed `persistence.Tables` (follow the sqlite driver's test-open helper used across `lib/foundation/persistence/sqlite/*_test.go`; `lib/runtime` may import the sqlite driver in a `_test.go` — no import cycle since the sqlite driver does not import `lib/runtime`), seed old + recent terminal frames and old + recent event rows, then call `SweepRunTreeRetention(ctx, runtime.RetentionConfig{TraceTrailing: <window covering the old rows>} /* RecentFramesKept unset */, tables, now, log)`. Assert old frames/node_runs/events are reaped and recent survive. Confirm this test FAILS against the un-fixed guard (step 2 not yet applied → early-return → nothing reaped) and PASSES once steps 1–2 land — this is what makes the trace_trailing-only path checkable.
5. Run the Task 13 persistence test (unaffected by Task 17 — it calls the persistence methods directly) and the new sweep test; both pass.

**Blast-radius note:** `test/scenarios/retention_sweep_e2e_test.go::TestRetentionSweepsReapOnTick` is the existing end-to-end exercise of this exact function. It sets `RecentFramesKept=2` (no `TraceTrailing`) and asserts only on `rimsky_node_runs` and `rimsky_lineage` rows — it never asserts a `rimsky_frames` row survives — so it remains green under the frame-row-deleting reshape (the surviving frames' runs still survive; pruned runs are now cascade-deleted instead of directly). It is covered by the Manual-checks scenario run; no edit needed, but do not be surprised by it.

**Verification:** `go test -run 'TestSweepRunTreeRetention_TraceTrailingOnly' ./lib/runtime/... && go test -run TestTraceRetentionReapsWholeTrace ./lib/foundation/persistence/sqlite/... && go build ./...`

---

## Pass 5: Concept-doc mutations

**Goal:** Apply the spec's `## Design changes` to the three concept docs so the design matches the code. Doc edits only — no runtime behavior, so no red test; verified by asserting the new text is present and the contradicting text is gone.
**Scope:** Tasks 18–20
**End state:** working
**Verification:** the per-task grep checks below all succeed, and `go build ./...` is unaffected.

### Task 18: `concepts/instance.md` — durable-by-default invariants

**Files:** `.ok-planner/design/concepts/instance.md`

**Steps:**
1. In Invariants, leave the existing combined termination invariant (the "An instance is terminal exactly when its terminal timestamp is set … the instance key is freed for reuse only by the subsequent row delete" bullet) unchanged, and add two new invariant bullets alongside it, verbatim from the spec's `## Design changes` (durable-by-default / `terminate_after_run` strict semantics; and termination independent of `concept:sensor` / `concept:publisher-subscription` and node presence).
2. Append the dated Notes entry from the spec's `## Design changes` (the `2026-06-03 — Durable-by-default lifecycle …` entry).

**Verification:** `rg -q "durable by default" .ok-planner/design/concepts/instance.md && rg -q "2026-06-03 — Durable-by-default" .ok-planner/design/concepts/instance.md`

### Task 19: `concepts/frame.md` — frame begin/end + parked + contained RunScopes

**Files:** `.ok-planner/design/concepts/frame.md`

**Steps:**
1. In "What it is", replace the frame-end sentence ("It ends when no run row for the instance remains `stale` or `running`…") with the spec's new frame-end text (ends only when every node_run is resolved — none `stale`, `running`, or `parked`; a parked node_run holds its frame open).
2. In "What it is", replace the existing frame-begin clause ("A frame begins when a node receives an invalidate (in-frame cascade walk) OR when pending boundary-crossing messages get delivered") with the spec's new begin text (begins only on invalidation — operator/user or message delivery; park-wake resumes, does not begin; preserve the "(see Message delivery below)" pointer).
3. In the Held-frames section, append the spec's held-frame clarification sentence (a held frame is a running frame with a parked/acquisition-pending node_run; diagnostic and frame-end rule now agree).
4. Append the dated Notes entry from the spec (the `2026-06-03 — Frame-end definition corrected …` entry, including the "contains" not "spans" clarification for RunScopes).

**Verification:** `rg -q "stale.*running.*or .parked" .ok-planner/design/concepts/frame.md && rg -q "park-wake" .ok-planner/design/concepts/frame.md && rg -q "2026-06-03 — Frame-end definition corrected" .ok-planner/design/concepts/frame.md`

### Task 20: `concepts/event-log.md` — audit log under trace retention

**Files:** `.ok-planner/design/concepts/event-log.md`

**Steps:**
1. In Invariants, replace the bullet "No built-in retention; operator-managed retention is required." with the spec's new retention invariant (reaped under the shared trailing trace-retention window plus instance-delete cascade; append-only within the window).
2. In Boundaries' "Does NOT own" list, replace "retention policy (operator-managed)" with the spec's new phrase (does not own the trace-retention window value — a shared per-instance bound that also governs frames and node_runs, applied here as a reaping cutoff).
3. Append the dated Notes entry from the spec (the `2026-06-03 — Audit log brought under the shared trace-retention window …` entry).

**Verification:** `! rg -q "No built-in retention; operator-managed retention is required" .ok-planner/design/concepts/event-log.md && rg -q "shared trailing trace-retention window" .ok-planner/design/concepts/event-log.md`

---

## Pass 6: Acceptance — durable lifecycle & `terminate_after_run` against the real image stack

**Goal:** Prove, against the real assembled product (the `rimsky-all-in-one:latest` image driven by the services testcontainers harness `lib/services/test/harness`), that a durable instance survives repeated real external change (scenario 1, the headline) and that a `terminate_after_run` instance runs once and refuses further work (scenario 2). Harness reality (grounded): the only value-delivering peer image available is the real `rimsky-sensor-http` image (scenario 1's driver); the harness's executor is the bundled Success-stub image (`StartExecutorStubOnNetwork`), which emits a real terminal Success over the wire to a real node-run. For these two scenarios the behavior under test is **rimsky's own lifecycle** (durable-by-default; terminate-after-one-run), so the real sensor image is the value-delivering component for scenario 1, and the Success stub is a sufficient real per-node trigger for scenario 2 — it is not the component the feature exists to exercise.
**Scope:** Tasks 21–23
**End state:** working
**Verification:** `make core-images && make service-images && go test -run 'TestSensorHTTP_DurableAcrossFires|TestTerminateAfterRun_EndToEnd' ./lib/services/test/scenarios/... -count=1`

### Task 21: Rework the sensor-cascade e2e into the durable-across-fires acceptance gate

**Files:** `lib/services/test/scenarios/sensor_cascade_e2e_test.go`

**Context:** This test already wires a real `rimsky-sensor-http` image polling a real host `httptest.Server` into rimsky's real cascade via the `lib/services/test/harness`. Under durable-by-default the sensor instance must stay alive across many fires with no flag and no publisher-subscription coupling.

**Steps:**
1. Read the existing test and the `lib/services/test/harness` API it uses (stack bring-up, instance create, message/observe assertions).
2. Add `TestSensorHTTP_DurableAcrossFires`: create the sensor-driven instance with NO `terminate_after_run` flag; mutate the host source body N≥3 times; after each fire assert the `reactor` node re-runs (stale→fresh) AND `GET /instances/{id}` shows `terminated_at` unset (the instance is never reaped). Assert the negative-control `bystander` never fires.
3. Run it (after building images); confirm it PASSES (durable default landed in Pass 3).

**Verification:** `make core-images && make service-images && go test -run TestSensorHTTP_DurableAcrossFires ./lib/services/test/scenarios/... -count=1`

### Task 22: Add the `terminate_after_run` end-to-end acceptance test

**Files:** `lib/services/test/scenarios/terminate_after_run_e2e_test.go` (new)

**Steps:**
1. Using the same harness with the bundled Success-stub executor image (`harness.StartExecutorStubOnNetwork` + `WithExecutor`, as the existing `sensor_cascade_e2e_test.go` does), add `TestTerminateAfterRun_EndToEnd`: create an instance with `terminate_after_run: true` against the all-in-one image's real `POST /instances`; trigger one invalidation; let the frame run the stub executor to terminal Success; assert `GET /instances/{id}` shows `terminated_at` set; then POST a follow-up message to `/instances/{id}/messages` and assert it is rejected (the terminated-instance rejection path).
2. Run it; confirm it PASSES.

**Verification:** `make core-images && make service-images && go test -run TestTerminateAfterRun_EndToEnd ./lib/services/test/scenarios/... -count=1`

### Task 23 (revert-check): Prove the durable acceptance gate is meaningful

**Files:** `lib/foundation/persistence/postgres/frames.go` (temporary edit, reverted within the task)

**Context:** Scenarios 1–2 are green-on-arrival (the feature landed in Pass 3), so prove the gate would catch a regression via the proof-first revert-check fallback (no `git stash`/`checkout` — edit out and back). The gate test here is the Pass-3 engine test `TestDurableByDefaultVsTerminateAfterRun`, which runs against **Postgres** (`engine_test.go` uses the pgtest driver) — so the temporary edit must be to the **Postgres** driver for the edit to reach the test.

**Steps:**
1. Temporarily edit `MarkInstanceTerminatedIfDone` (Postgres driver) to drop the `i.terminate_after_run = true` gate (re-introducing auto-terminate-on-drain).
2. Run `TestDurableByDefaultVsTerminateAfterRun`; confirm it now FAILS (durable instance A is wrongly terminated on drain). Capture the red output.
3. Restore the gate by editing it back to the Task 9 form; re-run the same test; confirm it PASSES again.

**Verification:** `go test -run TestDurableByDefaultVsTerminateAfterRun ./lib/graph/frame/... -count=1` (passes after restore)

---

## Pass 7: Acceptance — parked-holds-frame & trace retention against the real runtime

**Goal:** Prove the remaining two scenarios end-to-end: a parked node holds its frame open and its instance is not terminated until a real async callback resolves it (scenario 3), and a live durable instance's old trace is reaped under a configured retention window while in-flight and recent trace survive (scenario 4). This is the final pass; it ends `working` on a green acceptance gate.

**Harness choice (grounded — read this before implementing):** these two scenarios use the root-module `test/scenarios/` harness, NOT the `lib/services/test/` image harness, for grounded reasons: (1) the services harness's only executor image (`lib/services/test/stubexecutor/main.go`) emits Success/Error **only — it has no Park outcome**, so the real-image stack cannot drive a park; (2) the `rimsky-all-in-one:latest` image bakes its `cfg:retention` (`dockerfiles/all-in-one.rimsky.yml`) and the harness exposes no retention override, so a short reaping window can't be configured against it. The `test/scenarios/` harness is the project's established real-runtime end-to-end harness: it brings up the real control-api over HTTP (`h.ControlBase`), the real supervisor with a real async-callback listener (`h.Supervisor.CallbackAddr()`), the real scheduler (`scheduler.Tick` with a real `runtime.RetentionConfig`), and the real frame engine, all against a testcontainers Postgres, with a scriptable executor stub that **can** emit Park (`.Park(...)`). It already proves both behaviors in-suite (`test/scenarios/parked_lifecycle_test.go`, `test/scenarios/retention_sweep_e2e_test.go`) — these tasks extend that proven harness. The value-delivering behavior under test (frame-hold-while-parked; trace reaping) is rimsky's real runtime; the executor's Park is the trigger, not the component under test.
**Scope:** Tasks 24–26
**End state:** working
**Verification:** `go test -run 'TestParkedHoldsFrame_EndToEnd|TestTraceRetention_EndToEnd' ./test/scenarios/... -count=1` (testcontainers Postgres; Docker required)

### Task 24: Parked-holds-frame end-to-end acceptance test

**Files:** `test/scenarios/parked_holds_frame_e2e_test.go` (new)

**Context — read carefully; the wake mechanism is the load-bearing detail.** The Pass-1 fix concerns the `parked` node state, which is produced by a `Park` executor terminal. A `parked` node is NOT woken by the `/v1/callback` endpoint — that endpoint serves the separate `AwaitAsyncCallback` terminal, which keeps a node `running` (and a running node already holds its frame, so it never exercised the Pass-1 bug). The callback handler in fact rejects a parked run (`rejected_run_parked`), and a `Park` terminal registers no `async_ack_id`. A parked node is woken only by admin/cascade invalidate or the snooze sweep. So this acceptance test must use the **true park + admin-invalidate wake** flow, modeled on `test/scenarios/parked_lifecycle_test.go::TestParkedLifecycleResumeOnExternalInvalidate`: a scripted executor `.Park(genv1.ParkReason_PARK_REASON_AWAIT_CALLBACK, …)` on first dispatch then a resolving Success on the wake dispatch; `h.WaitForNodeState(..., cascade.NodeStateParked, …)`; the wake is a POST to `{h.ControlBase}/admin/instances/{id}/nodes/{node_id}/invalidate` (NOT `/v1/callback`). (This grounds the spec's scenario-3 wording "awaiting an async callback" to the real parked-node wake path — see the plan handoff note; the spec's intent, "a parked node holds its frame open and the instance is not terminated until the parked work resolves," is preserved exactly.)

**Steps:**
1. Read `parked_lifecycle_test.go` for the harness API and the park-then-external-invalidate flow. Note `h.CreateInstance(tid, ck, params)` (in `test/support/scenario/harness.go`) does not set `terminate_after_run`; to set the flag, POST the create request directly to `h.ControlBase + "/instances"` with `"terminate_after_run": true` in the JSON body (the real HTTP create path), or extend the harness `CreateInstance` helper to accept it.
2. Add `TestParkedHoldsFrame_EndToEnd`: create an instance with `terminate_after_run: true` whose node's scripted executor emits `Park` on first dispatch and `Success` on the next. Wait for the node to reach `parked`.
3. While parked, assert `GET {h.ControlBase}/instances/{id}` shows `terminated_at` unset (the instance is NOT terminated while parked, even though `terminate_after_run` is set), AND the held-frames diagnostic `GET {h.ControlBase}/admin/diagnostics/held-frames` shows this frame held (running) — the Pass-1 fix.
4. Wake the parked node via `POST {h.ControlBase}/admin/instances/{id}/nodes/{node_id}/invalidate`. Assert the node resumes and resolves to Success, the frame ends, and only THEN does `GET /instances/{id}` show `terminated_at` set (the Pass-3 strict terminate-after-the-run).
5. Run it; confirm it PASSES (Pass-1 holds the frame while parked; Pass-3 terminates after the real frame-end).

**Verification:** `go test -run TestParkedHoldsFrame_EndToEnd ./test/scenarios/... -count=1`

### Task 25: Trace-retention end-to-end acceptance test

**Files:** `test/scenarios/trace_retention_e2e_test.go` (new)

**Context:** Model on `test/scenarios/retention_sweep_e2e_test.go::TestRetentionSweepsReapOnTick`, which creates an instance via `h.CreateInstance`, builds a `scheduler.Config{Retention: runtime.RetentionConfig{...}}`, and calls `scheduler.Tick(h.Ctx, cfg)` synchronously (use the harness's `NoScheduler` mode so the background tick loop doesn't race the seeded state).

**Steps:**
1. Add `TestTraceRetention_EndToEnd`: create a durable instance (no flag) and drive it through several frames (several invalidations each resolved by the executor stub), accumulating terminal frames + node_runs + audit/named events; also leave one in-flight (parked-held) frame.
2. Call `scheduler.Tick` with `runtime.RetentionConfig{RecentFramesKept: 2, TraceTrailing: <short window covering the old rows>}`.
3. Assert via the control-API read surfaces (frames feed / event feed) and/or direct DB reads that old terminal frames + their node_runs + old events are deleted, while the most-recent frames, the in-flight parked-held frame, and recent events survive — and no event references a deleted frame's node that was removed.
4. Run it; confirm it PASSES.

**Verification:** `go test -run TestTraceRetention_EndToEnd ./test/scenarios/... -count=1`

### Task 26 (revert-check): Prove the parked-holds-frame acceptance gate is meaningful

**Files:** `lib/foundation/persistence/sqlite/frames.go` (temporary edit, reverted within the task)

**Context:** Scenario 3 is green-on-arrival (the frame-hold fix landed in Pass 1). Prove the gate would catch a regression via the proof-first revert-check fallback (edit out and back — no `git stash`/`checkout`/`reset`).

**Steps:**
1. Temporarily remove the parked clause (`OR r.phase = 'parked' OR r.state = 'parked'`) from `ListRunningFramesNoPendingNodes` in the SQLite driver (re-introducing the bug where a parked-only frame ends).
2. Run the fast Pass-1 mechanism test `go test -run TestParkedNodeRunHoldsFrameOpen ./lib/foundation/persistence/sqlite/...`; confirm it now FAILS (the parked-only frame is reported ended). Capture the red output. (This is the predicate the parked acceptance gate rides on; running the fast unit proof avoids a redundant full testcontainers cycle.)
3. Restore the parked clause; re-run the same test; confirm it PASSES again.

**Verification:** `go test -run TestParkedNodeRunHoldsFrameOpen ./lib/foundation/persistence/sqlite/... -count=1` (passes after restore)

---

## Manual checks after completion

These require a Docker host and locally-built images; run them once the automated passes are green (they are the Docker-backed counterparts of the in-process gates above, surfaced here because they are slow/environment-dependent, not because they need human judgment):

- Full Postgres-driver suite: `go test ./lib/foundation/persistence/postgres/... -count=1` (testcontainers Postgres).
- Race-sensitive predicate paths: `go test ./lib/foundation/persistence/postgres/... ./lib/runtime/... ./lib/graph/scheduler/... -race -count=3`.
- Full scenario suites: `go test ./test/scenarios/... -count=1` and `make core-images && make service-images && go test ./lib/services/test/... -count=1`.
- `make lint` and `make test-all`.
