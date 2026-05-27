# Post-Data-Platform Cleanup Implementation Plan

**Spec:** `.ok-planner/specs/2026-05-17-post-data-platform-cleanup-design.md`
**Goal:** Land six post-delivery follow-ups: an A+B reviewer pass, a smoke-flake investigation, a claim-handle state-column refactor (5-stage cutover), a cold-read paydown of oversized runtime files, a multi-replica `sensor-cron` test fixture, and an asset/lineage coverage uniformity audit-and-upgrade.
**Architecture:** Six independent work items in one plan; sequenced internally (Item 2 → 3 → 1 → 4 → 5+6). Item 1 dominates the surface — it replaces `held_durable bool` on `rimsky_claim_handles` with a 3-state column (`active`, `committed`, `abandoned`), introduces `resolved_at` + a retention sweep parallel to `SweepLineageRetention`, and lands as a 5-section cutover (additive → reader cutover → promote-not-delete → drop bool → docs). Items 4, 5, 6 are scoped paydown / test additions on the post-Item-1 codebase.
**Tech Stack:** Go (root module + `foundation/` submodule), Postgres + SQLite drivers under `foundation/persistence/`, scenario tests under `test/scenarios/` using `testcontainers-go`, slog (stdlib), pgx/v5, modernc.org/sqlite, robfig/cron/v3.

---

## Reading order for the implementer

This plan is executed in one fresh `/execute-plan` run, start to finish. The five sections under Item 1 (Stage 1 → 5) are quiescence points within that single run — each is internally consistent, with `make test-all` clean before moving to the next — but the implementer does not pause for user review between them. Same for Item 4's Tier 1 → Tier 2 and Item 6's Phase 1 → Phase 2.

Before starting, read these files in this order:

1. `.ok-planner/specs/2026-05-17-post-data-platform-cleanup-design.md` — the full spec (referenced throughout).
2. `.ok-planner/design/concepts/claim-handle.md` — what `rimsky_claim_handles` is and what owns it.
3. `.ok-planner/design/concepts/claim-lifetime.md` — `subgraph` vs `durable` lifetime distinction.
4. `.ok-planner/design/concepts/auto-terminal.md` — the resolution path Item 1 reshapes.
5. `.ok-planner/design/concepts/asset.md` — the asset query Item 1 changes.
6. `.ok-planner/design/concepts/claim-tree.md` + `.ok-planner/design/concepts/cancel-siblings.md` — recursive walks Item 1's skip-filter expression touches.
7. `.ok-planner/design/concepts/orphan-reaper.md` — the reaper's skip-rule Item 1 simplifies.
8. `CLAUDE.md` — the gotchas + `@blessed-invariant` block (invariants 4 and 22 in particular).
9. `.claude/rules/rules.md` — pre-v1 freedom, after-code-changes discipline, code style.

## Execution order

The six items run in this sequence within the single plan run:

1. **Item 2** — A+B follow-up review pass.
2. **Item 3** — Smoke flake investigation.
3. **Item 1** — Claim-handle state-column refactor (Stage 1 → 5).
4. **Item 4** — Cold-read paydown (Tier 1 → Tier 2).
5. **Item 5** + **Item 6** — Multi-replica `sensor-cron` test fixture; asset/lineage coverage uniformity (Phase 1 → Phase 2).

## File map

### Created

- `foundation/persistence/postgres/migrations/<N>-claim-handles-state-column.sql` — Stage 1 schema additions (postgres).
- `foundation/persistence/sqlite/migrations/<N>-claim-handles-state-column.sql` — Stage 1 schema additions (sqlite).
- `foundation/persistence/postgres/migrations/<N+1>-claim-handles-drop-held-durable.sql` — Stage 4 schema drop (postgres).
- `foundation/persistence/sqlite/migrations/<N+1>-claim-handles-drop-held-durable.sql` — Stage 4 schema drop (sqlite).
- `foundation/spec/claim_handle_state.go` — `ClaimHandleState` type + constants + `ErrIllegalClaimHandleTransition` sentinel.
- `runtime/sweep_claim_handle_retention.go` — `SweepClaimHandleRetention` sweep.
- `runtime/sweep_claim_handle_retention_test.go` — pgtest of the sweep cutoff predicate.
- `runtime/runner_acquire_named_locks.go` — Item 4 Tier 1 split.
- `runtime/runner_acquire_claims.go` — Item 4 Tier 1 split.
- `runtime/runner_acquire_holders.go` — Item 4 Tier 1 split.
- `sensors/sensor-cron/multi_replica_test.go` — Item 5 scenario test.

### Modified

- `foundation/persistence/claim_handles.go` — `ClaimHandleRow.State`; interface methods `Promote`, `ListByState`, `ListByInstanceAndState`; remove `SetHeldDurable`, `ListHeldDurableByInstance` (Stage 4).
- `foundation/persistence/postgres/claim_handles.go` — implement new methods; flip all `held_durable` filter expressions to `state`-column equivalents; drop dual-write scaffold in Stage 4.
- `foundation/persistence/sqlite/claim_handles.go` — same flips as the postgres driver.
- `runtime/runner_acquire.go` — Item 4 Tier 1 split; orchestration shell only post-split.
- `runtime/terminal_decision.go` — Stage 3 flip from `Delete` to `Promote(committed|abandoned)`; Item 4 Tier 1 helper extraction in `ResolveClaimHandleTerminal`; cancel-siblings skip-filter expression change.
- `runtime/auto_terminal.go` — Stage 3 + Item 4 Tier 2 re-measure; skip-filter expression change in `resolveParentClaimChain`.
- `runtime/instance_termination.go` — Stage 3 row-discovery flip for `ReleaseHeldDurableClaims`.
- `runtime/orphan_reaper.go` — Stage 2 skip-rule expression flip.
- `runtime/retention_sweeps.go` — `RetentionConfig.ClaimHandlesTrailing` field; wire `SweepClaimHandleRetention` into the scheduler tick.
- `control/controlapi/assets.go` — Stage 3 row-discovery flip for the `DELETE /instances/{id}/assets/{alias}` handler.
- `control/config/config.go` (or wherever `cfg:retention` parses) — accept the new `claim_handles_trailing` YAML key.
- `runtime/subgraph_dispatch.go` — Item 4 Tier 2 re-measure (may stay as-is if Item 1 pulled it under guideline).
- `runtime/runner_acquire_test.go`, `runtime/terminal_decision_test.go`, `runtime/auto_terminal_test.go`, `runtime/orphan_reaper_test.go`, `runtime/instance_termination_test.go` — assertion updates per stage (row-existence vs row-preservation).
- Various scenario tests under `test/scenarios/asset/`, `test/scenarios/lineage/`, `test/scenarios/run_tree/`, `test/scenarios/forensics/` — assertion updates and any Phase-2 upgrades / split companions per the Item 6 classification.
- `.ok-planner/design/concepts/claim-handle.md`, `claim-lifetime.md`, `asset.md`, `claim-tree.md`, `cancel-siblings.md`, `auto-terminal.md`, `orphan-reaper.md` — Stage 5 mutations.
- `CLAUDE.md` — `@blessed-invariant 4` text refresh, `@blessed-invariant 22` text refresh, gotchas-section text refresh for held-durable wording.
- `CHANGELOG.md` — Unreleased section, one bullet per item.
- `feature-index.md` — if file layout changed enough to matter (Item 4 will).

### Notes for the implementer

