# Last-Mile Stability Implementation Plan

**Spec:** .ok-planner/specs/2026-06-11-last-mile-stability-design.md
**Goal:** Consolidate rimsky's hand-paired duplicate code paths, close every wire-contract-vs-runtime gap the spec enumerates, and land a race-honest test harness first so the consolidations are verifiable.
**Architecture:** Go workspace, five modules (root, lib/foundation, lib/protocols, lib/services, examples). Three role binaries (scheduler / supervisor / control-api) over a shared persistence layer with two drivers (postgres `lib/foundation/persistence/postgres/`, sqlite `lib/foundation/persistence/sqlite/`) kept equivalent by the driver-parity suite (`lib/foundation/persistence/conformance/` — note: distinct from `concept:conformance`, the protocol-conformance subcommands). Design docs under `.ok-planner/design/` mutate in the same passes as the code that conforms to them.
**Tech Stack:** Go 1.x, gRPC + protobuf (`make proto-gen`), go-chi/chi, pgx/v5, modernc.org/sqlite, testcontainers-go (Docker required for scenario/integration passes), log/slog, Prometheus client.

**Ordering constraint (TD-harness-first-ordering):** Passes 1–3 (harness) precede all consolidation passes; the child-execution unification (Passes 26–28) is the last consolidation. Do not reorder.

**Conventions for every pass:**
- After any Go change: `go build ./... && go test ./...` in the affected module(s), then `make lint`. Proto changes: `make proto-gen` first.
- No commits, no branches — working-tree edits only.
- Design-doc edits are current-state-only: no "previously", no dated notes, no TODO/deferred language.
- The spec is at `.ok-planner/specs/2026-06-11-last-mile-stability-design.md`; TD-/STORY- references below name its sections.

---

## Pass 1: Race-detection gates in the Makefile

**Goal:** TD-race-gate-split — thin `-race` slice in `test-all`, full `make test-race` target required by the release chain.
**Scope:** Tasks 1–2
**Falsifier:** `make test-all` still runs no `-race` over the race-sensitive packages, OR no `test-race` target exists, OR the `release:` prerequisite list does not include `test-race`.

### Task 1: Add the thin race slice to `test-all` and a `test-race` target

**Files:** `Makefile`

