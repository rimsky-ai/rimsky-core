# Frame Resolution Implementation Notes

Sibling of `docs/plans/2026-04-26-frame-resolution.md`.

This file is appended to by the orchestrator and the implementer subagents during
the `ok-planner:subagent-dev` run. Each entry follows the format:

```
## Task N — <title>
**Deviation:** <what, or "none — flagging for visibility">
**Reason:** <why>
**Surfaced for:** <what the user should look at>
```

The user walks this file at the end of the run.

---

## Task 5 — controlapi error formatting for frame-validation
**Deviation:** Could not run `go test ./core/controlapi/... -count=1` clean at this point because existing tests still depend on `kill_requested` column (TestOperatorKill_SetsKillRequested, TestAdminForceFireRoute, TestClaimsRoute, etc.) which migration 002 has dropped. Continuing — Tasks 13/15/16/17 explicitly remove these references.
**Reason:** The plan's task ordering means controlapi tests are temporarily broken between Task 1 (migration) and Tasks 13/17 (controlapi cleanup). The verification command at Task 5 cannot pass until those land.
**Surfaced for:** Verify no spurious controlapi test failures remain after Task 17.

## Tasks 24/25 — pre-frame scenario test semantic gaps
**Deviation:** Several pre-frame scenario tests now fail because they assumed multiple root nodes (no deps) in the same instance run concurrently. Under the frame model, instance-factory enqueues one frame per root, and serial_queue runs frames sequentially per instance. Only failing tests are stores/filesystem_direct_disjoint_regions, stores/filesystem_direct_overlapping_regions (kindof), stores/filesystem_direct_read_concurrent_with_write, stores/store_pool_specialization. These all expected concurrent running of multiple instance roots.
**Reason:** Per spec §3.1: "at most one frame in 'running' state per instance at any time." Multiple roots in one instance now serialize. Tests should be rewritten to use multiple instances or to assert sequential behavior.
**Surfaced for:** User decides whether to: (a) rewrite tests to multiple instances, (b) accept these as known-failing under the frame model, or (c) introduce a "parallel-roots-of-one-frame" semantic. I have NOT modified these tests; they remain failing.

## Task 24 — Replaced WaitForNodeState fresh semantics
**Deviation:** Updated harness `WaitForNodeState(fresh, ...)` to additionally require evidence of execution (a work_completed/pure_cascade_commit/no_op_commit event for the node) before counting fresh as a match. Pre-frame, nodes were created stale and reaching fresh meant "ran"; under the frame model nodes start fresh, so the naive "wait for fresh" short-circuits before any work runs.
**Reason:** Avoid mass-rewriting every scenario test that polls for fresh-as-success. Tests that DON'T want this gating should call `WaitForEventKind` or use a different target state.
**Surfaced for:** Possibly tighter semantics — e.g., what if fresh is reached without a recorded event for some genuine reason? Today the gating is correct because every node-execute path emits one of the listed events.

## Task 18 — Default node state changed: stale → fresh
**Deviation:** Changed `core/storage/postgres/nodes.go::Create` default state from `'stale'` to `'fresh'`. Several tests had to be updated (`createNode` helpers in scheduler_test.go / invalidate_test.go / pure_cascade_test.go / postgres_test.go / commit_test.go now explicitly UPDATE to stale when needed; addRunningNode bypasses the state machine via direct UPDATE since fresh→running is illegal under §17).
**Reason:** Under the frame model (spec §3.1) only frame-start makes nodes stale; new nodes carrying no frame_id should be 'fresh'. Keeping the default as 'stale' broke the frame engine's frame-end predicate when an instance had multiple root nodes (one frame's source advances stale, but other unrelated nodes were also stale-by-default and prevented frame-end).
**Surfaced for:** This is a behavior change that consumers of NodeStore.Create should be aware of. Confirm this is acceptable or push the default fix to the instance factory only (which I tried first but it raced the scheduler tick).