- Migration numbering: use the next two integers after the highest existing number in `foundation/persistence/postgres/migrations/`. Mirror those numbers in `foundation/persistence/sqlite/migrations/`. The two `<N>-…` placeholders in this plan must be replaced with concrete numbers before writing the files.
- Postgres + SQLite migrations must stay in lockstep — the same column adds, same backfill, same CHECKs (SQLite's CHECK enforcement is the same).
- The plan refers to "`make test-all` clean" as the per-stage signal. This is `make test-all` over all three Go modules per `CLAUDE.md`'s build commands. If `testcontainers` isn't available (Docker not running), the scenario tests under `test/scenarios/` and `foundation/persistence/` will fail; in that case the implementer must surface this as a blocker rather than skipping scenarios.

---

## Item 2 — A+B follow-up review pass

### Scope

Review the uncommitted A+B follow-up: the `col:rimsky_lineage.outcome` column + the 7 new event-emit sites + the `claim_commit` → `claim_terminal` rename propagation through `subscribers/openlineage/`, `control/controlapi/`, the CLI tests, and the `CLAUDE.md` gotcha block.

### Task 2.1 — Identify the uncommitted A+B surface

**Files:** none modified.

**Steps:**

1. Run `git status` and `git diff --stat HEAD` to list the uncommitted working-tree changes.
2. Read each modified file (or the relevant diff sections of large files) to internalize the scope under review. Pay particular attention to: the lineage row writer at `runtime/lineage_writer.go` (`outcome` column population), the 7 new event-emit sites that the spec/A+B follow-up added, the openlineage subscriber under `subscribers/openlineage/`, the CLI test files, and the CLAUDE.md gotcha block referencing the lineage shape.

**Verification:**

```
git status --short      # surfaces every modified / untracked file
git diff --stat HEAD    # surfaces the per-file change footprint
```

The implementer's working memory now holds the surface for Task 2.2.

### Task 2.2 — Dispatch reviewer subagent against the A+B surface

**Files:** none modified.

**Steps:**

1. Dispatch a `general-purpose` subagent with this prompt:

   ```
   Review the uncommitted A+B follow-up in the rimsky working tree.

   Scope: every uncommitted change (run `git diff HEAD` to see all of them) that
   touches the lineage outcome column, the 7 new event-emit sites, the
   `claim_commit` → `claim_terminal` rename propagation through
   `subscribers/openlineage/` + `control/controlapi/` + CLI tests + CLAUDE.md.

   Check for:
   - Inertness invariants 20, 21, 24 (claim payload / blob / message bytes
     never logged, formatted, validated beyond schema, transformed, attached
     to traces, or included in error messages).
   - Claimant-guarded release (invariant 4) — every DELETE / UPDATE that
     mutates live ownership rows carries `AND … = supervisor_id`.
   - Schema consistency: postgres and sqlite drivers stay in lockstep on the
     new column.
   - Lineage record kind / outcome column semantics: `claim_terminal` row's
     `outcome` discriminator values (`committed`, `abandoned`, `force_cancelled`).
   - Test coverage: every new event-emit site has at least one assertion path.
   - Concept-doc and CLAUDE.md text matches the implementation.

   Report Critical / Important / Minor findings with file:line citations and
   suggested fixes. Fix-everything rule applies to all findings regardless of
   severity (this is the project rule from .claude/rules/rules.md).
   ```

2. Receive the reviewer's report.

**Verification:**

Reviewer report present, with findings list (possibly empty).

### Task 2.3 — Apply review-cleanup loop until clean

**Files:** whatever the reviewer flagged.

**Steps:**

1. If the reviewer reported "Approved" with no findings, this task is a no-op; skip to Task 2.4.
2. Otherwise, for each finding (regardless of severity label), apply the suggested fix. Per project rule (`.claude/rules/rules.md`), every finding gets fixed; no triage.
3. Re-dispatch the reviewer subagent (Task 2.2's prompt) against the now-modified working tree.
4. Loop until the reviewer reports "Approved" with no findings.

**Verification:**

```
make build-all && make test-all && make lint
```

All three clean. Reviewer-loop final report: Approved.

### Task 2.4 — A+B review done marker

**Files:** none modified.

**Steps:**

1. Record completion of Item 2 in the plan-notes file (`.ok-planner/plans/2026-05-17-post-data-platform-cleanup-notes.md`, created by execute-plan): a short line "Item 2 (A+B review) clean: <N> cleanup cycles." If `<N>` is zero, "clean on first pass."

**Verification:**

Implementation-notes file contains the Item 2 completion line.

---

## Item 3 — Smoke flake investigation

Two flakes to address, each via the same investigate-then-fix-then-validate loop.

### Task 3.1 — `TestParkedLifecycleResumeOnDeadline` reproduction

**Files:** none modified (investigation only).

**Steps:**

1. Run the target test in isolation 30 times to confirm pass-in-isolation baseline:
   ```
   go test ./test/scenarios/... -run TestParkedLifecycleResumeOnDeadline -count=30 -v
   ```
2. Run the same target test under heavy concurrent load: in one terminal-equivalent, run `make test-all` in the background; in another, run the target test 50 times:
   ```
   make test-all &
   MAKE_PID=$!
   go test ./test/scenarios/... -run TestParkedLifecycleResumeOnDeadline -count=50 -v 2>&1 | tee /tmp/parked_flake_repro.log
   wait $MAKE_PID
   ```
3. Inspect `/tmp/parked_flake_repro.log` for any failure. Capture the failure mode (panic / assertion / timeout / data-race report) and the line at which it fires.

**Verification:**

`/tmp/parked_flake_repro.log` exists. Either the flake reproduced (the log contains a FAIL line) or it did not.

### Task 3.2 — `TestParkedLifecycleResumeOnDeadline` root-cause classification

**Files:** none modified (investigation only).

**Steps:**

1. If Task 3.1 did NOT reproduce the flake, run Task 3.1's load loop one more time with `-count=100`. If still no repro, classify the flake as "intermittent under conditions not yet reproduced; defer to passive monitoring" — record this disposition in the plan-notes file and proceed to Task 3.4 (but still validate via the 50-100 runs in Task 3.4 to ensure no regression).
2. If Task 3.1 DID reproduce, examine the failure mode against three categories:
   - **Real race in production code** — failure mode points at a synchronization gap in `runtime/` (e.g., the parked-deadline resume path); the test exposes a real bug. Read the relevant source (`runtime/runner.go`, `runtime/sweep_parked_nodes.go`, the on-acquire-unavailable + park handler chain) and identify the race.
   - **Fixture timing** — failure mode points at a test-fixture timing assumption (e.g., the 10-second `resumeAt` budget is too tight under load; the Success-script-swap-before-probes window is too narrow). Fix is in the test file or its harness setup.
   - **`testcontainers` parallelism artifact** — failure mode is a Postgres container startup timeout, a connection refused error, or similar infrastructure noise rather than a rimsky-code issue.
3. Record the classification in the plan-notes file with a citation to the failure-mode evidence.

**Verification:**

Plan-notes file contains the Task 3.2 classification with file:line citation evidence (or the "no-repro / passive monitoring" disposition if Task 3.1 step 1 hit).

### Task 3.3 — `TestParkedLifecycleResumeOnDeadline` fix

**Files:** depends on Task 3.2's classification.

**Steps:**

1. If classification is "real race in production code": fix the synchronization gap in the appropriate `runtime/` file(s). Per project rule, fix the root cause, not the symptom — no workarounds.
2. If classification is "fixture timing": extend the timing budget or restructure the fixture to remove the racy assumption. Document the change in the test file with a `// @reason:` comment on the touched lines if non-obvious.
3. If classification is "`testcontainers` parallelism artifact": serialize the affected scenario test (add the `pgtest.Serial(t)` helper if one exists, or wire one up) or extend the container-readiness timeout in the relevant test fixture. Document the change.
4. If classification was "no-repro / passive monitoring" in Task 3.2: this task is a no-op.

**Verification:**

```
make build-all
go test ./test/scenarios/... -run TestParkedLifecycleResumeOnDeadline -count=10 -v
```

Both clean.

### Task 3.4 — `TestParkedLifecycleResumeOnDeadline` validation under load

**Files:** none modified.

**Steps:**

1. Run the validation loop:
   ```
   make test-all &
   MAKE_PID=$!
   go test ./test/scenarios/... -run TestParkedLifecycleResumeOnDeadline -count=100 -v 2>&1 | tee /tmp/parked_flake_validate.log
   wait $MAKE_PID
   ```
2. Confirm all 100 runs pass.

**Verification:**

`/tmp/parked_flake_validate.log` shows 100 PASS lines and no FAIL. `make test-all` exits 0.

If a failure occurred during validation: return to Task 3.2 with the new evidence. Repeat the diagnose-fix-validate cycle.

### Task 3.5 — `TestPerInstanceOrderingInvariant_Concurrent` reproduction

**Files:** none modified.

**Steps:**

1. Locate the test file (`grep -rln "TestPerInstanceOrderingInvariant_Concurrent" test/`).
2. Run in isolation 30 times to confirm pass-in-isolation baseline:
   ```
   go test ./<test-path>/ -run TestPerInstanceOrderingInvariant_Concurrent -count=30 -v
   ```
3. Run under heavy concurrent load, same shape as Task 3.1:
   ```
   make test-all &
   MAKE_PID=$!
   go test ./<test-path>/ -run TestPerInstanceOrderingInvariant_Concurrent -count=50 -v 2>&1 | tee /tmp/ordering_flake_repro.log
   wait $MAKE_PID
   ```

**Verification:**

`/tmp/ordering_flake_repro.log` exists; outcome captured.

### Task 3.6 — `TestPerInstanceOrderingInvariant_Concurrent` classify + fix + validate

**Files:** depends on classification.

**Steps:**

1. Repeat the Task 3.2 → 3.3 → 3.4 classify-fix-validate sequence on `TestPerInstanceOrderingInvariant_Concurrent`.
2. Specifically: classify into real-race / fixture-timing / testcontainers-artifact; fix per classification; validate with 100 runs under load.

**Verification:**

```
make test-all &
MAKE_PID=$!
go test ./<test-path>/ -run TestPerInstanceOrderingInvariant_Concurrent -count=100 -v 2>&1 | tee /tmp/ordering_flake_validate.log
wait $MAKE_PID
```

100 PASS lines, no FAIL. `make test-all` exits 0.

### Task 3.7 — Smoke flake findings recorded in CHANGELOG

**Files:** `CHANGELOG.md`.

**Steps:**

1. Append an Unreleased-section bullet describing the smoke flake resolution for each test, root cause, and the fix shape:

   ```
   - **Smoke flake fix: `TestParkedLifecycleResumeOnDeadline`** — <one-sentence root cause>; <one-sentence fix shape>.
   - **Smoke flake fix: `TestPerInstanceOrderingInvariant_Concurrent`** — <one-sentence root cause>; <one-sentence fix shape>.
   ```

**Verification:**

```
git diff CHANGELOG.md
```

Shows both bullets under the Unreleased section.

---

## Item 1 — Claim-handle state-column refactor

Five-section cutover. Each section ends with `make test-all` clean as a quiescence check before the next section starts. The implementer drives through all five in this single run; no pauses.

### Stage 1 — Additive: introduce `state` + `resolved_at`; dual-write

#### Task 1.1.1 — Postgres migration: ADD columns and CHECKs

**Files:** `foundation/persistence/postgres/migrations/<N>-claim-handles-state-column.sql` (new). Determine `<N>` by `ls foundation/persistence/postgres/migrations/ | sort -n | tail -1` and incrementing.

**Steps:**

1. Write the migration file with this content (substituting `<N>` for the chosen number in the filename only; the SQL body uses no number):

   ```sql
   -- Add state column with default 'active'; rows already in the table get
   -- 'active' as their default value; durable rows are subsequently backfilled
   -- to 'committed' below.
   ALTER TABLE rimsky_claim_handles
       ADD COLUMN state TEXT NOT NULL DEFAULT 'active'
       CHECK (state IN ('active', 'committed', 'abandoned'));

   -- resolved_at: timestamp the row transitioned out of 'active'. NULL while
   -- 'active'; set by Promote (Stage 3) to now(). The retention sweep
   -- filters on this column.
   ALTER TABLE rimsky_claim_handles
       ADD COLUMN resolved_at TIMESTAMPTZ NULL;

   -- Backfill: durable rows must transition to state='committed' AND have
   -- holder_supervisor_id nulled before the holder-state-consistency CHECKs
   -- can be enforced. Today's SetHeldDurable does NOT null holder_supervisor_id,
   -- so durable rows in the table still carry their original acquirer's id;
   -- null them as part of the same UPDATE.
   UPDATE rimsky_claim_handles
   SET state = 'committed',
       resolved_at = now(),
       holder_supervisor_id = NULL
   WHERE held_durable = TRUE;

   -- Two CHECK constraints, kept separate for clarity:
   --  - active rows must have a holder
   --  - non-active rows must not have a holder
   ALTER TABLE rimsky_claim_handles
       ADD CONSTRAINT rimsky_claim_handles_active_has_holder
       CHECK (state != 'active' OR holder_supervisor_id IS NOT NULL);
   ALTER TABLE rimsky_claim_handles
       ADD CONSTRAINT rimsky_claim_handles_inactive_has_no_holder
       CHECK (state = 'active' OR holder_supervisor_id IS NULL);
   ```

**Verification:**

```
go build ./...
```

Build succeeds (migration files are typically embedded via go:embed; build catches missing-file references).

#### Task 1.1.2 — SQLite migration: mirror the Postgres adds

**Files:** `foundation/persistence/sqlite/migrations/<N>-claim-handles-state-column.sql` (new). Same `<N>` as Task 1.1.1.

**Steps:**

1. Write the SQLite-flavored mirror of Task 1.1.1's migration. SQLite differences: `TIMESTAMPTZ` becomes `TIMESTAMP`; `now()` becomes `CURRENT_TIMESTAMP`; the ALTER TABLE ADD CONSTRAINT pattern works in modernc.org/sqlite if the SQLite version supports it (3.37+); if not, fall back to the `CREATE TABLE rimsky_claim_handles_new (…) → INSERT INTO rimsky_claim_handles_new SELECT … FROM rimsky_claim_handles → DROP TABLE rimsky_claim_handles → ALTER TABLE rimsky_claim_handles_new RENAME TO rimsky_claim_handles` pattern that other rimsky SQLite migrations use. Check existing SQLite migrations under `foundation/persistence/sqlite/migrations/` for the project convention before deciding.
2. Ensure the CHECK constraint expressions match the postgres ones.

**Verification:**

```
go build ./...
```

#### Task 1.1.3 — Add `ClaimHandleState` type, `ClaimLifetime` type, and error sentinel

**Files:** `foundation/spec/claim_handle_state.go` (new); `foundation/spec/graphs.go` (modified).

**Steps:**

1. Write the new file `foundation/spec/claim_handle_state.go`:

   ```go
   package spec

   import "errors"

   // ClaimHandleState is the rimsky_claim_handles.state enum.
   //
   // active: currently held by a supervisor, heartbeating.
   // committed: producer Commit fired; row preserved past terminal.
   // abandoned: producer Abandon fired (natural or force-cancel); row preserved.
   //
   // State transitions are claimant-guarded; revival transitions are not permitted
   // at the Go layer. See @blessed-invariant 4 (post-refactor text) for the two
   // guard shapes (active-row mutations carry the per-row holder_supervisor_id
   // guard; non-active-row deletions are guarded by absence + the scheduler-tick
   // advisory lock).
   //
   // @concept: claim-handle
   type ClaimHandleState string

   const (
       ClaimHandleStateActive    ClaimHandleState = "active"
       ClaimHandleStateCommitted ClaimHandleState = "committed"
       ClaimHandleStateAbandoned ClaimHandleState = "abandoned"
   )

   // ErrIllegalClaimHandleTransition is returned by ClaimHandleTable.Promote
   // when the affected-rows count is 0 — the row was not in the expected
   // active state or was held by a different supervisor.
   //
   // Mirror of cascade.ErrIllegalTransition for node-runs.
   var ErrIllegalClaimHandleTransition = errors.New("rimsky: illegal claim-handle state transition")
   ```

2. In `foundation/spec/graphs.go`, retype the existing untyped `ClaimLifetime` constants to use a named string type. Today:

   ```go
   const (
       ClaimLifetimeSubgraph = "subgraph"
       ClaimLifetimeDurable  = "durable"
   )
   ```

   Change to:

   ```go
   // ClaimLifetime is the per-claim lifetime: enum (subgraph default; durable
   // requires DataProcessing on the producer).
   //
   // @concept: claim-lifetime
   type ClaimLifetime string

   const (
       ClaimLifetimeSubgraph ClaimLifetime = "subgraph"
       ClaimLifetimeDurable  ClaimLifetime = "durable"
   )
   ```

3. Retype `ClaimHandleRow.Lifetime` from `string` to `spec.ClaimLifetime` at its declaration site in `foundation/persistence/claim_handles.go`. This will surface any caller that compared `row.Lifetime` to a bare string literal; fix each by using the typed constant.

4. Fix any callsite breakage that surfaces from the typing change. Most usage sites compare against `spec.ClaimLifetimeDurable` already; those compile fine with the typed constant.

**Verification:**

```
make build-all
```

Builds clean across all modules. Any spec-package-internal test for the constants stays green.

#### Task 1.1.4 — Add `State` field to `ClaimHandleRow`

**Files:** `foundation/persistence/claim_handles.go`.

**Steps:**

1. Read the existing `ClaimHandleRow` struct definition.
2. Add the `State` field of type `spec.ClaimHandleState`. Keep the existing `HeldDurable bool` field — it stays for the dual-write window (Stages 1–3) and is removed in Stage 4. Add the new field directly after `HeldDurable` to keep related fields visually grouped:

   ```go
   HeldDurable bool

   // State: rimsky_claim_handles.state enum. Active during the holding-subgraph
   // run; transitions to committed / abandoned at Promote (Stage 3). Until Stage
   // 4, HeldDurable + State are dual-written and dual-readable.
   //
   // @concept: claim-handle
   State spec.ClaimHandleState
   ```

3. Add `ResolvedAt *time.Time` adjacent to `State` (nullable; populated by `Promote` from Stage 3 onwards):

   ```go
   // ResolvedAt: timestamp the row exited 'active'. NULL while State == active.
   // Used by the retention sweep cutoff predicate (Stage 3).
   ResolvedAt *time.Time
   ```

**Verification:**

```
cd foundation && go build ./...
```

#### Task 1.1.5 — Update `ClaimHandleTable` interface signatures

**Files:** `foundation/persistence/claim_handles.go`.

**Steps:**

1. Add three new methods to the `ClaimHandleTable` interface:

   ```go
   // Promote transitions a claim handle from active to committed or abandoned.
   // Claimant-guarded: WHERE id = $1 AND state = 'active' AND holder_supervisor_id = $2.
   // The UPDATE sets state = newState, holder_supervisor_id = NULL, resolved_at = now()
   // in the same statement. Returns ErrIllegalClaimHandleTransition on
   // affected-rows = 0 (row not in active state, or supervisor mismatch).
   //
   // @blessed-invariant 4 (post-refactor): active-row mutations are claimant-guarded.
   Promote(ctx context.Context, id ClaimHandleID, supervisorID SupervisorID,
       newState spec.ClaimHandleState, tx Tx) error

   // ListByState returns claim-handle rows currently in the given state.
   ListByState(ctx context.Context, state spec.ClaimHandleState, tx Tx) ([]ClaimHandleRow, error)

   // ListByInstanceAndState returns rows joined through holder_node_id → rimsky_nodes
   // filtered by instance + state + lifetime. Replaces ListHeldDurableByInstance in
   // Stage 2 (where the existing call site flips to ListByInstanceAndState(instance,
   // committed, durable)).
   ListByInstanceAndState(ctx context.Context, instanceID InstanceID,
       state spec.ClaimHandleState, lifetime spec.ClaimLifetime, tx Tx) ([]ClaimHandleRow, error)
   ```

2. Do NOT remove `SetHeldDurable` or `ListHeldDurableByInstance` in this stage — they stay through Stages 1–3 and are removed in Stage 4.

**Verification:**

```
cd foundation && go build ./...
```

Build fails on the postgres + sqlite drivers (they don't implement the new methods yet). Expected; fixed in Task 1.1.6 + 1.1.7.

#### Task 1.1.6 — Postgres driver: implement Promote / ListByState / ListByInstanceAndState; flip SetHeldDurable to dual-write

**Files:** `foundation/persistence/postgres/claim_handles.go`.

**Steps:**

1. Implement `Promote`:

   ```go
   func (t *claimHandleTable) Promote(ctx context.Context, id ClaimHandleID,
       supervisorID SupervisorID, newState spec.ClaimHandleState, tx Tx) error {
       const q = `
           UPDATE rimsky_claim_handles
           SET state = $3,
               holder_supervisor_id = NULL,
               resolved_at = now()
           WHERE id = $1
             AND state = 'active'
             AND holder_supervisor_id = $2
       `
       cmd, err := tx.Exec(ctx, q, id, supervisorID, newState)
       if err != nil {
           return fmt.Errorf("promote claim_handle: %w", err)
       }
       if cmd.RowsAffected() == 0 {
           return spec.ErrIllegalClaimHandleTransition
       }
       return nil
   }
   ```

2. Implement `ListByState`:

   ```go
   func (t *claimHandleTable) ListByState(ctx context.Context,
       state spec.ClaimHandleState, tx Tx) ([]ClaimHandleRow, error) {
       const q = `SELECT <columns> FROM rimsky_claim_handles WHERE state = $1`
       // Use existing column list helper for consistency with other ListBy* methods.
       …
   }
   ```

3. Implement `ListByInstanceAndState` (join through `holder_node_id`):

   ```go
   func (t *claimHandleTable) ListByInstanceAndState(ctx context.Context,
       instanceID InstanceID, state spec.ClaimHandleState,
       lifetime spec.ClaimLifetime, tx Tx) ([]ClaimHandleRow, error) {
       const q = `
           SELECT <ch_columns>
           FROM rimsky_claim_handles ch
           JOIN rimsky_nodes n ON ch.holder_node_id = n.id
           WHERE n.instance_id = $1
             AND ch.state = $2
             AND ch.lifetime = $3
       `
       …
   }
   ```

4. Update `SetHeldDurable` to dual-write — preserve the existing signature `(ctx, id, supervisorID, heldDurable, tx)` and the `AND holder_supervisor_id = $3` claimant guard (today's implementation at `foundation/persistence/postgres/claim_handles.go:240-251`). Only extend the SET clause so the new state-column world stays consistent with the held_durable bool through Stages 1–3. `@blessed-invariant 4` requires the claimant guard stay on every active-row mutation:

   ```go
   // SetHeldDurable flips the held_durable column claimant-guarded.
   //
   // Stage 1 dual-write: mirror the post-cutover Promote semantics so the
   // state column stays consistent with held_durable through Stages 1–3.
   // Removed in Stage 4 along with the held_durable column.
   func (s *claimHandlesImpl) SetHeldDurable(
       ctx context.Context, id shared.UUID, supervisorID string, heldDurable bool, tx persistence.Tx,
   ) error {
       _, err := s.q(tx).Exec(ctx,
           `UPDATE rimsky_claim_handles
            SET held_durable = $1,
                state = CASE WHEN $1 THEN 'committed' ELSE state END,
                holder_supervisor_id = CASE WHEN $1 THEN NULL ELSE holder_supervisor_id END,
                resolved_at = CASE WHEN $1 THEN now() ELSE resolved_at END
            WHERE id = $2 AND holder_supervisor_id = $3`,
           heldDurable, id, supervisorID)
       if err != nil {
           return fmt.Errorf("lockholders.SetHeldDurable: %w", err)
       }
       return nil
   }
   ```

   Notes:
   - The signature is unchanged — every existing caller continues to pass `supervisorID`. Stage 4 removes both the field and the method entirely.
   - `SetHeldDurable(id, supervisorID, false, tx)` is not a path that fires in production — durable is one-way — but the schema permits the call shape; the CASE preserves today's columns on that branch.
   - The CASE on `holder_supervisor_id` does NOT need to coordinate with the second CHECK constraint added in Stage 1 step 3, because when durable transitions to TRUE, this UPDATE nulls `holder_supervisor_id` in the same statement (atomic) — the CHECK is satisfied at statement commit.

5. Update the `SELECT … FROM rimsky_claim_handles` column list used by Get / ListByX / etc. to include `state` and `resolved_at`. Update the row-scan helper to populate `ClaimHandleRow.State` and `ClaimHandleRow.ResolvedAt`.

**Verification:**

```
cd foundation && go build ./...
go test ./foundation/persistence/...
```

#### Task 1.1.7 — SQLite driver: mirror Task 1.1.6

**Files:** `foundation/persistence/sqlite/claim_handles.go`.

**Steps:**

1. Mirror Task 1.1.6 against the sqlite driver. SQL differences: `now()` becomes `CURRENT_TIMESTAMP`; the CASE syntax is the same; the `RowsAffected` pattern is the same on `modernc.org/sqlite`.
2. Keep the postgres + sqlite implementations behaviorally identical.

**Verification:**

```
cd foundation && go build ./... && go test ./foundation/persistence/...
```

#### Task 1.1.8 — Stage 1 quiescence: full test pass

**Files:** none (verification).

**Steps:**

1. Run the full build + test + lint:
   ```
   make build-all
   make test-all
   make lint
   ```
2. If any failure, fix before proceeding. Stage 1 must be clean before Stage 2.

**Verification:**

All three commands exit 0. The working tree is in a state where every row has both `held_durable` and `state` populated consistently; every `SetHeldDurable(id, true)` call writes both columns; no reader has flipped to consult `state` yet.

### Stage 2 — Reader cutover

#### Task 1.2.1 — Flip orphan reaper skip-rule expression

**Files:** `runtime/orphan_reaper.go`.

**Steps:**

1. Find the reaper's claim-handle skip predicate (`WHERE held_durable = FALSE AND expires_at < now()` or similar) in the `DeleteIfExpired` (or equivalent) call site.
2. Change to `WHERE state = 'active' AND expires_at < now()`. Functionally equivalent (held-durable rows have `state = 'committed'` after the Stage 1 backfill + dual-write), but the post-refactor expression aligns with the rest of the state-column world.

**Verification:**

```
go build ./... && go test ./runtime/... -run TestOrphan
```

#### Task 1.2.2 — Flip `ListByProducerScope` and `ExtendHeartbeat` filter expressions

**Files:** `foundation/persistence/postgres/claim_handles.go`, `foundation/persistence/sqlite/claim_handles.go`.

**Steps:**

1. For each driver, find the `ListByProducerScope` method's `WHERE` clause containing `(expires_at > now() OR held_durable = TRUE)`. Change to `WHERE state IN ('active', 'committed')` (committed rows still conflict for scope-byte-equal acquisition until they're swept or Released).
2. For each driver, find the `ExtendHeartbeat` method's `WHERE` clause containing `held_durable = FALSE`. Change to `state = 'active'` (composite with the existing claimant guard).

**Verification:**

```
cd foundation && go build ./... && go test ./foundation/persistence/...
make test-all
```

#### Task 1.2.3 — Flip recursive walk skip-filter expressions

**Files:** `runtime/terminal_decision.go`, `runtime/auto_terminal.go`.

**Steps:**

1. In `runtime/terminal_decision.go::cancelDescendantClaims`, find the per-row skip predicate that today reads something like `if row.HeldDurable { continue }`. Change to `if row.State != spec.ClaimHandleStateActive { continue }`.
2. In `runtime/auto_terminal.go::resolveParentClaimChain`, find the analogous skip predicate on the recursive walk. Change to the same `state != active` expression.
3. In `runtime/terminal_decision.go::cancelInFlightSiblings`, find the held-durable skip rule on the sibling-level walk. Change to the same `state != active` expression.

**Verification:**

```
go build ./... && go test ./runtime/... -run TestResolveParentClaimChain
go test ./runtime/... -run TestStrictCancelSiblings
```

#### Task 1.2.4 — Introduce `ListByInstanceAndState` callers; deprecate `ListHeldDurableByInstance`

**Files:** `runtime/instance_termination.go`, `control/controlapi/assets.go`.

**Steps:**

1. In `runtime/instance_termination.go::ReleaseHeldDurableClaims`, find the call to `ListHeldDurableByInstance(instanceID)`. Replace with `ListByInstanceAndState(instanceID, spec.ClaimHandleStateCommitted, spec.ClaimLifetimeDurable)`. (The semantics are identical because of Stage 1's backfill + dual-write.)
2. In `control/controlapi/assets.go`'s asset-listing and `DELETE /instances/{id}/assets/{alias}` handlers, find the analogous call. Replace with the same `ListByInstanceAndState` call.
3. Mark `ListHeldDurableByInstance` deprecated with a `// Deprecated: use ListByInstanceAndState(instance, committed, durable). Removed in Stage 4.` doc comment.

**Verification:**

```
go build ./... && go test ./...
```

#### Task 1.2.5 — Stage 2 quiescence: full test pass

**Files:** none (verification).

**Steps:**

1. `make build-all && make test-all && make lint`.

**Verification:**

All clean. The working tree now has all readers consulting `state`; `held_durable` is still populated by dual-write but no read site consults it.

### Stage 3 — Promote-not-delete

#### Task 1.3.1 — Flip `ResolveClaimHandleTerminal` to `Promote` instead of `Delete`

**Files:** `runtime/terminal_decision.go`.

**Steps:**

1. Find the Commit branch in `ResolveClaimHandleTerminal` — the place where, today, after firing the producer `Commit` verb, the row is `Delete`'d. Change to `Promote(id, supervisorID, spec.ClaimHandleStateCommitted, tx)`.
2. Find the Abandon branch. Change to `Promote(id, supervisorID, spec.ClaimHandleStateAbandoned, tx)`.
3. Handle `ErrIllegalClaimHandleTransition`: if Promote returns it, log a warn and continue (same defensive shape as today's race-loss bail on Delete affected-rows = 0).
4. Carve-out paths must NOT change:
   - `runtime/abandon_claim.go::abandonOpenedClaim` (used by `runner_lifecycle.go::abandonPartialLocks` for the pre-dispatch OnAcquireUnavailable pass/error path, and by `runner_acquire.go::handleOrphanedClaim` for the verify-before-run bail path) — these rows never went through Promote and never had `state` flipped; they're `Delete`'d directly as today.

**Verification:**

```
go build ./... && go test ./runtime/...
```

Tests that asserted "row deleted after terminal" will fail. That's expected; fix them in Task 1.3.2.

#### Task 1.3.2 — Update scenario + runtime tests for row-preservation

**Files:** every test that asserted "row gone after terminal" on a non-carve-out path.

**Steps:**

1. Run `make test-all` and capture every failing test. Most will be of the shape `assert: row exists` post-terminal that now succeeds (because Promote preserved the row), or `assert: row gone` that now fails.
2. For each failing assertion, flip the assertion: instead of `assert: row gone after terminal`, assert `row.State == 'committed'` (or `'abandoned'`) AND `row.ResolvedAt != nil`. For tests that need to assert the post-retention-sweep state, advance the test clock past the retention cutoff and call the sweep explicitly, then assert row gone.
3. Tests under: `runtime/terminal_decision_test.go`, `runtime/auto_terminal_test.go`, `runtime/instance_termination_test.go`, `test/scenarios/asset/`, `test/scenarios/lineage/`, `test/scenarios/run_tree/`, `test/scenarios/forensics/`.

**Verification:**

```
make test-all
```

All clean (except for the new `SweepClaimHandleRetention` tests written in Task 1.3.4 — those land alongside in the same stage; before they exist, scenario tests using the sweep should be skipped or marked TODO referencing Task 1.3.4).

#### Task 1.3.3 — Add `RetentionConfig.ClaimHandlesTrailing` field

**Files:** `runtime/retention_sweeps.go`, `control/config/config.go` (or wherever the YAML `retention:` block is parsed — check for `RetentionConfig` definition site).

**Steps:**

1. Add `ClaimHandlesTrailing time.Duration` to `RetentionConfig` with a default of `30 * 24 * time.Hour` (30 days).
2. Update the YAML parser to read the `claim_handles_trailing` key (parsing `time.Duration` strings like `"30d"`; check existing `lineage_trailing` parser for the precedent — likely uses `time.ParseDuration` with the existing extension that handles `d` suffix, or has a custom yaml-tag).
3. Surface the default in the reference `deploy/rimsky.yml` if a `retention:` block exists there.

**Verification:**

```
go build ./... && go test ./runtime/... -run TestRetentionConfig
go test ./control/...
```

#### Task 1.3.4 — Implement `SweepClaimHandleRetention`

**Files:** `runtime/sweep_claim_handle_retention.go` (new), `runtime/sweep_claim_handle_retention_test.go` (new).

**Steps:**

1. Write the sweep:

   ```go
   package runtime

   import (
       "context"
       "fmt"
       "time"

       "github.com/rimsky-ai/rimsky-core/foundation/persistence"
   )

   // SweepClaimHandleRetention deletes terminal claim_handle rows that have
   // outlived the retention window. Modeled on SweepLineageRetention's
   // time-based cutoff pattern. Serialization: runs under the scheduler-tick
   // advisory lock; no per-row claimant guard is required (the rows being
   // swept have holder_supervisor_id IS NULL by construction per Stage 1's
   // second CHECK constraint).
   //
   // Never sweeps durable-Commit rows (state='committed' AND lifetime='durable')
   // — those are the asset surface and are released only via instance
   // termination or operator DELETE /assets/{alias}.
   //
   // @blessed-invariant 4 (post-refactor): non-active-row deletions are
   // claimant-guarded by absence + the scheduler-tick advisory lock.
   func SweepClaimHandleRetention(ctx context.Context, db persistence.Database,
       cutoff time.Duration) (deleted int64, err error) {
       const q = `
           DELETE FROM rimsky_claim_handles
           WHERE state IN ('committed', 'abandoned')
             AND (state = 'abandoned' OR lifetime = 'subgraph')
             AND resolved_at + $1 < now()
             AND holder_supervisor_id IS NULL
       `
       cmd, err := db.Pool().Exec(ctx, q, cutoff)
       if err != nil {
           return 0, fmt.Errorf("sweep claim_handle retention: %w", err)
       }
       return cmd.RowsAffected(), nil
   }
   ```

   (Adjust the function signature + DB-access pattern to match existing retention sweeps in `runtime/retention_sweeps.go` — use the same `*pgx.Pool` access shape, the same context handling, and the same return-type convention.)

2. SQLite variant: the same SQL works under modernc.org/sqlite (no `now()` — use `CURRENT_TIMESTAMP` and adjust the date arithmetic). Likely the existing `SweepLineageRetention` already handles the driver dispatch; mirror its pattern.

3. Write the test file:

   ```go
   package runtime

   import (
       "context"
       "testing"
       "time"

       "github.com/rimsky-ai/rimsky-core/internal/pgtest"
       …
   )

   func TestSweepClaimHandleRetention_DoesNotSweepDurableCommitted(t *testing.T) {
       // Insert a state='committed', lifetime='durable' row with resolved_at
       // far in the past. Run the sweep. Assert the row is still present.
       …
   }

   func TestSweepClaimHandleRetention_SweepsSubgraphCommittedPastCutoff(t *testing.T) {
       // Insert a state='committed', lifetime='subgraph' row with resolved_at
       // older than cutoff. Run the sweep. Assert the row is gone.
       …
   }

   func TestSweepClaimHandleRetention_SweepsAbandonedPastCutoff(t *testing.T) {
       // Insert a state='abandoned' row (any lifetime) with resolved_at
       // older than cutoff. Run the sweep. Assert the row is gone.
       …
   }

   func TestSweepClaimHandleRetention_DoesNotSweepWithinCutoff(t *testing.T) {
       // Insert a state='committed', lifetime='subgraph' row with resolved_at
       // within cutoff. Run the sweep. Assert the row is still present.
       …
   }

   func TestSweepClaimHandleRetention_DoesNotSweepActive(t *testing.T) {
       // Defense in depth: active rows shouldn't be candidates regardless
       // of resolved_at (which is NULL on active rows anyway).
       …
   }
   ```

   Use the existing `pgtest` fixture pattern for Postgres-backed tests.

**Verification:**

```
go build ./... && go test ./runtime/... -run TestSweepClaimHandleRetention
```

All five test cases pass.

#### Task 1.3.5 — Wire `SweepClaimHandleRetention` into the scheduler tick

**Files:** `graph/scheduler/scheduler.go` (likely; verify in discovery subtask) and possibly `runtime/retention_sweeps.go`.

**Important context:** As of this plan's writing, neither `SweepLineageRetention` nor `SweepRunTreeRetention` has any caller — both are defined-but-unused in `runtime/retention_sweeps.go`. The spec's framing "runs on the same scheduler-tick cadence as the existing retention sweeps" assumed a tick caller already existed; it does not. This task therefore performs **first-time wiring** for `SweepClaimHandleRetention` only. The other two sweeps stay unwired (out of scope for this spec — wiring them is a separate decision).

**Steps:**

1. **Discovery subtask.** Identify the scheduler-tick orchestrator. Likely candidates:
   - `graph/scheduler/scheduler.go` — the main tick body. Look for the tick function (typically named `Tick`, `RunTick`, or similar) that fires under the `pg_try_advisory_lock(SCHEDULER_TICK_KEY)` guard.
   - `graph/scheduler/pure_cascade.go` — possible alternate orchestration site.
   Run `grep -rn "pg_try_advisory_lock\|SCHEDULER_TICK_KEY" graph/ runtime/` to locate the tick boundary.
   Record the file + function name in the plan-notes file.
2. **Wire the new sweep.** At the identified tick site, add a call to `SweepClaimHandleRetention(ctx, db, cfg.ClaimHandlesTrailing)`. If the tick already has a place for retention/cleanup steps, the new call goes there; if not, add a small `runRetentionSweeps(ctx, db, cfg)` helper local to the tick orchestrator that calls only the new sweep (do NOT add wiring for `SweepLineageRetention` or `SweepRunTreeRetention` — they remain unwired, per the spec scope).
3. Log the affected-rows count at slog Info level with structured fields: `slog.Info("claim_handle_retention_sweep", "deleted", n, "cutoff", cfg.ClaimHandlesTrailing.String())`.
4. Add a small integration check or scenario test verifying that a tick run actually invokes the sweep — at minimum, exercise the path via a scenario or runtime test that asserts a terminal claim-handle row past its retention cutoff is deleted after one tick.

**Verification:**

```
go build ./... && go test ./...
make lint
```

All clean. The discovery subtask's output (filename + function name) is recorded in the plan-notes file.

#### Task 1.3.6 — Stage 3 quiescence: full test pass + race

**Files:** none (verification).

**Steps:**

1. `make build-all && make test-all && make lint`.
2. Race test the critical paths:
   ```
   go test ./foundation/persistence/postgres/... ./runtime/... -race -count=3
   ```

**Verification:**

All clean. Working tree now has the row-preservation contract live: terminal Promote, then retention sweep at cutoff.

### Stage 4 — Drop the bool

#### Task 1.4.1 — Postgres migration: DROP COLUMN held_durable; refresh indexes

**Files:** `foundation/persistence/postgres/migrations/<N+1>-claim-handles-drop-held-durable.sql` (new). `<N+1>` is one past Task 1.1.1's chosen number.

**Pre-discovery (named indexes confirmed against the current schema):** the existing relevant indexes on `rimsky_claim_handles` are:
- `idx_rimsky_claim_handles_supervisor ON (holder_supervisor_id)` — baseline (migration 001).
- `idx_rimsky_claim_handles_node ON (holder_node_id)` — baseline (migration 001).
- `idx_rimsky_claim_handles_scope ON (producer_name) WHERE lock_kind = 'scope'` — baseline (migration 001); this is the scope-conflict-supporting index. It does NOT carry a state-column filter today (it filters by `lock_kind` only); post-refactor it continues to work for both active and committed rows because the conflict-query's state-column WHERE clause filters at query time, not at index lookup time.
- `idx_rimsky_claim_handles_held_durable ON (held_durable)` — migration 002; this is the index this stage drops.

The plan-notes file should record this index inventory. If migration 001 / 002 have evolved (e.g., the cycles 8–10 cleanup loop renamed an index), re-run `grep -rn "CREATE.*INDEX.*claim_handles" foundation/persistence/postgres/migrations/` and reconcile.

**Steps:**

1. Write the migration:

   ```sql
   -- Stage 4: drop held_durable; the state column has replaced it.

   ALTER TABLE rimsky_claim_handles DROP COLUMN held_durable;

   -- Drop the held-durable index added in migration 002.
   DROP INDEX IF EXISTS idx_rimsky_claim_handles_held_durable;
   -- Also tolerate the alternate name in case earlier cleanup cycles renamed it:
   DROP INDEX IF EXISTS rimsky_claim_handles_held_durable_idx;

   -- New state-based partial indexes:

   -- Supports active-row lookups by supervisor (orphan reaper, heartbeat extend).
   -- Replaces the baseline idx_rimsky_claim_handles_supervisor for active rows;
   -- the baseline index can stay (it covers all states) or be dropped if we
   -- want to keep the per-state index alone. Keep both for safety.
   CREATE INDEX rimsky_claim_handles_active_idx
       ON rimsky_claim_handles (holder_supervisor_id) WHERE state = 'active';

   -- Supports the asset query: ListByInstanceAndState(instance, committed, durable)
   -- joins through holder_node_id → rimsky_nodes.id to filter by instance.
   CREATE INDEX rimsky_claim_handles_committed_durable_idx
       ON rimsky_claim_handles (holder_node_id) WHERE state = 'committed' AND lifetime = 'durable';

   -- Note: the scope-conflict-supporting index (idx_rimsky_claim_handles_scope,
   -- created in migration 001) does NOT need a state-column WHERE-clause update.
   -- It filters by `lock_kind = 'scope'` only; the state-column filter applies
   -- at query time in ListByProducerScope's WHERE state IN ('active','committed')
   -- — that's a filter the planner applies after the index seek, not an
   -- index-predicate concern. The index continues to work for both active and
   -- committed rows post-refactor.
   ```

2. Inspect `foundation/persistence/postgres/migrations/002-data-platform-extensions.sql` to confirm the held-durable index's exact name; the DROP statement above tolerates both common spellings.

**Verification:**

```
go build ./...
```

#### Task 1.4.1b — Add scope-conflict regression test for committed rows

**Files:** `foundation/persistence/postgres/claim_handles_test.go` (or `runtime/runner_acquire_test.go` — pick whichever package owns the scope-conflict acquisition test fixture; check existing `TestListByProducerScope` or `TestAcquireScopeConflict` callers).

**Steps:**

1. Add a test that exercises the load-bearing property the spec calls out: a committed-durable row continues to trip byte-equal scope conflict at acquire-time, even though the original acquirer has terminated and the row has been promoted to `state = 'committed'`.

   ```go
   func TestScopeConflict_CommittedDurableStillConflicts(t *testing.T) {
       ctx := context.Background()
       pg := pgtest.Postgres(t)

       // Acquire claim A on scope S with lifetime: durable.
       acquireA := insertActiveClaim(t, pg, "producer-x", scopeBytes, spec.ClaimLifetimeDurable, supervisorID_A)

       // Promote A to committed (simulating durable-Commit at terminal).
       promoteToCommitted(t, pg, acquireA, supervisorID_A)

       // Verify the row is now state='committed', holder_supervisor_id IS NULL.
       row := getClaimHandle(t, pg, acquireA)
       require.Equal(t, spec.ClaimHandleStateCommitted, row.State)
       require.Nil(t, row.HolderSupervisorID)  // post-Promote nulling

       // Attempt to acquire claim B on the same byte-equal scope from a
       // different supervisor. This must conflict (the committed-durable
       // row is the asset surface; another acquire would corrupt the
       // single-writer-per-scope invariant).
       conflicting, err := tryAcquireClaim(t, pg, "producer-x", scopeBytes,
           spec.ClaimLifetimeDurable, supervisorID_B)
       require.Error(t, err)
       require.True(t, isScopeConflictError(err),
           "expected scope-conflict error, got %v", err)
       require.Nil(t, conflicting)
   }
   ```

   (Pseudocode; bind to the actual test fixture API the existing tests use. The exact helper names — `insertActiveClaim`, `promoteToCommitted`, `tryAcquireClaim`, `isScopeConflictError` — should reuse existing test helpers if available; otherwise create thin wrappers in a `_test.go` helper.)

**Verification:**

```
go test ./foundation/persistence/postgres/... -run TestScopeConflict_CommittedDurableStillConflicts -v
```

Test passes; the conflict property is structurally pinned.

#### Task 1.4.2 — SQLite migration: mirror the column drop + index changes

**Files:** `foundation/persistence/sqlite/migrations/<N+1>-claim-handles-drop-held-durable.sql` (new).

**Steps:**

1. Mirror Task 1.4.1 against the SQLite driver. SQLite's `ALTER TABLE DROP COLUMN` works in 3.35+ (modernc.org/sqlite supports this); if the table-recreate pattern is needed instead, use it as Task 1.1.2 may have established.
2. Partial indexes in SQLite use the same `WHERE` clause syntax.

**Verification:**

```
go build ./...
```

#### Task 1.4.3 — Remove `HeldDurable` field from `ClaimHandleRow`; remove deprecated methods

**Files:** `foundation/persistence/claim_handles.go`, `foundation/persistence/postgres/claim_handles.go`, `foundation/persistence/sqlite/claim_handles.go`.

**Steps:**

1. From `claim_handles.go` (interface package): delete the `HeldDurable bool` field from `ClaimHandleRow`. Delete the `SetHeldDurable` interface method. Delete the `ListHeldDurableByInstance` interface method.
2. From both drivers: delete the `SetHeldDurable` and `ListHeldDurableByInstance` implementations. Update the column list used in `SELECT … FROM rimsky_claim_handles` queries to drop `held_durable`. Update row-scan helpers to drop the `HeldDurable` populate.
3. Any caller that still references `row.HeldDurable` or the removed methods will fail to build — fix each by routing through `row.State == ClaimHandleStateCommitted` or `ListByInstanceAndState`.

**Verification:**

```
make build-all
```

Build clean. No references to `HeldDurable` or `SetHeldDurable` or `ListHeldDurableByInstance` remain (`grep -rln HeldDurable .` returns no Go-source matches; same for `SetHeldDurable` and `ListHeldDurableByInstance`).

#### Task 1.4.4 — Drop SetHeldDurable's dual-write scaffold

**Files:** `foundation/persistence/postgres/claim_handles.go`, `foundation/persistence/sqlite/claim_handles.go`.

**Steps:**

1. The previous task removed the `SetHeldDurable` implementation. Verify no scaffold remains (e.g., commented-out dual-write CASE expressions). If any vestigial code remains, delete it.

**Verification:**

```
grep -rn "held_durable" foundation/persistence/
```

No Go-side references remain (only migration files mention `held_durable`, in the `DROP COLUMN` migration body).

#### Task 1.4.5 — Stage 4 quiescence: full test pass + race

**Files:** none (verification).

**Steps:**

1. `make build-all && make test-all && make lint`.
2. `go test ./foundation/persistence/postgres/... ./runtime/... -race -count=3`.

**Verification:**

All clean. Working tree now has the column dropped, the dual-write scaffold gone, and only the state column live.

### Stage 5 — Concept catalog + invariants + docs

#### Task 1.5.1 — Update `concepts/claim-handle.md`

**Files:** `.ok-planner/design/concepts/claim-handle.md`.

**Steps:**

1. Read the file. Find every reference to `held_durable` and `is_held` (and the related sentences about `held_durable = TRUE`).
2. Replace `held_durable` with `state` references:
   - "Post-2026-05-15 the row also carries: … `held_durable BOOLEAN NOT NULL DEFAULT FALSE`" — rewrite this bullet to instead describe `state TEXT NOT NULL CHECK (state IN ('active','committed','abandoned'))` and `resolved_at TIMESTAMPTZ NULL`, with the 3-state model documented (active / committed / abandoned).
   - In the Held-variant subsection: "marks a row that survived auto-terminal Commit on a `lifetime: durable` claim" — refresh to "promoted to `state = 'committed'` by auto-terminal on a `lifetime: durable` claim".
   - In the Invariants subsection: "The orphan-claim reaper skips `held_durable = true` rows" — refresh to "The orphan-claim reaper skips non-`active` rows (its predicate is `state = 'active' AND expires_at < now()`)."
3. Add a new subsection or expand the Definition to describe state transitions (active → committed | abandoned at Promote; deleted by retention sweep for swept categories; deleted by Release path for durable-committed) and the two guard shapes for invariant 4 (per-row claimant guard on active; absence-guard for non-active sweeps and Release-path deletes).
4. Add a Notes entry at the bottom citing the spec slug (`spec:2026-05-17-post-data-platform-cleanup`).

**Verification:**

```
grep -n "held_durable" .ok-planner/design/concepts/claim-handle.md
```

Returns 0 lines (or only lines that explicitly describe the pre-refactor history in a clearly-historical context).

#### Task 1.5.2 — Update `concepts/claim-lifetime.md`

**Files:** `.ok-planner/design/concepts/claim-lifetime.md`.

**Steps:**

1. Read the file. Find references to "flips `held_durable: true`" and "auto-terminal Commit on a `lifetime: durable` claim flips `held_durable = true` instead of deleting the row."
2. Replace: "auto-terminal Commit on a `lifetime: durable` claim promotes the row to `state = 'committed'` (not deleted); Release later deletes the row outright via the existing `Delete` (no intermediate state — the 3-state model has no `released` state)."
3. Update the Annotation sites: replace `SetHeldDurable` with `Promote`; update line references if needed.
4. Add a Notes entry citing the spec slug.

**Verification:**

```
grep -n "held_durable" .ok-planner/design/concepts/claim-lifetime.md
```

Returns 0 lines.

#### Task 1.5.3 — Update `concepts/asset.md`

**Files:** `.ok-planner/design/concepts/asset.md`.

**Steps:**

1. Read the file. Find "The asset presentation surface is a query alias over `rimsky_claim_handles` filtered by `held_durable = TRUE`".
2. Replace: "filtered by `state = 'committed' AND lifetime = 'durable'`".
3. Add a Notes entry citing the spec slug.

**Verification:**

```
grep -n "held_durable" .ok-planner/design/concepts/asset.md
```

Returns 0 lines.

#### Task 1.5.4 — Update `concepts/claim-tree.md`

**Files:** `.ok-planner/design/concepts/claim-tree.md`.

**Steps:**

1. Read the file. Find the invariant: "For `lifetime: durable` children, `SetHeldDurable(true)` keeps the row alive past terminal; the row participates in the parent's aggregation counter but is skipped by the descendant-cancel walker (durable-Commit contract — don't undo a successful promotion)."
2. Replace `SetHeldDurable(true)` with `Promote(committed)`; the skip-filter expression changes from `held_durable = true` to `state != 'active'`.
3. Add a Notes entry citing the spec slug.

**Verification:**

```
grep -n "held_durable\|SetHeldDurable" .ok-planner/design/concepts/claim-tree.md
```

Returns 0 lines (or only historical-context lines).

#### Task 1.5.5 — Update `concepts/cancel-siblings.md`

**Files:** `.ok-planner/design/concepts/cancel-siblings.md`.

**Steps:**

1. Read the file. Find the invariant "The cancel walker SKIPS three classes of sibling rows: (a) held-durable rows (`held_durable = TRUE`)…".
2. Replace `(a) held-durable rows (held_durable = TRUE)` with `(a) non-active rows (state != 'active' — committed-durable rows preserve the durable-Commit contract; committed-subgraph and abandoned rows aren't candidates for cancellation)`.
3. Find any other reference to `held_durable` and refresh.
4. Add a Notes entry citing the spec slug.

**Verification:**

```
grep -n "held_durable" .ok-planner/design/concepts/cancel-siblings.md
```

Returns 0 lines.

#### Task 1.5.6 — Update `concepts/auto-terminal.md`

**Files:** `.ok-planner/design/concepts/auto-terminal.md`.

**Steps:**

1. Read the file. Find "deletes the handle claimant-guarded" in the Definition section.
2. Replace: "promotes the handle to `state = 'committed'` (or `'abandoned'`) via `Promote`, claimant-guarded against the supervisor that held it. Carve-out paths (`abandonOpenedClaim`) continue to `Delete` directly; those rows never went through `Promote`."
3. Find the Invariants subsection. Update the "Delete of the rimsky_claim_handles row is claimant-guarded" bullet to refer to the post-refactor two-guard-shape model (per-row claimant guard on `Promote`; absence-guard on retention sweep + Release path).
4. Add a Notes entry citing the spec slug.

**Verification:**

```
grep -n "Delete" .ok-planner/design/concepts/auto-terminal.md
```

Returns only lines that describe the carve-out Delete paths or the historical Delete-at-terminal note; no stale "deletes the handle" claims for the main path.

#### Task 1.5.7 — Update `concepts/orphan-reaper.md`

**Files:** `.ok-planner/design/concepts/orphan-reaper.md`.

**Steps:**

1. Read the file. Find references to "skips `held_durable = TRUE` rows" or similar.
2. Replace: "skips non-`active` rows (the predicate is `WHERE state = 'active' AND expires_at < now()`); the held-durable preservation property now flows from the state-column structure rather than a bool check."
3. Add a Notes entry citing the spec slug.

**Verification:**

```
grep -n "held_durable" .ok-planner/design/concepts/orphan-reaper.md
```

Returns 0 lines.

#### Task 1.5.8 — Update CLAUDE.md `@blessed-invariant 4` text

**Files:** `CLAUDE.md`.

**Steps:**

1. Find the `@blessed-invariant 4` block. Today's text: "Every `DELETE FROM rimsky_claim_handles` and every `UPDATE rimsky_node_runs SET claimed_by = NULL` is `AND … = supervisor_id`. Stale orphan sweeps cannot null or delete live ownership."
2. Rewrite to enumerate the two guard shapes post-refactor:

   ```
   4. **Claimant-guarded release.** Two guard shapes:
      - Active-row mutations on `rimsky_claim_handles` and `rimsky_node_runs`
        (Promote, ExtendHeartbeat, terminal Delete in the abandonOpenedClaim
        carve-outs, `UPDATE rimsky_node_runs SET claimed_by = NULL`) carry
        `AND … = supervisor_id`. Stale orphan sweeps cannot null or delete
        live ownership.
      - Non-active-row deletions on `rimsky_claim_handles` (retention sweep;
        Release-path Delete in instance termination + operator DELETE
        /assets/{alias}) are guarded by (a) the second CHECK constraint
        (`state != 'active'` rows have `holder_supervisor_id IS NULL` by
        construction); (b) the scheduler-tick advisory lock serializing
        the retention sweep across replicas; (c) the row-discovery query
        filter for Release-path Delete (`state = 'committed' AND lifetime
        = 'durable'`). No per-row claimant guard is required because the
        rows have no holder.
      (`foundation/persistence/postgres/queue.go`, `foundation/persistence/postgres/claim_handles.go`,
      `runtime/runner_acquire.go`, `runtime/orphan_reaper.go`,
      `runtime/sweep_claim_handle_retention.go`)
   ```

**Verification:**

```
grep -A 15 "4\. \*\*Claimant-guarded release" CLAUDE.md
```

Shows the refreshed text.

#### Task 1.5.9 — Update CLAUDE.md `@blessed-invariant 22` text

**Files:** `CLAUDE.md`.

**Steps:**

1. Find the `@blessed-invariant 22` block. Today's text references `held_durable = true`.
2. Refresh to use `state = 'committed' AND lifetime = 'durable'`. Same substantive behavior; only the column reference changes.

**Verification:**

```
grep -A 8 "22\. \*\*Held-durable" CLAUDE.md
```

Shows the refreshed text with `state = 'committed'` rather than `held_durable = true`.

#### Task 1.5.10 — Update CLAUDE.md gotchas: held-durable wording

**Files:** `CLAUDE.md`.

**Steps:**

1. Find the gotcha bullet "**Held-durable claim handles persist past holding-subgraph completion**" or similar.
2. Refresh the text to refer to `state = 'committed' AND lifetime = 'durable'` instead of `held_durable = TRUE`.
3. Add a new gotcha bullet for the retention sweep:

   ```
   - **Terminal claim_handle rows live for `retention.claim_handles_trailing`
     (default 30d) before retention sweep deletes them.** Durable-committed
     rows are the exception — they're never swept; they go away only via
     producer `Release` (instance termination or operator
     `DELETE /assets/{alias}`). Forensics queries can hit `state = 'abandoned'`
     and `state = 'committed' AND lifetime = 'subgraph'` rows directly on
     `rimsky_claim_handles` within the retention window; older history lives
     only in `rimsky_lineage`.
   ```

**Verification:**

```
grep -n "held_durable" CLAUDE.md
```

Returns 0 lines (or only historical-narrative lines describing the pre-refactor model in clearly-historical context).

#### Task 1.5.11 — Update CHANGELOG.md Unreleased section

**Files:** `CHANGELOG.md`.

**Steps:**

1. Add Unreleased-section bullets:

   ```
   - **Claim-handle state-column refactor.** `rimsky_claim_handles.held_durable bool`
     replaced with `state TEXT` enum (`active`, `committed`, `abandoned`) and
     `resolved_at TIMESTAMPTZ`. Terminal Promote preserves the row past
     holding-subgraph completion; new `SweepClaimHandleRetention` reaps
     terminal rows past `retention.claim_handles_trailing` (default 30d).
     Durable-committed rows never swept (asset surface); deleted only by
     producer Release. `@blessed-invariant 4` text updated to enumerate the
     two guard shapes; `@blessed-invariant 22` text updated to refer to
     `state = 'committed' AND lifetime = 'durable'`. Migrations: ADD COLUMN
     state + resolved_at + CHECKs (Stage 1); DROP COLUMN held_durable +
     index refresh (Stage 4).
   ```

**Verification:**

```
git diff CHANGELOG.md
```

Shows the bullet.

#### Task 1.5.12 — Stage 5 quiescence: full test pass

**Files:** none (verification).

**Steps:**

1. `make build-all && make test-all && make lint`.

**Verification:**

All clean. Item 1 complete. Working tree has: state column live; held_durable column gone; retention sweep wired in; concept catalog refreshed; CLAUDE.md invariants + gotchas refreshed; CHANGELOG bullet present.

---

## Item 4 — Cold-read paydown

### Tier 1 — concrete splits

#### Task 4.1.1 — Measure files before splitting

**Files:** none (measurement only).

**Steps:**

1. `wc -l runtime/runner_acquire.go runtime/terminal_decision.go runtime/subgraph_dispatch.go runtime/auto_terminal.go`.
2. Record the line counts in the plan-notes file. These are the pre-split baseline.

**Verification:**

Plan-notes file contains the four line counts.

#### Task 4.1.2 — Split `runner_acquire.go` into four files

**Files:** `runtime/runner_acquire.go` (modified — becomes orchestration shell), `runtime/runner_acquire_named_locks.go` (new), `runtime/runner_acquire_claims.go` (new), `runtime/runner_acquire_holders.go` (new).

**Steps:**

1. Read `runtime/runner_acquire.go` in full. Identify the function groupings per the spec:
   - **Named locks** group: `takeNamedAdvisoryLocks`, `acquireOneLock`, `acquireNamedLock`.
   - **Claims** group: `acquireClaim`, `evaluateScopeConflict`.
   - **Holders** group: `insertHeldClaimHoldersAtAcquire`, `insertCoHolderClaimHoldersAtAcquire`, `findHoldingSubgraphForAcquirer`.
   - **Orchestration** (stays in `runner_acquire.go`): `tryAcquire` and any other top-level entry / helper that doesn't fit the above groups.
2. Move each group of functions to its new file. Each new file is `package runtime` and imports whatever the original file imported (run goimports after to clean up).
3. Preserve every `@source`, `@agent-contract`, `@blessed-invariant`, `@concept:` annotation on its function. If an annotation was on a moved function, it moves with the function. If a load-bearing site loses an annotation in the move, that's a bug — re-add it.
4. Test files (`runner_acquire_test.go`) stay where they are; they reference functions by exported name, not by file.

**Verification:**

```
wc -l runtime/runner_acquire*.go
```

All four files under 500 lines.

```
make build-all && make test-all
```

All clean.

#### Task 4.1.3 — Extract helpers from `terminal_decision.go::ResolveClaimHandleTerminal` in-file

**Files:** `runtime/terminal_decision.go` (modified; no new file).

**Steps:**

1. Read `runtime/terminal_decision.go::ResolveClaimHandleTerminal` in full (~190 lines pre-refactor; may have grown post-Item-1's Promote landing).
2. Identify the 7 distinct operations per the spec:
   - `dispatchDataProcessingTerminal` — DataProcessing-aware Commit branch (CommitCandidate vs Commit).
   - `fireProducerVerb` — the verb dispatch (Commit / Abandon) with retry/error handling.
   - `writeTerminalLineage` — lineage-row write (`record_kind = 'claim_terminal'`).
   - `promoteOrDelete` — the state-column write (`Promote(committed | abandoned)`; or `Delete` in the carve-out paths).
   - `bumpParentCounter` — parent aggregation-counter update.
   - `recurseSiblingCancel` — strict.cancel_siblings descent.
   - `recurseParentChain` — `resolveParentClaimChain` invocation.
3. Extract each operation to a helper function in the same file. The top-level `ResolveClaimHandleTerminal` shrinks to the workflow dispatch into these helpers.
4. Preserve every annotation.

**Verification:**

```
wc -l runtime/terminal_decision.go
```

File under 500 lines; `ResolveClaimHandleTerminal` body under 100 lines.

```
make build-all && make test-all
```

All clean.

### Tier 2 — re-measure

#### Task 4.2.1 — Re-measure `subgraph_dispatch.go` and `auto_terminal.go`

**Files:** none (measurement only).

**Steps:**

1. `wc -l runtime/subgraph_dispatch.go runtime/auto_terminal.go`.
2. Compare against the 500-line guideline.

**Verification:**

Line counts captured.

#### Task 4.2.2 — Conditionally split `subgraph_dispatch.go`

**Files:** `runtime/subgraph_dispatch.go` and possibly new files.

**Steps:**

1. If Task 4.2.1 shows `subgraph_dispatch.go` under 500 lines: this task is a no-op. Record in plan-notes "subgraph_dispatch.go came in at <N> lines post-Item-1; under guideline; no split needed."
2. Otherwise: split per the spec's suggested natural concerns — dispatch-phase boundaries (entry-node absorption / exit-node carry / failure-cancel cascade). Read the file, identify the boundaries, move function groups out.
3. Preserve all annotations.

**Verification:**

```
wc -l runtime/subgraph_dispatch*.go
make build-all && make test-all
```

Each file under 500 lines (if split); all tests clean.

#### Task 4.2.3 — Conditionally split `auto_terminal.go`

**Files:** `runtime/auto_terminal.go` and possibly new files.

**Steps:**

1. If Task 4.2.1 shows `auto_terminal.go` under 500 lines: no-op.
2. Otherwise: split per the spec's suggested concerns — `auto_terminal_check.go` (resolution check + lock acquisition) + `auto_terminal_chain.go` (parent-chain recursion) + `auto_terminal_aggregator.go` (holder-state aggregation).
3. Preserve all annotations.

**Verification:**

```
wc -l runtime/auto_terminal*.go
make build-all && make test-all
```

All clean.

#### Task 4.2.4 — Update `feature-index.md` if file layout changed

**Files:** `feature-index.md` (if it exists; create if doesn't).

**Steps:**

1. If `feature-index.md` doesn't exist at the repo root, create it per the project's cold-read convention (`.claude/rules/cold-read-cheatsheet.md`).
2. Update the entries for the touched files to reflect the new layout (new files added, file responsibilities re-described).

**Verification:**

```
git diff feature-index.md
```

Shows the touched-file entries are current.

#### Task 4.2.5 — CHANGELOG entry for Item 4

**Files:** `CHANGELOG.md`.

**Steps:**

1. Append Unreleased-section bullet:

   ```
   - **Cold-read paydown.** `runtime/runner_acquire.go` split into named-locks /
     claims / holders / orchestration files; `terminal_decision.go::ResolveClaimHandleTerminal`
     refactored into orchestration shell + 7 helpers. <If Tier 2 splits happened:
     subgraph_dispatch.go and/or auto_terminal.go also split.> All annotations
     preserved; no behavior change.
   ```

**Verification:**

```
git diff CHANGELOG.md
```

Shows the bullet.

---

## Item 5 — Multi-replica `sensor-cron` test fixture

### Task 5.1 — Locate the `sensor-cron` advisory-lock implementation

**Files:** none modified (orientation).

**Steps:**

1. Read `sensors/sensor-cron/` to find the advisory-lock acquisition site (`pg_try_advisory_lock(SENSOR_CRON_KEY)` per `CLAUDE.md`'s gotcha).
2. Identify the SENSOR_CRON_KEY constant and the watch-firing path.
3. Note the existing test fixture(s) under `sensors/sensor-cron/` for the project's conventions.

**Verification:**

Plan-notes file contains a one-line summary of the advisory-lock site and the watch-firing entry point.

### Task 5.2 — Write `multi_replica_test.go`

**Files:** `sensors/sensor-cron/multi_replica_test.go` (new).

**Steps:**

1. Write a scenario test with this structure (skeleton; concrete API calls follow existing `sensor-cron` test patterns):

   ```go
   package sensorcron_test

   import (
       "context"
       "sync"
       "testing"
       "time"

       "github.com/rimsky-ai/rimsky-core/internal/pgtest"
       …
   )

   // TestMultiReplica_OnlyOneReplicaFiresPerWindow verifies that two
   // sensor-cron replicas sharing the same Postgres + same watch set
   // serialize firing via pg_try_advisory_lock — exactly one replica
   // fires the cron action per window; the other backs off cleanly.
   func TestMultiReplica_OnlyOneReplicaFiresPerWindow(t *testing.T) {
       ctx := context.Background()
       pg := pgtest.Postgres(t)

       // Set up a shared watch in the rimsky_sensor_watches table targeting
       // a test cron schedule that's about to fire.
       watchID := setupSharedWatch(t, pg)

       // Spin up two sensor-cron instances against the same Postgres.
       replicaA := startSensorCronReplica(t, pg)
       replicaB := startSensorCronReplica(t, pg)
       defer replicaA.Stop()
       defer replicaB.Stop()

       // Wait for the fire window. Both replicas should attempt; only one
       // should hold the advisory lock and fire.
       observations := collectObservations(t, pg, 5*time.Second)

       if got, want := len(observations), 1; got != want {
           t.Fatalf("expected %d observation, got %d (%+v)", want, got, observations)
       }
   }

   // TestMultiReplica_OtherReplicaPicksUpAfterHolderDies verifies that
   // killing the lock-holding replica mid-window lets the other replica
   // pick up at the next tick.
   func TestMultiReplica_OtherReplicaPicksUpAfterHolderDies(t *testing.T) {
       …
   }
   ```

2. Use the existing pgtest fixture pattern; bound the test wall-clock to <30 seconds (it should complete in seconds, but the upper bound protects against fixture flakes).
3. The test must run cleanly under `go test -race`; use `sync.WaitGroup` or explicit channels for any goroutine coordination.

**Verification:**

```
go test ./sensors/sensor-cron/ -run TestMultiReplica -v
```

Both tests pass.

### Task 5.3 — Validate `multi_replica_test.go` under load

**Files:** none modified.

**Steps:**

1. Run the test 50 times in a row:
   ```
   go test ./sensors/sensor-cron/ -run TestMultiReplica -race -count=50 -v 2>&1 | tee /tmp/sensor_cron_validate.log
   ```
2. Confirm all 50 runs pass.

**Verification:**

50 PASS lines in `/tmp/sensor_cron_validate.log`; no FAIL.

If failures: the test fixture has its own flakiness. Tighten the synchronization (e.g., explicit signaling for "replica A holds the lock now") and re-validate. Per project rule, fix the test, don't relax the assertion.

### Task 5.4 — CHANGELOG entry for Item 5

**Files:** `CHANGELOG.md`.

**Steps:**

1. Append Unreleased-section bullet:

   ```
   - **Multi-replica `sensor-cron` test coverage.** New scenario test
     `sensors/sensor-cron/multi_replica_test.go` covers advisory-lock
     serialization across two replicas and fail-over when the lock holder
     dies mid-window.
   ```

**Verification:**

```
git diff CHANGELOG.md
```

Shows the bullet.

---

## Item 6 — Asset/lineage end-to-end coverage uniformity

### Phase 1 — classification dispatch

#### Task 6.1.1 — Enumerate test files in scope

**Files:** none modified.

**Steps:**

1. Run:
   ```
   ls test/scenarios/asset/*.go test/scenarios/lineage/*.go
   ```
2. Record the list of files in the plan-notes file. This is the in-scope set for the classification.

**Verification:**

Plan-notes file contains the file list.

#### Task 6.1.2 — Classify each test file

**Files:** plan-notes file modified.

**Steps:**

1. For each file in scope, read it and classify per test function:
   - **Shape-pinning** — verifies struct fields / SQL column round-trips without booting the harness (no scenario.Start, no testcontainers Postgres, no scheduler / supervisor / control-api boot).
   - **End-to-end** — boots Postgres + scheduler + supervisor + control-api + bundled producer/executor and drives a real cascade.
   - **Mixed** — does both in one test function.
2. For each shape-pinning or mixed test, recommend a disposition:
   - **Keep as-is** — unit-level coverage is appropriate (the property is genuinely shape-level; the cost of harness boot doesn't earn its keep).
   - **Upgrade to end-to-end** — the load-bearing property needs the full stack to verify (e.g., the property depends on the cascade interacting with the producer in real time, not just SQL round-trips).
   - **Split** — keep the unit-level pin (it's cheap signal) + add an end-to-end companion (the load-bearing property needs both signals).
3. Write the classification to the plan-notes file as a markdown table:

   ```
   | File | Test function | Current shape | Disposition | Rationale |
   |---|---|---|---|---|
   | test/scenarios/asset/foo_test.go | TestFoo_Bar | shape-pinning | upgrade | The cascade interaction is the load-bearing property; SQL round-trip alone misses … |
   | test/scenarios/asset/foo_test.go | TestFoo_Baz | end-to-end | keep-as-is | Already end-to-end. |
   ```

**Verification:**

Plan-notes file contains the classification table. Every test function in scope appears in the table.

### Phase 2 — execute the dispositions

#### Task 6.2.1 — Apply upgrades

**Files:** whatever the classification's `upgrade` rows name.

**Steps:**

1. For each row classified as **upgrade**: rewrite the test function as end-to-end. Boot the harness (use the existing `graph/scenario.Start` pattern that other end-to-end scenarios use). Drive the cascade. Assert at the same boundary the original test asserted, but through real driver output rather than struct round-trip.
2. Run the upgraded test in isolation 10x to confirm stability:
   ```
   go test ./test/scenarios/<dir>/ -run <UpgradedTestName> -count=10 -v
   ```

**Verification:**

Each upgraded test passes 10x in isolation. `make test-all` clean.

#### Task 6.2.2 — Apply splits (add end-to-end companions)

**Files:** whatever the classification's `split` rows name.

**Steps:**

1. For each row classified as **split**: keep the existing shape-pinning test as-is. Add an end-to-end companion test alongside, named with a `_EndToEnd` suffix to differentiate. The companion exercises the same property through the full stack.
2. Run each new companion 10x to confirm stability.

**Verification:**

All companion tests pass 10x. `make test-all` clean.

#### Task 6.2.3 — Keep-as-is rows: no action

**Files:** none modified.

**Steps:**

1. For each row classified as **keep-as-is**: no action. The unit-level coverage stays.

**Verification:**

N/A.

#### Task 6.2.4 — CHANGELOG entry for Item 6

**Files:** `CHANGELOG.md`.

**Steps:**

1. Append Unreleased-section bullet summarizing the audit findings + actions:

   ```
   - **Asset/lineage coverage uniformity.** Classified the ~20 test files under
     `test/scenarios/asset/` and `test/scenarios/lineage/` by harness use
     (shape-pinning / end-to-end / mixed). <Number> tests upgraded from
     shape-pinning to end-to-end; <number> tests gained an end-to-end
     companion; <number> tests kept as-is (shape-level coverage appropriate
     for the property). Classification matrix in the plan-notes file.
   ```

   Substitute the actual numbers from the Phase 2 work.

**Verification:**

```
git diff CHANGELOG.md
```

Shows the bullet.

---

## Final cross-cutting verification

### Task FINAL.1 — Full build + test + lint + race

**Files:** none (verification).

**Steps:**

1. Run the full discipline:
   ```
   make build-all
   make test-all
   make lint
   ```
2. Run race tests on the heavyweight paths:
   ```
   go test ./foundation/persistence/postgres/... ./runtime/... -race -count=3
   ```

**Verification:**

All four commands exit 0. Working tree is in a coherent state for review.

### Task FINAL.2 — Sanity grep for stale references

**Files:** none (verification).

**Steps:**

1. Confirm no Go-source references to removed identifiers remain:
   ```
   grep -rn "HeldDurable\|SetHeldDurable\|ListHeldDurableByInstance" --include='*.go' .
   ```
   Expected: zero matches (Stage 4 removed all three).
2. Confirm no documentation references to the pre-refactor `held_durable` column outside of clearly-historical context:
   ```
   grep -rln "held_durable" --include='*.md' .
   ```
   Inspect each match; refresh any stale text.

**Verification:**

No Go-source matches for the removed identifiers. Any markdown matches are intentional historical-context mentions.

### Task FINAL.3 — Notes file completeness check

**Files:** `.ok-planner/plans/2026-05-17-post-data-platform-cleanup-notes.md` (created and maintained by execute-plan).

**Steps:**

1. Confirm the notes file contains a completion line for every item: Item 2 (review cycles), Item 3 (flake classifications + fixes), Item 1 (Stage 1–5 quiescence summaries), Item 4 (Tier 1 splits + Tier 2 decisions), Item 5 (test added), Item 6 (classification matrix + actions).

**Verification:**

Notes file has completion lines for all six items.

---

## Manual checks after completion

None. All verification is expressible as Go test runs, build commands, lint runs, or grep checks. The plan executes end-to-end automatically; no human-in-the-loop steps are required during the run.

After the plan completes and the implementer has applied any post-run review-cleanup fixes, the user reviews the working tree and decides on commit strategy. Commits happen outside the plan, per project discipline (`file:.claude/rules/rules.md`: "we only commit after execution and review").