**Steps:**
1. Read the `test-all:` target (currently begins at the `go test -parallel 4 ./...` line under the long Subscribe-flake comment, Makefile ~line 57). Leave its existing lines intact for now (the `-parallel 4` caps are removed later in Task 16, not here).
2. Append to `test-all` a thin race slice line:
   `go test -race -count=1 ./lib/runtime/... ./lib/graph/scheduler/... && cd lib/foundation && go test -race -count=1 ./persistence/postgres/... ./persistence/sqlite/...`
   (match the Makefile's existing `cd`-per-module style; the foundation packages are a separate Go module so they need the `cd`).
3. Add a new top-level target `test-race:` running the full treatment over the same set with `-count=3`:
   ```
   test-race:
   	go test -race -count=3 ./lib/runtime/... ./lib/graph/scheduler/...
   	cd lib/foundation && go test -race -count=3 ./persistence/postgres/... ./persistence/sqlite/...
   ```
   Add `test-race` to the `.PHONY` line if the Makefile maintains one.
4. Run `make test-race` and confirm it executes (the postgres/sqlite packages use testcontainers — Docker must be running; expect several minutes).

### Task 2: Require `test-race` in the release chain

**Files:** `Makefile`

**Steps:**
1. Edit the release prerequisite list (Makefile ~line 275): `release: lint license-lint core-images service-images test-all scan push-images` → insert `test-race` immediately after `test-all`.
2. Run `make -n release` and confirm the dry-run output shows `test-race` in the chain (it will fail later at docker-push steps without credentials — only confirm ordering, do not run the real chain).

---

## Pass 2: Deterministic race-injection hooks at three defended seams

**Goal:** TD-race-injection-hooks — extend the `PostCommitHook` pattern with deterministic injection tests for the acquire-unavailable abandon path, the held-claim aggregate check-and-fire, and the orphan-reaper vs in-flight-terminal overlap. (The fourth seam — the folded ownership-bail — is pinned in Pass 10 after the fold exists.)
**Scope:** Tasks 3–5
**Falsifier:** Any of the three seams has no test that deterministically forces its race (a hook exists but no test drives it, or the test only exercises the happy path), OR the injected race is simulated by stubbing the very component whose defense is under test.

### Task 3: Injection test — acquire-unavailable abandons partial opens exactly once

**Files:** `lib/runtime/runner.go` (or `lib/runtime/runner_acquire_claims.go` — wherever the acquisition loop lives; follow the existing `PostCommitHook` placement at `lib/runtime/runner.go:240-247`), new test under `test/scenarios/`

**Steps:**
1. Read `lib/runtime/runner.go:240-247` (`PostCommitHook`) and its consumer `test/scenarios/verify_before_run_post_commit_test.go` to absorb the existing hook pattern: an exported, nil-by-default func field on `RunArgs`, invoked at a precise seam, set only by tests.
2. Read `lib/runtime/runner_lifecycle.go::handleAcquireUnavailable` (line 42) and the acquisition path that produces `acq.PartialLocks`. Add a hook field (e.g. `PreAcquireUnavailableHook`) invoked just before `abandonPartialLocks`, following the `PostCommitHook` doc-comment style (state it exists solely for deterministic injection tests).
3. Write a scenario test (pattern-match `verify_before_run_post_commit_test.go`: real Postgres via testcontainers, stub producer registry): a node requiring two claims where producer #1 Opens successfully and producer #2 returns the unavailable sentinel. Assert: producer #1 receives exactly one `Abandon`; no claim-handle rows survive; the run resolves through the error path with class `acquire/unavailable`.
4. Run the new test: `go test ./test/scenarios/ -run <TestName> -count=1`. Confirm green, then `go build ./... && make lint`.

### Task 4: Injection test — held-claim aggregate check-and-fire fires exactly once under racing finals

**Files:** new test under `test/scenarios/`, hook (if needed) in the held-claim check-and-fire site (`lib/runtime/` — locate via `rg -n "check-and-fire\|aggregate" lib/runtime/` and the auto-terminal chain `lib/runtime/auto_terminal_chain.go`)

**Steps:**
1. Read `lib/runtime/auto_terminal_chain.go` and `concept:auto-terminal` (`.ok-planner/design/concepts/auto-terminal.md`) to locate where the holding-subgraph-complete check decides to fire the producer verb.
2. Add a hook at the check-and-fire decision seam allowing a test to interleave a second contender (mirroring `PostCommitHook` style).
3. Write a scenario test: two co-holding node-runs reach terminal nearly simultaneously (use the hook to force the second check to run after the first check but before the first fire); assert the producer verb fires exactly once and the claim-handle row is deleted exactly once.
4. Run the test with `-race -count=3`; confirm green; `go build ./... && make lint`.

### Task 5: Injection test — orphan reaper vs in-flight terminal cannot double-resolve

**Files:** new test under `test/scenarios/`, hook (if needed) in `lib/runtime/orphan_reaper.go`

**Steps:**
1. Read `lib/runtime/orphan_reaper.go` (the `DeleteIfExpired` call at line ~115) and the terminal-release path `lib/runtime/runner_terminal_release.go` (claimant-guarded `Delete` at line ~77).
2. Write a scenario test: a claim-handle row at the edge of expiry; force the interleaving where the reaper sweep and the owning supervisor's terminal release race (hook or controlled clock). Assert exactly one of the two paths deletes the row (the claimant guard makes the loser a no-op) and the producer verb count is correct per `concept:terminal-resolution` (the reaper fires no verb).
3. Run with `-race -count=3`; confirm green; `go build ./... && make lint`.

---

## Pass 3: Polling audit — event-driven waits where polling masks ordering

**Goal:** TD-polling-audit — convert the subset of sleep/poll test waits that mask ordering assumptions to event-log-tail waits; leave genuine outcome-waits alone.
**Scope:** Tasks 6–7
**Falsifier:** No audit artifact exists distinguishing kept-vs-converted sites, OR converted tests still sample mutable state in a sleep loop where an event-log wait was available, OR tests that previously asserted ordering now assert nothing.

### Task 6: Audit and classify the polling sites

**Files:** none (working notes only — do not commit a notes file; the classification lands as the diff of Task 7)

**Steps:**
1. Enumerate: `rg -ln "time.Sleep" test/ lib/ --type go` (~113 files). For each, classify: (a) genuine outcome-wait with deadline — keep; (b) polling that masks an ordering assumption (the test passes only if A lands before B) — convert; (c) fixed sleep standing in for "wait until X happened" where X has an event-log record — convert.
2. Read the event-log read surface (`lib/foundation/persistence/` events table accessors; `rg -n "Events()" lib/foundation/persistence/*.go`) and any existing helper tests use to tail events. If no shared helper exists, note the need; Task 7 creates it.

### Task 7: Create the event-wait helper and convert the flagged sites

**Files:** `test/support/` (new helper file, e.g. `test/support/eventwait/eventwait.go`), the converted `*_test.go` files

**Steps:**
1. Create a helper in `test/support/` (match the existing support-package layout there): `WaitForEvent(ctx, db, matcher, deadline)` that polls the append-only event log (a durable record — reading it cannot miss a transient transition) and fails fast with the events actually seen on timeout.
2. Convert each site classified (b)/(c) in Task 6 to use the helper, preserving each test's assertion strength (the test must still fail if the awaited fact never occurs).
3. Run the converted tests: `go test ./test/scenarios/... -count=1` (Docker required) plus affected `lib/` packages. Confirm green; `make lint`.

---

## Pass 4: Subscription mounting — desired-state rows, reconciler, observable state

**Goal:** TD-subscription-mounting-state + TD-subscription-reconciler — `mounting` state, rows created in it, async reconciliation with no attempt cap, per-subscription state on the instance surface; matching design-doc mutations.
**Scope:** Tasks 8–13
**Falsifier:** Instance-create still performs the Subscribe RPC inline with a bounded retry budget, OR rows are still created as `active`, OR a slow-publisher subscription ends in `failed`, OR the instance-detail response exposes no per-subscription state.

### Task 8: Migration — add `mounting` to the subscription state set

**Files:** `lib/foundation/persistence/postgres/migrations/009-subscription-mounting.sql` (new), `lib/foundation/persistence/sqlite/migrations/009-subscription-mounting.sql` (new)

**Steps:**
1. Read `rimsky_publisher_subscriptions` in both `001-schema.sql` files to see how `state` is constrained (CHECK constraint or free text) and copy the prevailing migration style from `008-claim-handle-payload.sql` in each driver.
2. Write migration 009 in each driver updating the state constraint to `('mounting','active','failed','stopped')`. Postgres: `ALTER TABLE ... DROP CONSTRAINT ... / ADD CONSTRAINT ...` (or the file's prevailing idiom). SQLite cannot alter CHECKs: rebuild the table per the SQLite migration idiom used by earlier migrations (create new, copy, drop, rename) — pre-v1, no compat shim.
3. Run the migration tests: `cd lib/foundation && go test ./persistence/postgres/... ./persistence/sqlite/... -run Migrate -count=1`.

### Task 9: State constant and persistence accessors

**Files:** `lib/foundation/persistence/publisher_subscriptions.go`

**Steps:**
1. Add `PublisherSubscriptionStateMounting = "mounting"` beside the existing constants (lines ~26-27).
2. A list-by-state accessor already exists (`lib/foundation/persistence/publisher_subscriptions.go::ListByState#71-73`, no tx parameter) — confirm it serves the reconciler's selection; extend only if the reconciler needs a shape it lacks, matching the existing accessor's convention (no tx parameter).
3. `cd lib/foundation && go build ./... && go test ./persistence/... -count=1`.

### Task 10: Instance-create inserts `mounting` rows and stops calling Subscribe inline

**Files:** `lib/runtime/publishers.go`

**Steps:**
1. In `StartPublisherSubscriptionsForInstance` (line ~72): set `State: persistence.PublisherSubscriptionStateMounting` on the inserted row; delete the inline `Subscribe` RPC call, the `subscribeRetryAttempts` / `subscribeRetryBase` retry loop, and the `markSubscriptionFailed` flip on RPC error. Keep the unknown-publisher check — an unregistered publisher name is non-retryable and flips the row to `failed` with a reason (this is the one `failed` writer left in the create path).
2. **Load-bearing property:** instance-create must not block on, or fail because of, publisher reachability. Do not retain any synchronous RPC in this function.
3. Update this file's header comments (they describe the retry budget — now false; see also Task 78's sweep, but fix this file's own comments here).
4. `go build ./... && go test ./lib/runtime/... -count=1`.

### Task 11: The reconciliation worker

**Files:** `lib/runtime/publishers.go` (new exported worker func), `lib/control/config/controlapi.go` (start it)

**Steps:**
1. Read `ResyncPublisherSubscriptions` in `lib/runtime/publishers.go` and its startup call site in `lib/control/config/controlapi.go::StartControlAPI` — the reconciler lives beside resync (the publisher registry is control-api-side).
2. Add `RunPublisherSubscriptionReconciler(ctx, deps, interval)`: loop on a ticker; each tick, list `mounting` rows (plus `failed` rows whose failure class is retryable, if resync distinguishes — match resync's selection); for each, call `Subscribe`; on success flip to `active`; on RPC failure leave `mounting` (no attempt cap, the backoff is the tick interval); on non-retryable error (unknown publisher) flip to `failed` with reason. **Load-bearing property: there is no attempt budget — do not add one.** Log state flips with the existing `publisher.subscribe.*` log-key style.
3. Start the worker from `StartControlAPI` next to the resync call, wired to the same registry and persistence handles; stop it on shutdown with the handle pattern the file already uses for background loops.
4. `go build ./... && go test ./lib/runtime/... ./lib/control/... -count=1`.

### Task 12: Expose per-subscription state on the instance surface

**Files:** `lib/control/controlapi/instances.go`

**Steps:**
1. Locate the instance-detail GET handler in `lib/control/controlapi/instances.go`. Add a `subscriptions` array to its response: one entry per `rimsky_publisher_subscriptions` row for the instance — `publisher_name`, `kind`, `state`, `started_at`, and the failure reason field if the row carries one. Match the handler's existing response-shape conventions (snake_case JSON, omitempty style).
2. Add/extend the handler's unit or scenario test asserting the array appears with the row's state.
3. `go build ./... && go test ./lib/control/... -count=1 && make lint`.

### Task 13: Design-doc mutations — `concept:publisher-subscription` and `concept:publisher`

**Files:** `.ok-planner/design/concepts/publisher-subscription.md`, `.ok-planner/design/concepts/publisher.md`

**Steps:**
1. Apply the spec's `## Design changes` bullet for `publisher-subscription.md` exactly: rewrite the Boundaries sentence naming the state values AND the lifecycle-state invariant to the four-state set with the reconciler semantics; add the desired-state sentence to Purpose. Current-state-only prose; self-contained (no file paths).
2. Apply the spec's two-part bullet for `publisher.md`: add the provider framing to Definition/Purpose; replace the 3-retries-then-failed invariant (line ~32) with the reconciler invariant referencing `concept:publisher-subscription`.
3. Re-read both files end-to-end and confirm no remaining sentence contradicts the new state set or retry semantics.

---

## Pass 5: Acceptance — STORY-subscription-mounting (+ cap lift)

**Goal:** Deliver STORY-subscription-mounting end-to-end with its demo; re-point the services tests at observable state; lift both `-parallel 4` caps (TD-parallel-cap-removal).
**Scope:** Tasks 14–16
**Falsifier:** (from the spec story) A subscription that ends up unmounted with the operator unable to see that from the instance surface — the silent-201 behavior still exists; or `mounting` is observable but never reconciles without operator intervention under conditions that should recover.

### Task 14: Services tests wait on subscription state, not wall-clock

**Files:** `lib/services/test/scenarios/` (the sensor tests that poll "sensor never persisted subscription within 90s"), `lib/services/test/harness/rimsky.go`

**Steps:**
1. `rg -ln "never persisted a subscription" lib/services/test/` to find the affected tests (e.g. `lib/services/test/scenarios/sensor_object_store_e2e_test.go:441`). Add a harness helper that polls `GET /instances/{id}` until every subscription reports `active` (bounded deadline), and use it before the sensor-side assertions.
2. Rebuild images first (`make core-images service-images`), then run the affected scenario tests: `cd lib/services && go test ./test/scenarios/... -count=1`.

### Task 15: Acceptance demo for STORY-subscription-mounting

**Files:** `examples/subscription-mounting-demo.sh` (new — place beside existing demos; check `ls examples/` and match the prevailing demo layout; if demos live elsewhere, e.g. a `demos/` dir, follow that)

**Story:** STORY-subscription-mounting
**Proof form (from spec):** "demo — against a running stack, create an instance whose publisher is deliberately slow to respond; show the create returning immediately, the subscription visibly `mounting`, the flip to `active` once the publisher wakes, and the sensor's messages arriving."

**Steps:**
1. Write a script that: boots the all-in-one image plus a sensor container that is started *paused* (or delayed via entrypoint sleep); registers a template with a `publishers:` block; creates an instance and prints the immediate 201; polls `GET /instances/{id}` printing the subscription state (`mounting`); unpauses the sensor; polls again showing `active`; tails the instance's messages showing sensor data arriving. Use the real images (`make core-images service-images` precondition) and the real HTTP surface.
2. Run the script end-to-end; confirm each stage prints what the story promises.

### Task 16: Lift both `-parallel 4` caps

**Files:** `Makefile`

**Steps:**
1. In `test-all`, change `go test -parallel 4 ./...` → `go test ./...` and `cd lib/services && go test -parallel 4 $$(go list ./... | grep -v /node_modules/)` → same without `-parallel 4`. Replace the long Subscribe-flake comment block with a short note that subscription mounting is asynchronous and tests wait on observable state.
2. Run `make test-all` in full (Docker running; this is the load test of the new mounting path — the old flake reproduced at high parallelism). Confirm green.

---

## Pass 6: Single-process all-in-one entrypoint

**Goal:** TD-single-process-mode + TD-memory-gate-premise-corrected — the no-command entrypoint path runs migrate then all three roles in one process; the memory-blob gate's text becomes true; matching concept mutations.
**Scope:** Tasks 17–19
**Falsifier:** The no-command path still spawns child processes, OR single-role commands change behavior, OR `RIMSKY_PROCESS_ROLE=unified` is set anywhere except the genuine single-process mode, OR the memory-gate error text still claims the per-process binaries are the rejection reason in a way that misdescribes the new topology.

### Task 17: Extract importable role runners

**Files:** `cmd/rimsky-scheduler/main.go`, `cmd/rimsky-supervisor/main.go`, `cmd/rimsky-control-api/main.go`, new package `lib/control/launch/` (one file per role)

**Steps:**
1. Read all three role mains. Each loads config and calls its `config.Start*` (`lib/control/config/scheduler.go::StartScheduler#90`, `supervisor.go::StartSupervisor#124`, `controlapi.go::StartControlAPI#178`) plus role-specific wiring (metrics gauge refresher, observability handshake for the supervisor).
2. Create `lib/control/launch/` with `RunScheduler(ctx, ...) (stop func(), err)`, `RunSupervisor(...)`, `RunControlAPI(...)` — each containing the main's wiring verbatim (config load from the standard config path, Start call, background loops), returning a stop handle. The mains shrink to flag/env parsing + `launch.RunX` + signal wait. Behavior of each role binary must be byte-for-byte equivalent (same config keys, same ports, same logs).
3. `go build ./... && go test ./... && make lint`. Boot one role binary manually against a dev config and confirm it serves as before.

### Task 18: Rewrite the entrypoint's no-command path

**Files:** `cmd/rimsky-entrypoint/main.go`

**Steps:**
1. Keep: the single-role spawn path (explicit role command → spawn that role binary, unchanged), the migrate-ownership rules (no-arg always migrates; single-role migrates only for control-api; `RIMSKY_ENTRYPOINT_MIGRATE` override), and the unknown-command rejection.
2. Replace the no-command spawn-three path: run migrate synchronously (keep the existing exec of `rimsky-migrate` — it is a separate one-shot, that's fine), set `RIMSKY_PROCESS_ROLE=unified` in this process's own environment, then call `launch.RunScheduler`, `launch.RunSupervisor`, `launch.RunControlAPI` in-process; wait on SIGTERM/SIGINT; stop all three handles with the existing shutdown-deadline discipline. Delete the three-child spawn list, the per-child wait goroutines, and the child SIGTERM fan-out for the no-arg path (keep whatever the single-role path still needs).
3. Update the file's header comment (PID-1 process supervisor description) to describe the new shape.
4. **Verify ports don't collide:** the three roles bind distinct ports from config — boot the all-in-one config (`dockerfiles/all-in-one.rimsky.yml` et al.) locally via `go run ./cmd/rimsky-entrypoint` with the baked config paths and confirm all three surfaces respond, then SIGTERM and confirm clean exit.
5. `go build ./... && make lint`; rebuild images: `make core-images`.

### Task 19: Correct the memory-gate text + concept mutations

**Files:** `lib/foundation/persistence/blob_config.go`, `.ok-planner/design/concepts/blob-backend.md`, `.ok-planner/design/concepts/replica.md`

**Steps:**
1. Rewrite the `ValidateBlobConfig` rejection message (line ~116): the memory backend requires the single-process mode (all roles in one process sharing one in-process map, `RIMSKY_PROCESS_ROLE=unified` set by the entrypoint's no-command path); per-role processes cannot share it. Update the surrounding comments (lines ~85-100) likewise.
2. Apply the spec's `blob-backend.md` mutation (memory legal only in single-process mode; sweep reaps; cross-role reads work; rejected in any per-role process) and `replica.md` mutation (all-in-one = one process serving every role surface; per-role replicas are the split shape). Re-read both files for surviving contradictions.
3. `cd lib/foundation && go test ./persistence/... -run Blob -count=1`.

---

## Pass 7: Acceptance — STORY-single-process-all-in-one + topology coverage

**Goal:** Deliver STORY-single-process-all-in-one with its executable proof; add the split-topology integration test (TD-topology-test-coverage).
**Scope:** Tasks 20–21
**Falsifier:** (from the spec story) The all-in-one deployment still runs the roles as separate child processes; or the memory blob backend in all-in-one loses blobs across role boundaries; or single-role deployments change behavior.

### Task 20: Executable proof — single-process all-in-one

**Files:** `lib/services/test/scenarios/single_process_allinone_test.go` (new; follow the harness conventions in `lib/services/test/harness/rimsky.go` and neighboring scenario tests)

**Story:** STORY-single-process-all-in-one
**Proof form (from spec):** "proof (executable) — an integration test boots the all-in-one image, asserts a single rimsky process serves all three role surfaces, drives a node to terminal, and round-trips a spilled blob across roles under the memory backend."

**Steps:**
1. Rebuild images (`make core-images`). Boot `rimsky-all-in-one:latest` via the harness with a config override selecting the memory blob backend and a low spill threshold (mount a config file; the harness supports config injection — read `lib/services/test/harness/rimsky.go`'s config plumbing, extend if needed).
2. Assert single process: exec `ps -eo comm` (or read `/proc`) in the container and assert exactly one `rimsky-entrypoint` process and zero `rimsky-scheduler`/`rimsky-supervisor`/`rimsky-control-api` children.
3. Drive a node to terminal through the HTTP surface (reuse the harness's existing register/deploy/create flow) with an attribute value exceeding the spill threshold, then read it back through the control-api surface — the cross-role memory-blob round-trip. Assert the orphan-blob sweep ran without "missing blob" warnings in the container logs.
4. Run: `cd lib/services && go test ./test/scenarios/ -run SingleProcess -count=1`. Confirm green.

### Task 21: Executable proof — split three-container topology

**Files:** `lib/services/test/scenarios/split_topology_test.go` (new), `lib/services/test/harness/rimsky.go` (extend with a split-boot mode)

**Steps:**
1. Extend the harness: a boot mode launching a postgres container plus three containers from the `rimsky:latest` image with `command:` `[rimsky-scheduler]` / `[rimsky-supervisor]` / `[rimsky-control-api]`, shared config pointing at the postgres (the migrate-once rule means control-api migrates — boot it first or rely on the entrypoint rule).
2. Drive the same register/deploy/create/terminal scenario as Task 20 against the split stack; assert the node reaches terminal.
3. Run: `cd lib/services && go test ./test/scenarios/ -run SplitTopology -count=1`. Confirm green; `make lint`.

---

## Pass 8: Claimant-guard helper — one written guard per driver

**Goal:** TD-claimant-guard-helper — collapse the ~15 hand-written `holder_supervisor_id = $N` predicates per driver into one internal helper each.
**Scope:** Task 22
**Falsifier:** The guard predicate string still appears verbatim at multiple mutation sites in either driver, OR any previously-guarded mutation lost its guard in the refactor (a wrong-supervisor mutation now affects rows).

### Task 22: Route guarded mutations through a per-driver helper

**Files:** `lib/foundation/persistence/postgres/claim_handles.go`, `lib/foundation/persistence/postgres/claim_holders.go`, `lib/foundation/persistence/sqlite/claim_handles.go`, `lib/foundation/persistence/sqlite/claim_holders.go`

**Steps:**
1. In the postgres driver, add a small unexported helper (e.g. `claimantGuardedExec(ctx, tx, sql, id, supervisorID, extraArgs...)` or a clause-constant + exec wrapper — pick the shape that keeps each call site a one-liner and the predicate written once). The helper must preserve each statement's exact semantics including affected-row checking where present.
2. Convert every `holder_supervisor_id = $N` mutation site in the postgres driver (14 sites in `claim_handles.go`, 1 in `claim_holders.go`) to the helper. Preserve the `@blessed-invariant 4` file-header annotation; update its text to name the helper as the single written site.
3. Repeat for the sqlite driver (`?` placeholders).
4. **Load-bearing property:** no statement may lose its guard; do not "simplify" any statement to an unguarded form even where a caller seems to guarantee ownership.
5. `cd lib/foundation && go test ./persistence/... -count=1` (full driver suites, Docker for postgres) and `make lint`.

---

## Pass 9: Guard conformance suite — wrong-claimant is a provable no-op

**Goal:** TD-guard-conformance-suite — driver-parity coverage proving every ownership mutation is a no-op for the wrong supervisor, on both drivers.
**Scope:** Task 23
**Falsifier:** Any mutating claim-handle or node-run ownership operation lacks a wrong-claimant case in the suite, OR the suite runs against only one driver.

### Task 23: Add the guard suite to the driver-parity library

**Files:** `lib/foundation/persistence/conformance/claimant_guard.go` (new; follow the structure of `lib/foundation/persistence/conformance/verify.go` and `dispatch.go` — **unexported** `testX` funcs taking `*testing.T` + the database handle, registered as `t.Run` entries inside `conformance.go::Suite` (line ~43), which `conformance_test.go` invokes once per driver factory — there are no per-driver runner test files)

**Steps:**
1. Enumerate the guarded operations from Pass 8's helper call sites (claim-handle Delete, DeleteIfExpired, Promote, the guarded UPDATEs, claim-holder release) plus the node-run ownership mutations (`claimed_by` guards in `queue.go`/`queue_park.go`/`nodes.go` — `rg -n "claimed_by = " lib/foundation/persistence/postgres/`).
2. For each: seed a row owned by supervisor A; perform the operation as supervisor B; assert zero rows changed (state, holder, heartbeat all intact); perform as A; assert it succeeds. One unexported `testClaimantGuardX` func per operation family.
3. Register the new funcs as `t.Run` entries in `conformance.go::Suite` so both driver factories in `conformance_test.go` pick them up automatically.
4. Run the conformance package tests (`cd lib/foundation && go test ./persistence/conformance/... -count=1`); confirm green; `make lint`.

---

## Pass 10: Fold the ownership-bail into the unified engine; pin both carve-outs

**Goal:** TD-fold-ownership-bail + TD-acquire-unavailable-carveout — the verify-before-run bail resolves through the unified claim-handle resolution engine; the acquire-unavailable path is the single named carve-out; concept + tension design-doc updates ride along.
**Scope:** Tasks 24–27
**Falsifier:** `handleOrphanedClaim` still fires Abandon and deletes handle rows at its own site instead of calling the unified engine, OR the engine grew a no-rows mode to absorb acquire-unavailable, OR the fold changed observable behavior (verb counts, deletion guard) without a pinning test catching it.

### Task 24: Fold `handleOrphanedClaim` into the engine

**Files:** `lib/runtime/runner_acquire_postcommit.go`, the unified claim-handle resolution engine (locate: `rg -n "unified claim-handle resolution\|@blessed-invariant 4" lib/runtime/` — the single audited verb-then-delete site referenced by `concept:terminal-resolution` Stage 4)

**Steps:**
1. Read the engine's entry point and its existing source kinds (active-terminal, held-terminal). Add an ownership-bail source kind.
2. Rewrite `handleOrphanedClaim` (line 57) to call the engine with that source kind per claim, deleting its inline `Abandon` + `args.ClaimHandles.Delete` sequence (line ~68). The engine fires `Abandon` and performs the claimant-guarded delete — same observable behavior, one audited site.
3. **Load-bearing property:** the verb-then-delete ordering and the claimant guard are preserved exactly; the bail path emits no signal (admin path) — the engine's source kind must allow that.
4. Run the existing pins: `go test ./test/scenarios/ -run 'VerifyBeforeRun|Orphan' -count=1 -race`. Confirm green.

### Task 25: Post-fold injection pin for the bail path

**Files:** `test/scenarios/verify_before_run_post_commit_test.go` (extend) or a sibling new test

**Steps:**
1. Extend the existing `PostCommitHook` scenario to additionally assert the engine route: after the forced ownership flip, the bail resolves via the unified engine (assert the engine's audited behavior — exactly one Abandon per claim, handle rows deleted claimant-guarded, no signal emitted).
2. Run with `-race -count=3`; confirm green.

### Task 26: Carve-out injection test already exists — verify and annotate

**Files:** `lib/runtime/runner_lifecycle.go`

**Steps:**
1. Task 3 built the acquire-unavailable injection test. Add a comment block at `handleAcquireUnavailable` naming it the single carve-out from the unified engine and why (acquisition tx rolled back; no rows to delete), referencing `concept:terminal-resolution`.
2. `go build ./... && make lint`.

### Task 27: Design docs — `concept:terminal-resolution` mutation + tension resolution

**Files:** `.ok-planner/design/concepts/terminal-resolution.md`, `.ok-planner/design/tensions/reaper-vs-bail-abandon-asymmetry.md` → `.ok-planner/design/tensions/_resolved/`

**Steps:**
1. Apply the spec's `terminal-resolution.md` bullet in full: single-carve-out rewrite of the upstream-siblings paragraph; bail resolves through the engine with its own source kind; Stage-4 + invariants updated; AND the enumerated restatement rewrites — the kind→signal→verb table's verify-before-run row, the acquisition-failure row, and the intro/Stage-2/Stage-3 sentences naming only the synthetic class → "the producer-declared class else the synthetic acquisition class". Re-read the whole file; no surviving contradiction.
2. `git mv .ok-planner/design/tensions/reaper-vs-bail-abandon-asymmetry.md .ok-planner/design/tensions/_resolved/`; edit frontmatter `status: resolved`; append a `resolution:` block with the spec's resolution text (path-free).

---

## Pass 11: Scheduler sweep-lock error skips the pass

**Goal:** TD-sweep-lock-skip-on-error — a lock error is treated as lock-held; advisory-lock concept gains the invariant.
**Scope:** Task 28
**Falsifier:** A `TrySchedulerTick` error still falls through to running the sweeps unlocked.

### Task 28: Skip on lock error + concept invariant

**Files:** `lib/graph/scheduler/scheduler.go`, a scheduler unit test, `.ok-planner/design/concepts/advisory-lock.md`

**Steps:**
1. Edit `tick()` (lines ~253-256): on `TrySchedulerTick` error, log a warning ("skipping sweep pass") and `return nil` — same as the lock-held branch. Remove the "running unlocked" fall-through.
2. Add/extend a unit test with an advisory-locker stub whose `TrySchedulerTick` errors; assert no sweep ran (e.g. counting stub persistence calls).
3. Apply the spec's `advisory-lock.md` mutation part (1): add the lock-error-is-lock-held invariant. (Part (2), the SQLite file-lock rewrite, lands in Pass 12 with its code.)
4. `go test ./lib/graph/scheduler/... -race -count=3 && make lint`.

---

## Pass 12: SQLite multi-process safety

**Goal:** TD-sqlite-multiproc-safety — bare read-then-write sites become transactional; the SQLite advisory locker's tick + migration locks become file-lock-based; persistence-database + advisory-lock concepts updated; the sqlite-vs-memory tension resolves.
**Scope:** Tasks 29–32
**Falsifier:** Any read-then-write site the driver's pool comment enumerates still executes without a transaction, OR two processes sharing one SQLite file can both hold the scheduler-tick (or migration) lock simultaneously.

### Task 29: Transactional read-then-write sites

**Files:** `lib/foundation/persistence/sqlite/` (the sites enumerated by the `database.go` pool comment — start from `node_attributes` `MergeDelta` and `rg -n "tx == nil\|caller passes tx == nil" lib/foundation/persistence/sqlite/`)

**Steps:**
1. Read the pool comment (`database.go` ~118-127) and enumerate every impl relying on conn-level serialization for tx==nil read-then-write atomicity.
2. Convert each: when called with `tx == nil`, open an immediate-mode transaction internally (the DSN's `_txlock=immediate` makes `BEGIN` hold the writer slot) around the read-then-write; behavior with a caller-supplied tx unchanged.
3. Update the `sqliteMaxOpenConns` comment: conn-level serialization is no longer load-bearing for correctness (it may stay for throughput reasons — state which).
4. `cd lib/foundation && go test ./persistence/sqlite/... -race -count=3`.

### Task 30: File-lock-based scheduler-tick and migration locks

**Files:** `lib/foundation/persistence/sqlite/advisory_locker.go`

**Steps:**
1. Read the current locker (in-process `sync.Mutex` for tick + migration, lines ~16-23). Replace those two with file locks: a lock file derived from the database path (e.g. `<db-path>.tick.lock`, `<db-path>.migrate.lock`), `syscall.Flock` with `LOCK_EX|LOCK_NB` for try-acquire (tick) and blocking `LOCK_EX` for migration, matching the interface's existing contract (`TrySchedulerTick` returns held/release; migration lock blocks). Keep the per-name/per-scope in-tx no-ops unchanged (BEGIN IMMEDIATE covers them).
2. **Load-bearing property:** exclusion must hold across OS processes, not goroutines — the test must spawn or simulate two independent locker instances over the same path (two `sql.DB` handles in one test process with two locker instances is sufficient since flock is per-fd/per-process — use `flock` semantics carefully: prefer `LOCK_EX` on separately-opened fds, which contend correctly within one process too).
3. Write the test: two locker instances on one path; first `TrySchedulerTick` held, second not-held; release; second acquires.
4. `cd lib/foundation && go test ./persistence/sqlite/... -race -count=3 && make lint`.

### Task 31: Design docs — advisory-lock part 2 + persistence-database

**Files:** `.ok-planner/design/concepts/advisory-lock.md`, `.ok-planner/design/concepts/persistence-database.md`

**Steps:**
1. Apply the spec's `advisory-lock.md` mutation part (2): rewrite every sentence describing the SQLite tick/migration locks as in-process mutex / no-op (lines ~11, ~15, ~23) to the file-lock cross-process statement.
2. Apply the spec's `persistence-database.md` mutation: replace the dev-only / "NOT gate-rejected" invariant (line ~33) with the multi-process-safe / no-gate / deliberate-override statement from the spec.
3. Re-read both files; no surviving contradictions.

### Task 32: Resolve the sqlite-vs-memory tension

**Files:** `.ok-planner/design/tensions/sqlite-vs-memory-reject-asymmetry.md` → `_resolved/`

**Steps:**
1. `git mv` to `_resolved/`; set `status: resolved`; add the `resolution:` block with the spec's resolution text (no gate for SQLite + driver made safe + memory stays gated by physics; path-free).

---

## Pass 13: Driver-parity expansion

**Goal:** TD-parity-expansion — every queue, claim-handle, and frame behavior the runtime depends on has a parity test in the driver-parity suite.
**Scope:** Task 33
**Falsifier:** Runtime-consumed persistence behaviors exist with no parity coverage (enumerable by diffing the persistence interfaces against the suite's coverage).

### Task 33: Close the parity-coverage gaps

**Files:** `lib/foundation/persistence/conformance/` (new coverage files as needed)

**Steps:**
1. Enumerate the persistence interface methods the runtime calls: `rg -n "func.*persistence\." lib/foundation/persistence/*.go` for interface definitions; cross-reference which already have conformance funcs (`ls lib/foundation/persistence/conformance/`).
2. For each uncovered queue/claim-handle/frame behavior (park/resume transitions, retention sweep selection, frame lifecycle states, heartbeat extension, fan-out child-count bumps, message idempotency), add an unexported `testX` conformance func exercising it and assert identical observable behavior on both drivers. Prioritize behaviors with cross-driver drift risk (anything with driver-specific SQL idioms).
3. Register the new funcs in `conformance.go::Suite` (same wiring as Task 23); run the conformance package tests; `make lint`.

---

## Pass 14: Upstream gating at dispatch eligibility

**Goal:** TD-upstream-gating-at-eligibility — a stale run is not dispatch-eligible while any subscribed upstream has an in-flight run in the same frame, regardless of propagation path; wait-set/cascade concepts updated; the multi-sender test strengthened.
**Scope:** Tasks 34–36
**Falsifier:** A receiver whose upstream staleness arrived via the settlement walk still becomes dispatch-eligible while a subscribed upstream is in-flight in the frame, OR the wait-set's substitution role changed, OR self-edge / cycle scenarios broke.

### Task 34: The in-flight-upstream eligibility condition

**Files:** `lib/runtime/` (the supervisor's candidate path — `lib/runtime/runner.go` / `runner_acquire*.go`, wherever a selected candidate is checked before dispatch), `lib/foundation/persistence/node_runs.go` (the `Queue` interface holding `SelectCandidates`) + both drivers (one new query)

**Steps:**
1. Read the candidate-selection flow: persistence `SelectCandidates` lives in the queue surface (`lib/foundation/persistence/postgres/queue.go::SelectCandidates#172`, sqlite analogue) and returns dispatchable stale runs; the supervisor verifies before running. The new condition is enforced supervisor-side, post-select (the subscription edges live in the template, not the database — the predicate needs the template).
2. Add a persistence query `AnyInFlightRunForNodes(ctx, nodeIDs, frameID, runScopeID, tx) (bool, error)` to the queue interface beside `SelectCandidates`, implemented in both drivers (`EXISTS` over in-flight rows for the given node set in the frame/scope), plus a driver-parity conformance func for it (registered in `Suite`, same wiring as Task 23).
3. In the supervisor's pre-dispatch path (adjacent to the verify-before-run check so it shares the candidate context): resolve the candidate node's subscription senders from the template spec (the cascade walker already derives the subscription edge map — reuse that derivation; `rg -n "subscription edge map" lib/runtime/`), call the new query; if any sender is in-flight in the candidate's frame, skip the candidate this cycle (leave it pending — the sender's settlement will re-trigger selection). Self-edges: exclude the candidate's own node ID from the sender set (preserves the drain-my-own-queue idiom).
4. **Load-bearing property:** this is an eligibility condition, not a wait-set write — do not seed wait-set rows here; the wait-set's drained-rows substitution role is untouched.
5. Run the cascade/run-tree scenario suites: `go test ./test/scenarios/ -run 'Cascade|Subscription|SelfEdge|Frame' -count=1 -race`. All green.

### Task 35: Strengthen the multi-sender eligibility scenario

**Files:** `test/scenarios/subscription_cascade_test.go`

**Steps:**
1. Rework `TestSubscriptionCascade_EligibilityRespectsMultipleSenders` (line ~76) so its assertions pin what its comments claim: within one frame, with two senders in-flight, assert the receiver is NOT dispatch-eligible until both settle (assert via dispatch-candidate query or run-row state at a forced midpoint), then runs exactly once. Fix the comments to match the actual mechanics.
2. Run it with `-race -count=3`; green; `make lint`.

### Task 36: Design docs — wait-set + cascade mutations

**Files:** `.ok-planner/design/concepts/wait-set.md`, `.ok-planner/design/concepts/cascade.md`

**Steps:**
1. Apply the spec's `wait-set.md` bullet: add the propagation-path-independent invariant AND rewrite both "iff no undrained rows" sentences (what-it-is ~line 13, Invariants ~line 37) to the two-condition predicate.
2. Apply the spec's `cascade.md` bullet: add the eligibility statement; rewrite the pitfalls parenthetical (~line 44) to the two-condition predicate.
3. Re-read both files; no surviving contradictions.

---

## Pass 15: Acceptance — STORY-all-upstream-gating

**Goal:** Deliver STORY-all-upstream-gating with its executable proof.
**Scope:** Task 37
**Falsifier:** (from the spec story) A receiver observed dispatching while a subscribed upstream still has an in-flight run in the same frame; or a receiver that runs early and is never re-fired when stragglers settle, leaving the frame's result computed from a partial upstream set.

### Task 37: Deterministic diamond proof

**Files:** `test/scenarios/all_upstream_gating_test.go` (new)

**Story:** STORY-all-upstream-gating
**Proof form (from spec):** "proof (executable) — a deterministic scenario test builds the diamond with settlement-propagated staleness, holds one upstream open via an injection hook, and asserts the receiver is not dispatch-eligible until the held upstream resolves — then asserts single dispatch with the full upstream set in the substitution context."

**Steps:**
1. Build the diamond template: A → (B, C) → D, where B/C staleness arrives via A's *settlement* (not an invalidation walk). Use an injection hook (or a stub executor that blocks on a channel) to hold C running while B settles.
2. Assert: while C is in-flight, D is not dispatch-eligible (query the candidate surface or assert D has no run in `active` phase); release C; assert D dispatches exactly once and its substitution context contains both B's and C's contributions.
3. Run with `-race -count=3`; green.

---

## Pass 16: Hard-dep rendezvous — test-first (acceptance pass — STORY-multi-hard-dep-rendezvous)

**Goal:** TD-hard-dep-settled-guard — reproduce (or refute) the multi-hard-dep livelock, then guard `pullHardDepUpstreams`; the reproducing test is the story's proof.
**Scope:** Tasks 38–39
**Falsifier:** (from the spec story) Upstreams re-running each other after settling in the frame (mutual re-seeding), the frame never terminating, or the receiver dispatching more than once for one frame.

### Task 38: The reproducing scenario, written before any fix

**Files:** `test/scenarios/multi_hard_dep_test.go` (new; pattern-match `test/scenarios/per_run_attributes/hard_dep_test.go`, the single-hard-dep coverage)

**Story:** STORY-multi-hard-dep-rendezvous
**Proof form (from spec):** "proof (executable) — a deterministic reproducing scenario test for the two-hard-dep shape, written before any fix, then kept as the regression pin in either outcome."

**Steps:**
1. Build a node with two `hard_dep: true` upstream attribute sources whose upstreams settle independently in one frame. Assert: each upstream runs exactly once; the receiver runs exactly once, after both; the frame terminates within the deadline.
2. Run it. Record the outcome: livelock confirmed (test red — proceed to Task 39's fix) or refuted (test green — Task 39 step 3 records the refutation; the guard is still added only if the test demonstrates re-affirmation, otherwise skipped).

### Task 39: The settled-this-frame guard

**Files:** `lib/runtime/runner_terminal.go` (`pullHardDepUpstreams`, line ~939)

**Steps:**
1. If Task 38 confirmed the livelock: add the settled-this-frame guard — before re-affirming a hard-dep'd upstream, check whether it already settled in this frame (`HasRunForNodeInFrame` or the settled-state probe the file already uses) and skip re-affirmation if so. Re-run Task 38's test: green.
2. Run the single-hard-dep pin (`go test ./test/scenarios/per_run_attributes/ -run HardDep -count=1 -race`) and the Pass 14/15 suites again — the guard must not break single-hard-dep or gating behavior.
3. If Task 38 refuted the suspicion: make no runtime change; keep the test as the pin. (The decision-file note recording the refutation is written with the decision files in Pass 31.)

---

## Pass 17: Producer errors cross the HTTP boundary (acceptance pass — STORY-producer-error-passthrough)

**Goal:** TD-producer-error-passthrough — `writeError` carries producer error class + message with a distinguishing status; demo proof.
**Scope:** Tasks 40–41
**Falsifier:** (from the spec story) A producer failure that still surfaces as a bare `500 Internal Server Error` with an empty or generic body — the producer's transmitted error class discarded between the gRPC boundary and the HTTP response.

### Task 40: Typed producer errors through `writeError`

**Files:** `lib/control/controlapi/app.go` (`writeError`, line ~320), the producer-client error types (`lib/runtime/peer/client.go` / `lib/runtime/clientiface/` — find where producer gRPC errors are translated; `rg -n "ErrorInfo\|error_class" lib/runtime/peer/`)

**Steps:**
1. Read how producer errors reach the control-api today (the assets/instances handlers that call producer verbs). Define or reuse a typed error carrying `producer_name`, `error_class`, `message` at the translation layer.
2. Teach `writeError` to recognize it: respond `502 Bad Gateway` (producer failed) or `422` (producer rejected operator input) — inspect the existing error-envelope shape in `writeError` and follow it, adding `producer_name` and `error_class` fields to the body. Do not classify rimsky-internal errors differently than today.
3. Unit-test `writeError` with a synthetic producer error; assert status + body fields.
4. `go test ./lib/control/... -count=1 && make lint`.

### Task 41: Acceptance demo

**Files:** `examples/producer-error-demo.sh` (new; same demo location convention as Task 15)

**Story:** STORY-producer-error-passthrough
**Proof form (from spec):** "demo — against a running stack with a real store, trigger a producer rejection and show the API response carrying the producer's own error class and message."

**Steps:**
1. Script: boot all-in-one + the filesystem store image with a deliberately bad backing path (or a path made read-only); register/deploy a template using it; trigger the operation that makes the store reject; print the API response showing the store's error class and message.
2. Run end-to-end; confirm the response exhibits the class/message.

---

## Pass 18: Validation rejections name the mode (acceptance pass — STORY-validation-names-the-mode)

**Goal:** TD-validation-error-names-mode — ref-validation failures name the active mode and the config key.
**Scope:** Tasks 42–43
**Falsifier:** (from the spec story) A reference-validation rejection that still reads as a generic "validation rejected the registration" — mode unnamed, config key unnamed.

### Task 42: Self-documenting rejection text

**Files:** `lib/graph/node/template_validator.go` (the ref-validation error construction sites — the four reference legs around lines 739-1072), `lib/control/controlapi/templates.go` (line ~259 if the handler wraps the message)

**Steps:**
1. Locate where each reference-leg failure constructs its error. Extend the message to: name the failing reference, name the active mode (`all`/`available`/`none` — stringify `RefValidationMode`), state the mode made it fatal, and name `templates.ref_validation_mode` with the relaxed settings for register-first workflows. Build the message once in a helper so the four legs stay consistent.
2. Update any tests asserting the old message text.
3. `go test ./lib/graph/node/... ./lib/control/... -count=1 && make lint`.

### Task 43: Executable acceptance proof

**Files:** `test/scenarios/ref_validation_mode_message_test.go` (new, or extend the existing template-validation scenario file if one covers registration rejections — check `rg -ln "ref_validation" test/scenarios/`)

**Story:** STORY-validation-names-the-mode
**Proof form (from spec):** "proof (executable) — a test registers a template with an unprovisioned reference under strict mode and asserts the rejection body names the active mode and the config key; a companion assertion registers the same template under the relaxed mode and succeeds, proving the advice the error gives is true."

**Steps:**
1. Through the real HTTP registration surface (scenario harness): register a template referencing an unprovisioned executor under mode `all`; assert the rejection body contains the mode name and `templates.ref_validation_mode`. Re-register under mode `available`; assert success.
2. Run; green; `make lint`.

---

## Pass 19: Producer-declared error classes — proto, validator, fallback

**Goal:** TD-producer-declared-classes-capability + TD-validator-learns-producer-classes + TD-acquire-prefix-fallback; the error-policy concept mutation rides along.
**Scope:** Tasks 44–47
**Falsifier:** The producer capabilities proto still has no declared-classes field, OR `validateErrorTypes` still hard-rejects producer-declared keys, OR an `acquire/*` key never matches a producer-classified acquisition failure, OR unattributable keys are still hard rejections.

### Task 44: Proto extension + handshake storage

**Files:** `lib/protocols/proto/v1/claim_producer.proto` (`CapabilitiesResponse`, fields 1-5 today), the capabilities-handshake consumer (`rg -n "CapabilitiesResponse" lib/runtime/ lib/control/` — the discovery cache, `concept:discovery-cache`)

**Steps:**
1. Add `repeated string declared_error_classes = 6;` to `CapabilitiesResponse`, comment mirroring `executor_observability.proto`'s field (line ~71). Run `make proto-gen`.
2. Store the field in the discovery cache at handshake, alongside the executor vocabularies (read how executor `declared_error_classes` flows into the validator's `RegistryHooks` — `rg -n "declared_error_classes\|DeclaredErrorClasses" lib/`).
3. `go build ./... && go test ./... && make lint` across affected modules.

### Task 45: Validator accepts the union; unattributable keys become warnings

**Files:** `lib/graph/node/template_validator.go` (`validateErrorTypes`, line ~421; `RegistryHooks`)

**Steps:**
1. Extend `RegistryHooks` with a producer-classes lookup (per producer name → declared classes), wired from the discovery cache at the same place executor classes are wired.
2. Rework `validateErrorTypes`: a key is hard-valid if in the executor's declared classes, the `acquire/*` family, or any reachable producer's declared classes (producers reachable from the node's `claims:` block). A key attributable to none becomes a **warning** appended to `res.Warnings` (not an error) stating no declared vocabulary contains it.
3. Update validator unit tests: producer-declared key accepted; unknown key warns instead of rejecting; executor-key behavior unchanged.
4. `go test ./lib/graph/node/... -count=1 && make lint`.

### Task 46: `acquire/*` prefix fallback at policy lookup

**Files:** `lib/runtime/on_error.go` (`lookupPolicy` — exact-key match today)

**Steps:**
1. For acquisition-failure classes (the error path entered via `handleAcquireUnavailable`): if the exact producer-declared class has no `error_types:` entry, fall back to `acquire/unavailable` (the synthetic family) before the unknown-class give-up default. Document the fallback order in a comment at the lookup site.
2. Unit-test: template declares only `acquire/unavailable: retry`; producer-classified failure (`pg/claim_unavailable`) routes to retry.
3. `go test ./lib/runtime/... -count=1 && make lint`.

### Task 47: Design doc — `concept:error-policy` three mutations

**Files:** `.ok-planner/design/concepts/error-policy.md`

**Steps:**
1. Apply the spec's three mutations: (1) retry-cap is a supervisor default (line ~12); (2) acquisition-failure lookup falls back exact-producer-class → `acquire/*` family → unknown-class give-up (invariant ~line 48); (3) `error_types:` keys validated against the executor ∪ producer ∪ `acquire/*` union, unattributable keys register as advisory warnings.
2. Re-read the file; no surviving contradictions.

---

## Pass 20: Acceptance — STORY-producer-class-routing

**Goal:** Deliver STORY-producer-class-routing with its executable proof.
**Scope:** Task 48
**Falsifier:** (from the spec story) Registration rejecting a producer-declared class that the runtime would route; or an `acquire/*` key that registers but never matches a producer-classified acquisition failure.

### Task 48: Executable proof through the real surfaces

**Files:** `test/scenarios/producer_class_routing_test.go` (new; reuse the acquisition-failure scenario machinery in `test/scenarios/acquire_unavailable_error_routing_test.go`, `acquire_unavailable_error_types_test.go`, and `acquire_unavailable_retry_default_test.go`)

**Story:** STORY-producer-class-routing
**Proof form (from spec):** "proof (executable) — a scenario registers both template shapes and drives a producer-classified acquisition failure through each, asserting the configured action fires."

**Steps:**
1. With a producer that declares `pg/claim_unavailable` in its capabilities (extend the scenario's stub producer to declare it) — register template A with `error_types: { pg/claim_unavailable: retry }` (must succeed) and template B with only `acquire/unavailable: retry`.
2. Drive a producer-classified acquisition failure through each; assert the retry action fires in both (template A via exact match, B via prefix fallback) — observe via run-row retry state or the emitted `transient/retry/...` signal in the event log.
3. Run; green; `make lint`.

---

## Pass 21: Validator warnings surfaced (acceptance pass — STORY-validation-warnings-surfaced)

**Goal:** TD-merge-validator-warnings — static-validator warnings reach both responses; `warnings_as_errors` trips on them.
**Scope:** Tasks 49–50
**Falsifier:** (from the spec story) A static-validator warning that is computed but absent from both responses; or `warnings_as_errors=true` not tripping on it.

### Task 49: Merge `res.Warnings` into both handlers

**Files:** `lib/control/controlapi/templates.go` (register handler ~201-213; validate endpoint ~385-442)

**Steps:**
1. In both handlers, merge the static validator's `res.Warnings` into the `validation_warnings` response array alongside the pipeline's `outcome.Warnings`, and include them in the `warnings_as_errors=true` rejection set. Match the existing warning-entry shape.
2. Update handler tests for both endpoints.
3. `go test ./lib/control/... -count=1 && make lint`.

### Task 50: Executable acceptance proof

**Files:** `test/scenarios/validation_warnings_test.go` (new)

**Story:** STORY-validation-warnings-surfaced
**Proof form (from spec):** "proof (executable) — register a template that trips the acquisition-policy advisory and assert it appears in `validation_warnings`; repeat with `warnings_as_errors=true` and assert rejection."

**Steps:**
1. Through the real registration surface: a template acquiring claims with no acquisition-failure policy (trips `validateAcquireUnavailablePolicyAdvised`, `template_validator.go` ~548). Assert the advisory in `validation_warnings`; repeat with `?warnings_as_errors=true`; assert rejection.
2. Run; green.

---

## Pass 22: Validation mix-in uniform across peer kinds (acceptance pass — STORY-validation-mixin-uniform)

**Goal:** TD-plumb-validation-roles — executor-observability proto gains `validation_supported_roles`; the all-peer-kind dial plumbs executor + publisher roles.
**Scope:** Tasks 51–52
**Falsifier:** (from the spec story) An executor or publisher advertising the mix-in whose supported-roles list is still treated as empty — dialed but never used.

### Task 51: Proto field + plumbing

**Files:** `lib/protocols/proto/v1/executor_observability.proto` (`ObservabilityCapabilities`), `lib/control/config/publishers.go` (`DialPublisherAndValidationRegistries`, ~line 91; the nil `fetchRoles` for executors/publishers at ~148-155, 207-213)

**Steps:**
1. Add `repeated string validation_supported_roles` to `ObservabilityCapabilities` with the next free field number, comment mirroring `publisher.proto`'s field (line ~47-49). `make proto-gen`.
2. In `DialPublisherAndValidationRegistries`: replace the empty-roles treatment for executor peers (fetch via the observability handshake's capabilities) and publisher peers (the field already exists on `PublisherCapabilitiesResponse` — fetch it), so all three kinds resolve live roles identically. Delete the "do not yet plumb" comments.
3. `go build ./... && go test ./lib/control/... -count=1 && make lint`.

### Task 52: Executable acceptance proof

**Files:** `lib/foundation/` or `test/scenarios/` conformance-style test (new; place beside the existing validation-registry tests — `rg -ln "ValidationRegistr" lib/ test/ -g '*_test.go'`)

**Story:** STORY-validation-mixin-uniform
**Proof form (from spec):** "proof (executable) — a conformance-style test registers each peer kind advertising the mix-in and asserts the handshake-learned roles are identical across kinds."

**Steps:**
1. Stand up three stub peers (claim-producer, executor, publisher) each advertising the validation mix-in with the same roles; run the registry dial; assert all three peers' learned role sets are identical and non-empty.
2. Run; green.

---

## Pass 23: Peer TLS enforcement (acceptance pass — STORY-peer-tls-enforced)

**Goal:** TD-peer-tls-enforcement + TD-tls-mode-validation — the `tls` key exists for executor/store/publisher entries, validates to `off | required`, and every dial site honors it.
**Scope:** Tasks 53–55
**Falsifier:** (from the spec story) A `tls: required` peer connection observed on the wire in plaintext; or the key accepted and silently ignored.

### Task 53: Config — key on all peer entries, validated enum

**Files:** `lib/control/config/stores.go` (ExecutorEntry.TLS ~136, yaml parsing ~319; StoreEntry ~101-112; PublisherEntry ~166-179)

**Steps:**
1. Add `TLS string` to `StoreEntry` and `PublisherEntry` + their yaml structs, mirroring the executor field.
2. Add parse-time validation for all three: accepted values exactly `""` (→ `off`), `off`, `required`; anything else (including `optional`) is a config error naming the entry and the accepted values. Update the field comments (delete the `optional` mention).
3. Config unit tests: valid values pass; `optional` rejected with the naming error.
4. `go test ./lib/control/config/... -count=1`.

### Task 54: Dial sites honor the mode

**Files:** `lib/runtime/peer/dial.go` (~40, ~95), `lib/runtime/peer/publisher_client.go` (~104), `lib/runtime/peer/data_processing_client.go` (~143), `lib/runtime/peer/validation_client.go` (~148), `lib/runtime/executor/client.go` (`NewGRPCClient`, ~45-50), `lib/control/observability/handshake.go` (~133-143)

**Steps:**
1. Add a shared credentials helper (in `lib/runtime/peer/` — e.g. `transportCredentials(mode string)` returning `insecure.NewCredentials()` for off and `credentials.NewTLS(&tls.Config{})` (system roots) for required).
2. Thread the per-peer mode from config through to each dial site listed above (follow how the address threads today) and use the helper. Failures under `required` must surface with the peer name and mode in the error.
3. **Load-bearing property:** every enumerated dial site is covered — the executor execute channel and the observability handshake included; a site left insecure under `required` trips the story falsifier.
4. `go build ./... && go test ./... && make lint` (root module).

### Task 55: Executable acceptance proof

**Files:** `test/scenarios/peer_tls_test.go` (new)

**Story:** STORY-peer-tls-enforced
**Proof form (from spec):** "proof (executable) — integration test dials a TLS-enabled stub peer under `required` and exchanges a request; companion test dials a plaintext stub under `required` and asserts the loud failure."

**Steps:**
1. Stand up a stub gRPC peer with a self-signed-for-localhost cert added to the test's cert pool (inject the pool via the credentials helper if needed for testability — keep the production default at system roots); dial with `tls: required`; exchange one RPC. Companion: plaintext stub + `required` → assert the dial/RPC error names the peer and mode.
2. Run; green; `make lint`.

---

## Pass 24: `work_completed` emission (acceptance pass — STORY-work-completed-emitted)

**Goal:** TD-emit-work-completed — the terminal-application step appends the declared-but-never-emitted kind.
**Scope:** Tasks 56–57
**Falsifier:** (from the spec story) Runs that reach terminal with no `work_completed` in the ledger — the kind still declared but never spoken.

### Task 56: Emit at terminal application

**Files:** `lib/runtime/runner_terminal.go` (the terminal-application step — where the per-kind handlers dispatch; mirror the `work_started` append at `lib/runtime/runner_acquire.go` ~365-376)

**Steps:**
1. Read the `work_started` append (`Events().Append` with `events.Kind...`) and the kinds map (`lib/foundation/events/kinds.go` ~195-196). At the terminal-application site, append `work_completed` with the same identifying payload fields as `work_started` plus the terminal kind. Cover every terminal kind that ends the dispatch (Complete / Errored / Infra; parked and await-async do not end work — they re-enter; emit on their eventual terminal only — follow `concept:terminal-resolution`'s table).
2. `go build ./... && go test ./lib/runtime/... -count=1`.

### Task 57: Executable acceptance proof

**Files:** `test/scenarios/work_completed_test.go` (new, or extend an existing event-log scenario — `rg -ln "work_started" test/scenarios/`)

**Story:** STORY-work-completed-emitted
**Proof form (from spec):** "proof (executable) — a scenario drives a run to terminal and asserts the paired events with matching identifiers."

**Steps:**
1. Drive a node to terminal through the real stack; read the event log; assert exactly one `work_started` and one `work_completed` for the run, with matching node/run identifiers and the terminal kind on the completion.
2. Run; green; `make lint`.

---

## Pass 25: Named-lock metric (acceptance pass — STORY-named-lock-metric)

**Goal:** TD-named-lock-metric — named-lock acquisitions increment a labeled acquisition counter.
**Scope:** Tasks 58–59
**Falsifier:** (from the spec story) Named-lock acquisitions that move no metric — the events ledger still the only trace.

### Task 58: Increment the counter

**Files:** `lib/runtime/runner_acquire_named_locks.go`, the runtime metrics interface (`lib/runtime/runner.go` — the claim-acquisition counter method and its Prometheus implementation; `rg -n "ClaimAcquisition" lib/`)

**Steps:**
1. Read the metrics interface and the existing producer-claim increment site (`runner_acquire_claims.go`). Follow the existing convention: either label the existing acquisition counter with a kind (`named_lock` vs `producer_claim`) if it carries labels, or add a sibling counter named per the existing family's naming. Increment in the named-lock acquisition path.
2. Update the noop + Prometheus metric implementations and any metrics test.
3. `go test ./lib/runtime/... -count=1 && make lint`.

### Task 59: Executable acceptance proof

**Files:** `test/scenarios/named_lock_metric_test.go` (new, or extend `test/scenarios/locks/` — the named-lock scenarios live there)

**Story:** STORY-named-lock-metric
**Proof form (from spec):** "proof (executable) — a test acquires named locks and asserts the counter's movement and labeling."

**Steps:**
1. Drive a node acquiring a named lock through the real stack; scrape the metrics endpoint (or read the registry in-process per the harness's metrics access pattern); assert the counter moved with the named-lock label.
2. Run; green.

---

## Pass 26: Child execution — the dispatch primitive

**Goal:** TD-child-execution-unification (dispatch half) + TD-entry-absorption-flag + TD-subclaims-as-input + TD-child-execution-naming — `DispatchChildren` replaces both dispatch sites.
**Scope:** Tasks 60–61
**Falsifier:** Both old dispatch sites still contain their own RunScope-allocation + child-row logic instead of delegating to the primitive, OR the primitive calls the producer's split itself, OR template-observable behavior changed (existing fanout/subgraph scenarios red).

### Task 60: `DispatchChildren`

**Files:** `lib/runtime/child_execution.go` (new), `lib/runtime/subgraph_dispatch.go`, `lib/runtime/fanout_dispatch.go`

**Steps:**
1. Read `applyTerminalCompleteSubgraphCaller` (`subgraph_dispatch.go:440`) and `CreateFanOutChildren` (`fanout_dispatch.go:258`) end-to-end. Extract the shared run-side work into `DispatchChildren(ctx, args, tx, in ChildExecutionInput)`: per partition allocate the child RunScope (`partition_key`, `parent_run_id`, `parent_run_scope_id`, `graph_name`), allocate the child's leaf-run row, wire the sub-claim handle if present. Input carries `Partitions []PartitionDescriptor` (partition key, optional sub-claim handle ID, inert payload), `AggregationPolicy`, `ChildGraphName`, `EntryAbsorbed bool`. The primitive accepts already-acquired sub-claims; it never calls the producer.
2. Convert both call sites into thin wrappers building the input (delegation: one partition, empty key, carry-verbatim, entry absorbed; fan-out: N partitions from `AcquireSubClaims` results, author policy). Delete the now-shared logic from both files.
3. **Load-bearing property:** all allocation stays inside the caller's transaction exactly as today — do not move any write out of the tx.
4. Run the suites: `go test ./test/scenarios/fanout/... ./test/scenarios/subgraph/... -count=1 -race`. Green. `make lint`.

---

### Task 61: Behavior-pin check before settlement work

**Files:** none (verification only)

**Steps:**
1. Run the full root-module suite plus race slice: `go test ./... && go test -race -count=1 ./lib/runtime/...`. Green tree before Pass 27 starts.

---

## Pass 27: Child execution — the settlement primitive

**Goal:** TD-child-execution-unification (settlement half) + TD-carry-verbatim-requires-one + TD-cascade-inside-settlement — `SettleChildren` replaces both settlement paths; carry-verbatim is a policy row requiring N=1 at canonicalization; the cascade bridge fires inside settlement.
**Scope:** Tasks 62–64
**Falsifier:** `CarryExitWriteback` and `resolveParentClaimChain` still exist as parallel settlement implementations, OR the carry-writeback is no longer atomic with closing the child execution context, OR a settlement caller can skip the parent-settlement cascade, OR a delegation declaring multiple children passes canonicalization.

### Task 62: `SettleChildren`

**Files:** `lib/runtime/child_execution.go`, `lib/runtime/subgraph_dispatch.go` (`CarryExitWriteback`, line 172), `lib/runtime/auto_terminal_chain.go` (`resolveParentClaimChain`, line 54)

**Steps:**
1. Read both settlement paths end-to-end, including the parent-settlement cascade bridge (the `cascadeSubscribersStaleInTx` call for the parent) and the RunScope closure writes. Implement `SettleChildren` firing on every child terminal: record outcome; apply policy (`carry_verbatim` = the single child's outcome copied verbatim to the parent writeback; `strict|threshold|best_effort|first` per existing semantics); when the policy settles the parent: close the child RunScope(s), write the parent settlement, and fire the cascade bridge — inside the primitive, same transaction discipline as today.
2. **Load-bearing property (from the spec's carry-rule atomicity constraint):** the carry-writeback and the child-context closure commit in one transaction — the boundary must be neither widened nor narrowed. Map today's tx boundary first; preserve it exactly.
3. Re-route both old paths through `SettleChildren`; delete `CarryExitWriteback` and `resolveParentClaimChain` (and any now-empty shells). Keep the `@blessed-invariant: exit-node-writeback` annotation, moved to the primitive's carry site.
4. Run: `go test ./test/scenarios/fanout/... ./test/scenarios/subgraph/... -count=1 -race`, then the full `go test ./...`. Green.

### Task 63: Carry-verbatim requires N=1 at canonicalization

**Files:** the template canonicalizer (`rg -n "canonicaliz" lib/graph/node/` — where `delegate:`/`fan_out:` translate to internal declarations)

**Steps:**
1. At canonicalization, when the aggregation policy is carry-verbatim, enforce exactly one child; violation is a template-validation error naming the node.
2. Unit-test the rejection.
3. `go test ./lib/graph/... -count=1 && make lint`.

### Task 64: Re-target carry-rule tests

**Files:** the scenario/unit tests that assert against `CarryExitWriteback` directly (`rg -ln "CarryExitWriteback" test/ lib/`)

**Steps:**
1. Re-point them at `SettleChildren` + the carry-verbatim policy, preserving each assertion's strength.
2. Full suite: `go test ./... -count=1` and `make test-race`. Green tree at pass end.

---

## Pass 28: Child execution — design docs

**Goal:** The spec's child-execution design changes: create `concept:child-execution`; demote `concept:delegation` and `concept:fan-out`; resolve the tension.
**Scope:** Tasks 65–66
**Falsifier:** `concepts/child-execution.md` absent, OR delegation/fan-out still restate the shared invariants, OR the tension file still sits in `tensions/` as open.

### Task 65: Concept create + demotions

**Files:** `.ok-planner/design/concepts/child-execution.md` (new), `.ok-planner/design/concepts/delegation.md`, `.ok-planner/design/concepts/fan-out.md`

**Steps:**
1. Create `child-execution.md` per the spec's bullet verbatim — Definition / Purpose / Boundaries (dispatch + settlement primitives; run-scope owns the contexts and tree; template surfaces to delegation/fan-out; sub-claim acquisition to claim-tree) / Invariants (settlement the only run-side closure path with the instance-termination exception per `concept:run-scope`; carry-verbatim N=1 at canonicalization; entry absorption is the invoking pattern's property; the cascade unskippable; carry atomic with closure, `@blessed-invariant: exit-node-writeback`). Frontmatter: `concept: child-execution`, `status: as-is`, `aliases: []`. Self-contained — no file paths.
2. Rewrite `delegation.md` and `fan-out.md` per the spec's bullets: invocation patterns over `concept:child-execution`; boundaries shrink to template surface + their genuine asymmetries; shared shape referenced, not restated. Re-read both for surviving restatements of the moved invariants (delegation's carry-atomicity line moves to child-execution — reference it, don't restate).
3. Check `rg -n "@concept: (delegation|fan-out)" lib/` — if annotated code sites now express child-execution invariants, update annotations to `@concept: child-execution` where the site is load-bearing for the shared primitive (the new `child_execution.go` gets the annotation).

### Task 66: Resolve the delegation/fan-out tension

**Files:** `.ok-planner/design/tensions/delegation-and-fanout-share-runtime-primitive.md` → `_resolved/`

**Steps:**
1. `git mv` to `_resolved/`; `status: resolved`; `resolution:` block with the spec's resolution text (path-free).

---

## Pass 29: Base-Commit response honored (acceptance pass — STORY-commit-response-honored)

**Goal:** TD-wire-commit-response-fields — the producer client returns the Commit response; the engine persists `version_id`; `SettleChildren` surfaces `producer_metadata` in the fan-out parent writeback. (Runs after the child-execution passes so the metadata lands in the unified settlement path once.)
**Scope:** Tasks 67–68
**Falsifier:** (from the spec story) Base-protocol Commit response fields set by the producer and absent from the row / writeback — the response body still discarded.

### Task 67: Wire the response fields

**Files:** `lib/runtime/peer/client.go` (`Commit`, ~95) and its `clientiface` signature, the unified claim-handle resolution engine (the Commit-verb fire site), `lib/runtime/child_execution.go` (`SettleChildren` — the fan-out parent writeback)

**Steps:**
1. Change the client `Commit` to return the response body; update the interface the engine consumes (note: `lib/runtime/clientiface/` has no claim-producer file — find the Commit interface via `rg -n "Commit(" lib/runtime/ -g '!*_test.go'`) and all callers.
2. At the engine's Commit site: when the response carries `version_id`, persist via the existing `SetVersionID` accessor (claim-handle row) — mirror the data-processing path's usage (`terminal_decision.go` ~293-298). When children's commits carry `producer_metadata`, thread it to `SettleChildren` so the fan-out parent's writeback surfaces it (extend the writeback payload shape; follow how partition outcomes are recorded today).
3. `go build ./... && go test ./... -count=1 && make lint`.

### Task 68: Executable acceptance proof

**Files:** `test/scenarios/commit_response_fields_test.go` (new; reuse a stub producer from existing scenarios)

**Story:** STORY-commit-response-honored
**Proof form (from spec):** "proof (executable) — a scenario with a stub producer that stamps both fields on the base Commit response asserts the persisted version and the writeback-surfaced metadata."

**Steps:**
1. Stub producer stamps `version_id` + `producer_metadata` on Commit. Drive (a) a plain node to terminal — assert the claim-handle row's `version_id` (the row persists per its lifetime/retention; query before sweep); (b) a fan-out — assert the parent writeback carries the children's metadata.
2. Run; green.

---

## Pass 30: Comment-drift sweep + archive deletion + frontmatter fix

**Goal:** TD-comment-drift-sweep + TD-delete-archived-author-guide + the `claim-co-holdership.md` aliases fix.
**Scope:** Tasks 69–71
**Falsifier:** Any of the ten enumerated comments still states the falsehood, OR the archived guide still exists, OR `claim-co-holdership.md` still lacks the `aliases:` key.

### Task 69: The ten comment fixes

**Files:** `lib/control/controlapi/mcp_route.go` (~5), `lib/protocols/publisherkit/publisher.go` (~7, 13, and `Send` ~96-98), `lib/runtime/publishers.go` (retry comments — verify Task 10 already fixed them; fix any stragglers), `lib/services/executors/claude-agent/../http-node/server.go` — correct path `lib/services/executors/http-node/server.go` (`parseRetryAfter` doc-comment), `lib/runtime/runner_terminal.go` (~789-795 wait-set comment), `lib/control/config/stores.go` (internal-plan vocabulary in comments/error text), `feature-index.md` (stale rows ~80, ~137)

**Steps:**
1. Fix each comment to state what the code does (verify against the adjacent code while editing). For `stores.go`, rewrite error text/comments to plain language without plan-anchor vocabulary.
2. For the TypeScript-adjacent `http-node` server (it is Go — confirm; if TS, run that module's checks per its package.json): run the module's own test command.
3. `go build ./... && make lint` across touched modules.

### Task 70: Delete the archived author guide

**Files:** `.ok-planner/archive/internal/claim-producer-author-guide.md`

**Steps:**
1. `git rm .ok-planner/archive/internal/claim-producer-author-guide.md` (tracked) or `rm` if untracked.

### Task 71: `claim-co-holdership.md` frontmatter

**Files:** `.ok-planner/design/concepts/claim-co-holdership.md`

**Steps:**
1. Add `aliases: []` to the frontmatter, matching sibling files' key order.

---

## Pass 31: Design corpus — story files, decision files, TOC refresh

**Goal:** The spec's remaining design changes: 13 story files, 39 decision files, `concepts.md` TOC refresh for the new/changed concepts.
**Scope:** Tasks 72–74
**Falsifier:** Any spec story lacks its `design/stories/<slug>.md`, OR any TD lacks its `design/decisions/<slug>.md`, OR `concepts.md` lacks the `child-execution` entry.

### Task 72: 13 story files

**Files:** `.ok-planner/design/stories/` — the 13 filenames enumerated in the spec's Design changes

**Steps:**
1. Read one existing file in `design/stories/` for the template shape (frontmatter `story:`, `status:`, then role/capability/value + Acceptance / Falsifier / Proof sections).
2. Create each of the 13 from the spec's User outcomes verbatim, rewritten path-free per self-containment (strip any file path; keep artifact slugs). For STORY-multi-hard-dep-rendezvous, write the story as the contract (rendezvous), not the suspicion.

### Task 73: 39 decision files

**Files:** `.ok-planner/design/decisions/` — the 39 filenames enumerated in the spec's Design changes

**Steps:**
1. Read one existing file in `design/decisions/` for the template shape (frontmatter `decision:`, `status:`, then Choice / Rationale / Alternatives).
2. Create each of the 39 from the spec's TDs, path-free (the TDs cite file paths for grounding — the durable decision states the choice in prose without paths; name code-adjacent things by concept slug or plain description). If Task 38 refuted the hard-dep livelock, `decisions/hard-dep-settled-guard.md` records the refutation outcome as its current-state Choice.

### Task 74: Refresh `concepts.md`

**Files:** `.ok-planner/design/concepts.md`

**Steps:**
1. Regenerate/update the TOC: add `child-execution` with a one-sentence definition; update the one-liners for any mutated concept whose first-sentence definition changed (delegation, fan-out at minimum — both are now invocation patterns). Keep the file's existing format exactly.
2. Regenerate the sibling auto-generated catalogs the same way: `.ok-planner/design/stories.md` (add the 13 new story one-liners from Task 72) and `.ok-planner/design/decisions.md` (add the 39 new decision one-liners from Task 73) — both files state they are generated when a plan touches their directories; keep each file's existing format exactly.
3. Final repo-wide checks: `make build-all && make test-all && make lint` — the whole-tree green gate.

---

## Manual checks after completion

- Watch the all-in-one container's memory footprint under the single-process mode in a longer-running deployment (one process now hosts three roles; no automated check captures slow-leak behavior).
- Optional: visually confirm the demo scripts' output reads well to a third party (`examples/subscription-mounting-demo.sh`, `examples/producer-error-demo.sh`).