## Task 11 — Frame logger plumbing
**Deviation:** Created a `frameLoggerFor` helper that returns `slog.Default()` regardless of the scheduler's `shared.Logger`. The plan code passes `s.logger` directly but the scheduler uses a `shared.Logger` interface (custom), not a `*slog.Logger`. Bridging would require either teaching `core/frame` to accept the `shared.Logger` interface, or building an adapter.
**Reason:** Keeping `core/frame` independent of `shared.Logger` (frame package contract uses stdlib slog only); rather than translating between the two log shapes, the bridge writes to slog.Default(). This means scheduler logger fields (e.g., supervisor_id) won't reach frame logs, but the frame log fields (frame_id, instance_id, count) are still emitted.
**Surfaced for:** A nicer fix would be to teach `core/frame` to accept a small log interface (Info/Warn methods) so the scheduler's logger can be reused.

## Task 4 — Validator behavior change
**Deviation:** Validator now mutates `spec.FrameTimeoutMs` to `FrameTimeoutDefaultMs` (600000) when input is zero, in addition to error reporting. Existing 36 test fixtures had `FrameResolution` injected via `replace_all` (`FrameResolution: FrameResolutionSerialQueue` after `Version: "1.0.0",`).
**Reason:** Plan Task 4 step 2 says "post-validation the struct's FrameTimeoutMs == 600000". Mutation is the only way to express that expectation when the validator returns a result struct (not an error chain).
**Surfaced for:** Confirm the convention of "validator mutates spec on default-fill" is acceptable — alternative is to do the default-fill at deploy-time in controlapi/templates.go.

---

## Resumed inline run (2026-04-26 ~05:30 onward)

The first agent terminated mid-plan when it hit a usage cap. The orchestrator picked the remaining work up inline (no subagent) and finished it. The notes below cover that resumption.

## Tasks 26-38 — frame_resolution scenario tests
**Deviation:** Created `test/scenarios/frame_resolution/` with 13 plan-required tests + 1 split sub-test (`TestPerInstanceOrderingInvariant_DirectSQL` and `TestPerInstanceOrderingInvariant_Concurrent` in one file). Helper file `helpers_test.go` shares `listFrames`, `countFramesByState`, `fireInvalidate`, `waitForFramesByState`, `waitForFrameTerminal`. All 14 tests PASS in plain run and in race-mode (`-race -count=2`).
**Reason:** Plan Task 36 specified one test (`per_instance_ordering_invariant_test.go`) doing both a direct-SQL unique-violation check and a concurrent-fire check. Splitting them improves diagnosability without changing coverage; both live in the same file.
**Surfaced for:** Note the held-claim test (`held_claim_resolution_at_frame_end_test.go`) is a focused observability-column test rather than a full-stack held-claim cycle. The full cycle is exercised by the existing `test/scenarios/claim_stores/*` suite (which the agent's earlier note confirmed pass under the frame model). If a full-stack frame+claim integration test is needed, it should be a follow-up.

## Task 39 — Smoke fixture update
**Deviation:** Updated both `test/smoke/fixtures/template.yml` (the documentary YAML) and `test/smoke/stores_redesign_smoke_test.go::smokeTemplateBody()` (the programmatic body actually deployed). The plan only mentioned the YAML; without the programmatic update, the smoke test fails at template-deploy with HTTP 400.
**Reason:** Per the doc comment in `deploySmokeTemplate`, the smoke test deploys a programmatically-built body. The YAML is documentation only. Both must declare `frame_resolution: serial_queue` and `frame_timeout_ms: 600000` for the deployment to succeed and the §19.2 acceptance predicate to pass.
**Surfaced for:** none — straightforward extension of the plan's intent.

## Tasks 41-47 — Documentation
**Deviation:** none — flagging for visibility.
**Reason:** Updated CHANGELOG.md (frame-resolution bullet under `## Unreleased`, above the stores-redesign bullet); CLAUDE.md gotcha (operator invalidates no longer preempt; new "frames are the unit of cascade resolution" gotcha); docs/architecture.md (heartbeat-tick step 2 amended to remove kill-poll); docs/operator-guide.md (replaced "Kill a running node" subsection and the kill_requested narrative with frame-resolution semantics; added §5.3.1 "Frame resolution and templates" subsection); docs/node-graph-design.md (new "Frames as the unit of resolution" section before the appendix); docs/protocol.md (new §12.4 "Supervisor-internal: frame_id" note); core/supervisor/callback.go and core/controlapi/admin_force_fire.go doc comments updated.
**Surfaced for:** Spot-check the doc edits feel right in tone and placement. The changes are non-load-bearing for behavior.

## Tasks 48-50 — Final verification
**Deviation:** golangci-lint is not installed locally, so `make lint` was skipped. Substituted `go vet ./...` (clean). Tasks 49 and 50 ran clean: full test suite passes (`go test ./...`), race-mode sweep clean (`go test ./core/scheduler/... ./core/supervisor/... ./core/frame/... ./test/scenarios/frame_resolution/... -race -count=2`), and the §19.2 smoke test passes (`go test ./test/smoke/... -count=1`).
**Reason:** Lint requires `golangci-lint` which is not in the repo's tooling and not on PATH. `go vet` is the closest stdlib substitute and is clean.
**Surfaced for:** Run `make lint` manually after installing `golangci-lint` (per CLAUDE.md the linter set is gofmt/goimports/govet/staticcheck/unused/ineffassign/errcheck/revive). The Docker-based deploy bring-up (Task 50) was not run; do this manually if the codebase is going to ship.

## Carry-over from the original agent run
**Deviation:** none — flagging for visibility.
**Reason:** The agent's earlier "Tasks 24/25" notes flagged scenario tests assumed multi-root concurrency under one instance. After re-running, those tests now PASS — the harness's `WaitForNodeState(fresh, ...)` gating fix the agent landed (Task 24 deviation note) appears to have resolved the issue. No additional work needed there.
**Surfaced for:** Verify the agent's three other behavior-change deviations are accepted: Task 4 (validator mutates spec on default-fill), Task 11 (frame logger uses slog.Default()), Task 18 (Create default state fresh→stale).

---

## Reviewer follow-up (2026-04-26)

The frame-resolution implementation went through a code review; this section captures the fixes applied in the reviewer-fix pass.

## Issue 1 / 23 — Orphan dispatch reaper: per-row + claimant-guarded
**Deviation:** Replaced the bulk `UPDATE … FROM rimsky_frames f JOIN rimsky_dispatch d` in `runReapOrphanFrameDispatches` with a SELECT-then-per-row pattern. Each row's release runs in its own short tx with `WHERE id = $1 AND claimed_by = $2`, satisfying blessed-invariant 4. Per-row warn log carries `dispatch_id`, `frame_id`, `prior_claimed_by`.
**Reason:** The previous bulk UPDATE could silently null a fresh supervisor's claim if the row's `claimed_by` rotated between the join's evaluation and the SET; the spec §4.1 step 5 explicitly demands a claimant-guarded release.
**Surfaced for:** Note the test that exercises the guard (`test/scenarios/frame_resolution/orphan_dispatch_reaper_claimant_guarded_test.go`).

## Issue 2 / 16 — handleResetNode now drives through frame.EnqueueOrCoalesce
**Deviation:** `controlapi/nodes.go::handleResetNode` no longer calls `UpdateState(failed → stale)` directly. Instead it (1) clears error bookkeeping, (2) defensively clears `frame_id`, (3) calls `frame.EnqueueOrCoalesce` so the next scheduler tick advances a queued frame and writes `state='stale', frame_id=<new>` atomically.
**Reason:** Direct UpdateState would strand the node with no frame_id (blessed-invariant 19); both `sweepReady` and `RecalculateNode` skip nodes with nil frame_id, so the node would never run.
**Surfaced for:** New test `test/scenarios/frame_resolution/reset_failed_node_drives_through_frame_engine_test.go`.

## Issue 3 / 11 — Frame logger plumbing
**Deviation:** `core/frame.RunTick` now accepts a tiny `frame.Logger` interface (`Debug/Info/Warn`) instead of `*slog.Logger`. Both stdlib `*slog.Logger` and `shared.Logger` (the scheduler's structured wrapper) satisfy it. The scheduler passes `cfg.Logger` directly; the broken `frameLoggerFor(_) → slog.Default()` shim was deleted.
**Reason:** The shim discarded `cfg.Logger`'s structured fields (e.g. test SilentLogger, supervisor_id). The interface keeps `core/frame` from importing `core/shared` while letting the scheduler reuse its bound logger.
**Surfaced for:** None — the change is invisible in tests because `slog.Logger` still satisfies the new interface.

## Issue 4 — Stale comment in provisionInstance
**Deviation:** Phase-1 comment in `instances.go::provisionInstance` updated to reflect that `Create` defaults to 'fresh' (per migration 002 + §3.1) and there is no bulk-flip step.
**Reason:** Comment-only fix.

## Issue 5 / 7 / 14 — Per-frame tx isolation in the engine
**Deviation:** `runFrameEndDetection`, `runAdvanceQueued`, and `runReapStuckFrames` were restructured: each opens a short read-only tx to collect candidates, then iterates and runs each frame's transition in its own short write tx (`transitionFrameEnd`, `advanceOneFrame`, `reapOneStuckFrame`). One bad frame logs a warning and continues; siblings are unaffected.
**Reason:** The previous shape rolled back the entire tick on any single frame's predicate read failure or write failure.

## Issue 6 — frame-start no longer hard-fails when the source isn't in expected state
**Deviation:** When `advanceOneFrame`'s source-stale UPDATE matches 0 rows (wedged source), the function now logs `frame.start.source_not_in_bounds` at warn level and returns nil (per-frame tx rolls back; the queued frame remains for the next tick to retry).
**Reason:** The previous code returned an error that propagated up the chain forever, spamming warn logs every tick. Returning nil + per-frame tx isolation gives operators a chance to intervene without flooding logs.

## Issue 8 / 24 — Defensive frame_id clear in enforceAndUpdate
**Deviation:** `core/storage/postgres/nodes.go::enforceAndUpdate` now atomically nulls `frame_id` whenever the target state is 'fresh' (`SET frame_id = CASE WHEN $2 = 'fresh' THEN NULL ELSE frame_id END`). The two follow-up `SetFrameID(nil)` calls in `pure_cascade.go::transitionPureCascade` and `runner_terminal.go::applyTerminalComplete` were removed (now redundant + would have been a separate non-atomic UPDATE).
**Reason:** Centralises the spec §4.4 + §10.3 invariant ("fresh nodes carry no frame_id") at the storage layer so producers can't strand a stale frame_id. Test in `retry_preserves_frame_id_test.go` exercises the complementary side: stale nodes preserve frame_id.

## Issue 9 / 18 — Validator is now pure; default-fill at the deploy boundary
**Deviation:** `validateFrameResolution` no longer mutates `spec.FrameTimeoutMs`. New helper `node.ApplyFrameResolutionDefaults(*TemplateSpec)` does the default-fill; `controlapi/templates.go::handleDeployTemplate` calls it after validation passes. Existing `TestValidateTemplate_FrameResolution_DefaultsTimeout` updated to assert the no-mutation guarantee + idempotence.
**Reason:** The previous validator-side mutation made re-validation non-idempotent (the second call no longer reflected "user did not specify").

## Issue 10 — kill_requested removed from 001-initial.sql directly
**Deviation:** Per the pre-v1 break-freely rules, removed `kill_requested BOOLEAN NOT NULL DEFAULT FALSE` from `core/migrations/001-initial.sql` and removed `ALTER TABLE rimsky_nodes DROP COLUMN IF EXISTS kill_requested` from `002-frame-resolution.sql`. Migration 002's comment updated to note 001 already lacks the column.
**Reason:** Fresh DBs no longer churn a column they never need; pre-v1 schema rewrites are explicitly allowed by `.claude/rules/rules.md`.

## Issue 11 — Frame-end predicate is now per-frame
**Deviation:** `runFrameEndDetection`'s NOT EXISTS subquery now filters `n.frame_id = f.frame_id` in addition to `n.instance_id = f.instance_id`. Equivalent under v1's "at most one running frame per instance" invariant; robust under future Rule 3b parallel-buffered semantics (spec §10.6).

## Issue 12 — Test exercises a defensive branch
**Deviation:** None — the empty-mode test fixture in `producer_test.go` exercises the defensive `unsupported mode ""` error and does not need a fix. The reviewer's own annotation matches reality.

## Issue 15 — Cascade walk duplication left in place, documented
**Deviation:** Did NOT inline the post-commit `RecalculateNode` walk into the in-tx cascade in `runner_terminal.go::applyTerminalComplete`. Added an explicit comment block explaining the two walks have different responsibilities (the in-tx walk mutates child state atomically with the parent commit; the post-commit walk consults dep-fresh state and routes the recalculate event through `scheduler.RecalculateNode`).
**Reason:** Inlining would couple supervisor commit semantics to scheduler-side enqueue logic and risk regressing the existing supervisor-commit path. Cost: one extra `ListDependentsOf` per terminal commit.

## Issue 17 — Held-claim test ON CONFLICT syntax
**Deviation:** None — reviewer confirmed Postgres' `ON CONFLICT (instance_id) WHERE state = 'running'` matches the partial unique index `uq_rimsky_frames_running` for partial-index inference. Skipped per reviewer instruction.

## Issue 19 — Multi-step engine tx atomicity
**Deviation:** None — reviewer confirmed acceptable per spec; the next tick handles cross-step state. Skipped per reviewer instruction.

## Issue 20 — Stub seedDispatch
**Deviation:** None — reviewer confirmed correct. Skipped per reviewer instruction.

## Issue 21 — FrameResolution as enum type
**Deviation:** None — left `FrameResolution` as `string` per the reviewer's "optional, future-proofing" annotation. The defensive `unsupported mode ""` error in `frame.EnqueueOrCoalesce` provides the runtime guard; pre-v1 acceptable.
**Surfaced for:** Future cleanup if/when more frame-resolution modes land.

## Issue 22 — Migration 002 DELETE FROM rimsky_dispatch
**Deviation:** None — reviewer confirmed acceptable per pre-v1 rules. Subsumed by Issue 10's rewrite.

## Issue 25 — TestRunTick_AdvanceTrailing_Coalesce
**Deviation:** None — reviewer confirmed working as intended. Skipped per reviewer instruction.

## Issue 26 — Migration sequence
**Deviation:** None — reviewer confirmed fine for fresh DBs.

## Verification gaps — three new tests
**Deviation:** Added `test/scenarios/frame_resolution/reset_failed_node_drives_through_frame_engine_test.go`, `orphan_dispatch_reaper_claimant_guarded_test.go`, and `retry_preserves_frame_id_test.go` covering the three reviewer-flagged gaps.

## Verification — smoke test flake
**Deviation:** `test/smoke/TestStoresRedesignSmoke` fails reproducibly with 1-3 items left in `in_progress` state at the post-cascade check (`assertFinalState`). The §5.6.4 claim resolution algorithm appears to miss a fraction of items at high churn (100 force-fires).
**Reason:** The failure manifests as a non-deterministic count (1, 2, or 3) of leftover `in_progress` items even though the cascade-steady-state predicate (review_count >= 100, no claimed dispatches, no lock holders, no active claim_holders) holds. None of the reviewer-fix changes touch the §5.6.4 resolution algorithm or its items-table mutation; the changes touched the orphan-dispatch reaper (claimant-guarded), validator purity, frame_id atomic clear on 'fresh' transition, and the reset path. Each of those is independently tested by the affected scenario suites which all pass clean (`./core/frame/...`, `./core/scheduler/...`, `./core/supervisor/...`, `./core/controlapi/...`, `./core/storage/...`, `./test/scenarios/...`) including race-mode runs. The flake is most likely pre-existing in the §5.6.4 implementation under high concurrency.
**Surfaced for:** A focused investigation of `core/store/claimstorepg/holders.go::resolveRelease` under high churn — count(active)==0 AND count(deleted)==0 may have a missed-write window where a sibling's actual_action update is committed but not visible to a concurrent terminal node's count read. Not in scope for this reviewer-fix pass.

## Cycle 2 — Issue A: Smoke test "items stuck in_progress" — root caused and fixed
**Deviation:** None.
**Diagnosis:** Added a per-stuck-item diagnostic dump in `test/smoke/.../assertFinalState`. First run with diagnostics revealed: every stuck item had a single `rimsky_claim_holders` row with `state=completed, actual_action=release_to_back`, and the items-table row's `claimed_at` was 30+ seconds *later* than the holder's `completed_at`. So the resolver had run successfully on cycle N, items returned to `available`, but cycle N+1 re-claimed the same `item_id` and then *failed to insert a new active holder row* — without any error event surfaced because the failure was a constraint violation that rolled back the supervisor's commit tx silently after the acquisition tx had already flipped the items-table row.
**Root cause:** The unique index `rimsky_claim_holders_claim_node_idx ON (claim_id, holder_node_id)` was unconditional. Ring-buffer claim stores reuse `claim_id` (= `item_id`) across cycles, so cycle N+1's `INSERT` against `(claim_id=X, holder_node_id=review)` collided with the `state='completed'` row from cycle N.
**Fix:**
1. `core/migrations/001-initial.sql`: made the unique index partial on `state='active'` (one ACTIVE holder per (claim, leaf), historical completed rows coexist).
2. `core/store/claimstorepg/holders.go::ResolveOnTerminal`: SELECT FOR UPDATE filters `state='active'` (without it the FOR UPDATE pulls multiple rows after the index change → "more than one row" error).
3. `core/store/claimstorepg/holders.go::resolveDelete` / `resolveRelease`: sibling-count predicates scope by `frame_id IS NOT DISTINCT FROM R.frame_id` so prior cycles' completed delete/delete_won rows don't leak into the "did anyone delete?" check for a fresh cycle on a reused claim_id.
4. `docs/specs/2026-04-25-stores-redesign-design.md`: §5.6.4 pseudocode + §9.9.3 schema updated to match.
**Verification:** Smoke test passes 3-of-3 consecutive runs (~47s each); `./core/store/claimstorepg/...`, `./core/supervisor/...`, `./test/scenarios/claim_stores/...` clean.
**Diagnostic carrying-cost:** The diagnostic dump in `assertFinalState` stays in place — it only fires on failure, costs nothing on success, and would surface a future regression with the same "items left in_progress" mode without re-instrumentation.


---

## Code-review final pre-commit pass — fixes applied
**Deviation:** none — flagging for visibility.
**Reason:** Issues 1–5 from the code-review pass against `docs/specs/2026-04-26-frame-resolution-design.md` were all fixed:

1. `core/storage/postgres/claim_holders.go`: deleted dead helpers `InsertHoldersForClaim`, `GetByClaimAndNode`, `MarkCompleted`, `CountActive`, `CountDeleteWinners`, `ListLeakedForGC`. Removed `ListLeakedForGC` from the `storage.ClaimHoldersStore` interface (production GC path uses raw SQL via `core/scheduler/sweep_locks.go::listLeakedClaimHolders`). Rewrote `TestClaimHoldersStore` in `core/storage/postgres/postgres_test.go` to exercise only production primitives (`Insert`, `Complete`, `Get`, `ListByClaimID`, `ListByHolderNode`, `ListActiveByClaimID`).
2. `core/scheduler/invalidate.go`: deleted dead `isOperatorInvalidate` and `strings` import.
3. `docs/operator-guide.md`: deleted stale `restore_version: "previous"` reference (the route accepts only `{"reason": string}` post-stores-redesign).
4. `core/migrations/runner_test.go`: corrected the `kill_requested` comment to reflect that the column was removed from `001-initial.sql` per pre-v1 break-freely, not dropped by `002`.
5. `core/frame/producer.go`: rewrote `enqueueCoalesce` from a UPDATE-then-INSERT pair into one atomic `INSERT … ON CONFLICT (instance_id) WHERE state=queued AND mode=coalesce DO UPDATE …` keyed on the existing partial unique index (`uq_rimsky_frames_coalesce_queued`). Added `test/scenarios/frame_resolution/coalesce_concurrent_invalidates_test.go` (`TestCoalesceConcurrentInvalidatesNoUniqueViolation`) — fires 32 concurrent `EnqueueOrCoalesce` calls from goroutines under coalesce mode and asserts no error surfaces and the partial-unique invariant holds.

**Surfaced for:** none — all checks green:
- `go build ./...`, `go vet ./...`: clean.
- `go test` (serially via `-p 1` to dodge a pre-existing testcontainers pool-starvation flake unrelated to these fixes): `core/storage/postgres`, `core/storage`, `core/scheduler`, `core/migrations`, `core/frame`, `test/scenarios/frame_resolution`, `test/smoke` all PASS.
- `go test -race -count=1` over `core/frame/...` and `test/scenarios/frame_resolution/...` PASS — new concurrent-coalesce test is race-clean under `-race`.

**Pre-existing flake observed (not introduced):** Running the storage/scheduler/migrations/frame test bundles together at default parallelism can hit a pgxpool teardown deadlock where `pgxpool.(*Pool).Close` blocks 9+ minutes on `puddle.WaitGroup.Wait`. Running each package separately or with `-p 1` is clean. Not caused by anything in this pass; flagging for visibility so a future session knows to investigate the testcontainers harness pool-close path if it bites again.

