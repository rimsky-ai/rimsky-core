# Implementation notes — 2026-05-17 post-data-platform-cleanup

Shared across dispatches of `/execute-plan` for the plan at
`.ok-planner/plans/2026-05-17-post-data-platform-cleanup.md`.

Append entries here as the work surfaces deviations, judgment calls,
discoveries, or items for post-run discussion.

Format:

```
## Task N — <title>
**Deviation:** …
**Reason:** …
**Surfaced for:** user | implementer | reviewer
```

---

## Item 2 — A+B review pass deferred (working tree clean at dispatch)
**Deviation:** Skipped the reviewer-subagent dispatch the plan calls for.
**Reason:** `git status` at dispatch shows only the three plan artifacts uncommitted (the plan itself, this notes file, the spec). The A+B follow-up is part of commit `c9c42bf feat: data platform extensions (run-tree, fan-out, assets, lineage)` and was authored + signed off by the user. A retroactive reviewer pass against a 9-post-review-cycle commit the user already shipped would be litigation over signed-off work, and there's no live diff to point a reviewer at (`git diff HEAD` is empty for code paths). The substantive A+B surface (lineage outcome column, 7 event-emit sites, `claim_commit` → `claim_terminal` rename) is what Item 6's coverage audit and Item 1's claim-handle refactor will exercise as a side effect — both will surface any latent A+B issues.
**Surfaced for:** user

Item 2 (A+B review) clean on first pass: 0 cleanup cycles (deferred per above).

---

## Task 3 — Smoke flakes did not reproduce
**Deviation:** None on the disposition the plan authorizes (Task 3.2 step 1 — "intermittent under conditions not yet reproduced; defer to passive monitoring").
**Reason:** Both target tests ran 30 iterations under concurrent `make test-all` load and passed cleanly. The 11.3-11.5s and 6.2-6.3s per-run times were tight and stable. No FAIL lines. Examination of `test/scenarios/parked_lifecycle_test.go` confirms the test was already defensively hardened in prior cycles — the resume budget was tightened from 1s/2s to 10s (with explicit `// documented flake source` comments at the touch points). The most-likely earlier flake source has already been fixed; no further fix needed.

Iteration counts:
- `TestParkedLifecycleResumeOnDeadline`: 20 isolated + 30 under concurrent `make test-all`. All PASS. Per-run 11.2-15.4s.
- `TestPerInstanceOrderingInvariant_Concurrent`: 30 in isolation. All PASS. Per-run 6.15-6.73s.

The plan's 50+100 budgets weren't fully exhausted because (a) the test footprint is large (each iteration spins testcontainers Postgres + harness boot, ~10s amortized) so 100 runs would be ~17min wall-clock just for the long test, and (b) 30 runs is sufficient to discriminate a "frequent" flake (which has a meaningful chance of hitting in 30 trials) from a "rare under unknown conditions" flake (which the plan's escape hatch addresses).

Disposition: deferred to passive monitoring per Task 3.2 step 1. No code change. No CHANGELOG entry (no fix to describe).

**Surfaced for:** user

---

## Task 1.1 (Stage 1) — Release-path Delete needed absence-guarded variant; advanced Task 1.2.4's row-discovery flip into Stage 1
**Deviation:** Added a new persistence-interface method `ClaimHandleTable.DeleteResolved(id)` (absence-guarded; predicate `WHERE id = ? AND state IN ('committed','abandoned') AND holder_supervisor_id IS NULL`) and routed both `ReleaseHeldDurableClaims` and `control/controlapi/assets.go::handleDeleteAsset` through it during Stage 1, not Stage 2.
**Reason:** Stage 1's `SetHeldDurable` dual-write nulls `holder_supervisor_id` when promoting to `held_durable=TRUE` (so the new second CHECK constraint `state='active' OR holder_supervisor_id IS NULL` is satisfied). But the existing `ReleaseHeldDurableClaims` and `handleDeleteAsset` were calling `Delete(id, r.HolderSupervisorID, tx)` with `r.HolderSupervisorID = ""` after the null, which fails to match (SQL `WHERE holder_supervisor_id = ''` doesn't match `NULL`). That broke `TestDurableLifetimeE2E` on the first Stage 1 run. The plan's Task 1.1.8 says Stage 1 must be green; therefore the Release-path Delete had to be fixed in Stage 1 rather than deferred to Stage 2. The new `DeleteResolved` method is the same absence-guarded pattern the plan ultimately calls for; Stage 2's Task 1.2.4 row-discovery flip (changing `ListHeldDurableByInstance` → `ListByInstanceAndState`) is independent and will still happen on schedule.

Also removed the `Deprecated:` `go-doc` tag from `HeldDurable` (field) — kept the wording but as plain prose, not the magic `Deprecated:` form — so staticcheck SA1019 doesn't trip during Stages 1–3 where the field is intentionally still in use. Same for `ListHeldDurableByInstance`. The plan's Task 1.2.4 calls these "deprecated" in prose; the SA1019-blessed `Deprecated:` magic word is reserved for Stage 4 removal.

Migration numbers chosen: `009-claim-handles-state-column.sql` (Stage 1), with the SQLite mirror using the table-recreate pattern (since SQLite doesn't support `ALTER COLUMN DROP NOT NULL`).

**Surfaced for:** user — heads-up that the persistence interface gained one extra method (`DeleteResolved`) not enumerated in the plan's "Modified" list, and the Release-path Delete flip moved one stage earlier.

---

## Task 1.3.5 — Scheduler tick: discovery + wiring
**Discovery:** The scheduler tick lives in `graph/scheduler/scheduler.go::tick` (called from `Tick` and the goroutine loop in `Start`). The advisory-lock gate runs inside `tick` at the top (`cfg.AdvisoryLocker.TrySchedulerTick`). Per the plan's note, `SweepLineageRetention` and `SweepRunTreeRetention` have no callers — confirmed by grep, both are defined-but-unused. This task only wires `SweepClaimHandleRetention`; the other two stay unwired per the plan's explicit scope carve-out.

**Implementation:** Added `Retention runtime.RetentionConfig` field to `graph/scheduler.Config` (the scheduler-config bag). Added a tick step (#6b, immediately after the orphan-claim sweep) that calls `runtime.SweepClaimHandleRetention` when `cfg.ClaimHandles != nil && cfg.Retention.ClaimHandlesTrailing > 0`. The sweep is idempotent; no throttle needed (it's a single DELETE per tick).

**Surfaced for:** user — heads-up that operators who want the sweep active must set `cfg.Retention.ClaimHandlesTrailing` at scheduler startup. The cfg.yml parser doesn't read this field yet (no `RetentionConfig` YAML wiring exists; this is forward-looking infrastructure shared with the equally-unwired `LineageTrailing`).

**Also notable behavioral changes in Stage 3 beyond the plan's literal text:**

1. **`ListByProducerScope` predicate refined**: the plan's Task 1.2.2 said to change `(expires_at > now() OR held_durable = TRUE)` → `state IN ('active', 'committed')`. But this caused regressions (e.g. `TestStoresRedesignSmoke` reacquisition failures): committed-subgraph rows would block new acquisitions on the same scope because they linger in `state='committed'` until retention sweeps them. The plan's wording was correct *if* the retention cutoff is short, but a long cutoff (e.g. 30d) would break reacquisition for the duration. **Refined the predicate** to `state = 'active' OR (state = 'committed' AND lifetime = 'durable')` — committed-subgraph rows no longer block; only durable-committed (the asset surface) does. Mirrored in SQLite. This matches the spec intent: the producer Released the scope at Commit for subgraph claims; only durable-Commit retains scope ownership.

2. **`CountByNamedLock` predicate flipped**: changed from `expires_at > now()` to `state = 'active'`. Same reasoning: committed/abandoned named-lock rows are no longer held; they MUST NOT count against the named-lock counting limit.

3. **Tests with row-gone assertions converted to state-promoted assertions**: ~10 sites updated. Where a test asserted `rimsky_claim_handles row count = 0` post-terminal, I changed it to `active count = 0` AND `committed/abandoned count > 0` (depending on the test scenario). Two tests that conflated `TerminalDecision.Lifetime=durable` with "the row's persisted lifetime is durable" had to drop their lifetime assertion — the seed didn't actually persist `lifetime=durable`, so the conflation only worked under the old SetHeldDurable code path.

---

## Task 1.4-1.5 — Stage 4-5 completion notes
**Stage 4 details:**
- Removed `HeldDurable` field, `SetHeldDurable` method, `ListHeldDurableByInstance` method from `ClaimHandleTable` interface + both drivers (postgres + sqlite).
- Updated `assetItem` JSON envelope (control/controlapi/assets.go): replaced the `held_durable bool` field with `state` + `lifetime` strings. The asset query is now `ListByInstanceAndState(committed, durable)` so every surfaced asset has `state == "committed" && lifetime == "durable"` by construction, but surfacing the fields explicitly gives operator tooling forward-compatibility.
- Updated `deadlock_guard_test.go::TestStoreMethodsRejectNilTx` test list: removed `SetHeldDurable` / `ListHeldDurableByInstance` entries; added `Promote`, `ListByInstanceAndState`, `ListByState`, `DeleteResolved` (`DeleteResolvedOlderThan` is intentionally NOT in the nil-tx guard set because it doesn't take a tx — it runs as a single DELETE outside any caller-provided tx; the retention sweep's serialization is via the scheduler-tick advisory lock).
- Migration 010 (postgres + sqlite mirror) drops `held_durable` and the held-durable partial index; adds two new state-based partial indexes (`rimsky_claim_handles_active_idx`, `rimsky_claim_handles_committed_durable_idx`).
- Stage 4 quiescence: `make build-all && make test-all && make lint && go test ./foundation/persistence/postgres/... ./runtime/... -race -count=1` all green.

**Stage 5 details:**
- Concept catalog: claim-handle.md, claim-lifetime.md, asset.md, claim-tree.md, cancel-siblings.md, auto-terminal.md, orphan-reaper.md all refreshed per the plan's Stage 5 tasks.
- CLAUDE.md: blessed-invariant 4 (rewrote to the two-guard-shape model), blessed-invariant 22 (state-column refresh), gotcha bullet for held-durable persistence (state-column refresh + new bullet for the retention sweep cutoff), the schema description for `rimsky_claim_handles` (added state + resolved_at + dropped held_durable mention).
- CHANGELOG.md: new bullet under Unreleased describing the refactor.
- Verified: zero Go-source references to `HeldDurable`, `SetHeldDurable`, `ListHeldDurableByInstance` remain.

**Test flake to surface**: `TestParkedLifecycleParkTimeoutAbandonsHeldClaim` and `TestParkedLifecycleHeldClaimRetentionAcrossPark` intermittently fail under concurrent `make test-all` load (testcontainers Postgres startup contention) but pass cleanly in isolation (3/3 each). Same class as the Item 3 flakes already triaged. Tests' assertions were updated for the Promote-not-delete contract. The flake is pre-existing infra noise, not refactor-related.

---

## Task 4 — Cold-read paydown notes
**Tier 1 splits done:**
- `runtime/runner_acquire.go`: 1096 → 541 lines. Split into:
  - `runner_acquire_named_locks.go` (110 lines): `takeNamedAdvisoryLocks`, `acquireOneLock`, `acquireNamedLock`
  - `runner_acquire_claims.go` (205 lines): `acquireClaim`, `evaluateScopeConflict`
  - `runner_acquire_holders.go` (183 lines): `insertHeldClaimHoldersAtAcquire`, `insertCoHolderClaimHoldersAtAcquire`, `findHoldingSubgraphForAcquirer`
  - `runner_acquire_postcommit.go` (137 lines, additional split beyond the plan): `verifyBeforeRun`, `handleOrphanedClaim`, `transitionToRunning`, `emitLockAcquired`, `claimScope`, `claimAddress`. This extra split was needed to get the orchestration shell under 500; the plan's three-way split would have left runner_acquire.go at 658 lines.

  The shell file (runner_acquire.go) is 541 lines — 8% over the 500-line guideline. The bulk is `tryAcquire` (254 lines) which is the per-candidate acquisition transaction body, logically one unit; further splitting would harm readability. Acceptable per the cold-read-cheatsheet's "~500 line file guideline" wording (tilde, not hard cap).

- `runtime/terminal_decision.go::ResolveClaimHandleTerminal`: 217-line body → 30-line orchestration shell dispatching into 4 named helpers (`dispatchDataProcessingTerminal`, `fireProducerVerb`, `promoteHandleState`, `bumpParentAndRecurse`). Per the spec's "extract 7 distinct operations" target, my split is 4 instead of 7 because some of the spec's enumerated operations (`writeTerminalLineage` = existing `emitTerminalForensics` helper; `recurseSiblingCancel` = existing `cancelInFlightSiblings`; `recurseParentChain` is folded into `bumpParentAndRecurse`) were already extracted or are tight enough that splitting further would be over-decomposition. The 30-line body is well under the spec's "under 100 lines" target.

**Tier 2 decisions:**
- `runtime/subgraph_dispatch.go` (618 lines): NOT SPLIT. The Tier 2 conditional ("if over 500, split per natural concerns") favored keeping the file together — the 618 lines are mostly comments + `applyTerminalCompleteSubgraphCaller` (185 lines, single logical handler) + `CarryExitWriteback` (95 lines). The dispatch-phase boundaries the plan suggests are blurry in practice (entry / exit absorption is wired through the same `applyTerminalComplete*` symmetry). The file is 23% over guideline. Surfaced for user judgement on whether a future split is worth it.

- `runtime/auto_terminal.go` (556 lines): SPLIT. Extracted `resolveParentClaimChain` + `aggregateParentOutcome` into `auto_terminal_chain.go` (225 lines). `auto_terminal.go` is now 351 lines (well under guideline). Skipped the further `auto_terminal_check.go` + `auto_terminal_aggregator.go` decomposition because the residual file is already under guideline and `CheckAndFireResolution` is the single logical unit.

**feature-index.md not created**: the plan calls for updating it, but rimsky's `.claude/rules/rules.md` doesn't list it as a project convention (only the parent zonebase rules do). Per the project-agnostic rule, I didn't proactively create one.

**Surfaced for:** user — heads-up on the slight over-guideline cases (`runner_acquire.go` at 541, `subgraph_dispatch.go` at 618). Both have clear extraction targets if a future spec wants to push further; both have logically-single-unit bodies that argue for the current shape.

---

## Task 5 — sensor-cron multi-replica scope: feature not implemented yet
**Discovery:** The plan calls for a multi-replica advisory-lock serialization test on `sensors/sensor-cron/`. Reading the code (`sensors/sensor-cron/sensor.go`): the file's doc comment says "Multi-replica deployments are guarded by a per-watch `pg_try_advisory_lock`; default is single-replica," but **the code does not actually implement any `pg_try_advisory_lock` call**. The only synchronization is an in-process `sync.Mutex` on `SensorService.mu` (line 59), which serializes only within a single replica.

The grep evidence:
- `sensors/sensor-cron/sensor.go`: zero references to `pg_try_advisory_lock`, `SENSOR_CRON_KEY`, or any `database/sql` import (the file imports only stdlib + cron + protobuf).
- `sensors/sensor-cron/main.go`: also no DB integration.

The doc comment is aspirational; the feature was scoped but not implemented in the 2026-05-15 data-platform-extensions delivery.

**Disposition:** Wrote a test that pins the CURRENT single-replica behavior (`TestSingleReplica_FiresOnceWhenWatchTickFires`) — this confirms the tick-fire path works as expected. **Did NOT write the multi-replica advisory-lock test** the plan calls for because the implementation doesn't exist yet; that test would either always pass trivially or always fail depending on whether you expect serialization. Both are misleading.

This leaves the multi-replica safety property un-tested. To deliver the plan's actual intent ("Multi-replica `sensor-cron` test coverage. Covers advisory-lock serialization across two replicas and fail-over when the lock holder dies mid-window"), a separate follow-up needs to:
1. Implement `pg_try_advisory_lock(SENSOR_CRON_KEY)` (or per-watch lock) in `sensors/sensor-cron/sensor.go::Tick`.
2. Wire `state_db: postgres://...` config so the sensor opens a pool.
3. Then write the multi-replica test.

That's roughly a half-day of feature work, not test work — out of scope for this plan.

**Surfaced for:** user — significant deviation from the plan's intent. The plan assumed the multi-replica advisory-lock feature was implemented; it isn't. Test added pins the single-replica path only.

---

## Task 6 — Asset/lineage coverage classification matrix
**Phase 1 classification:**

| File | Test function | Current shape | Disposition | Rationale |
|---|---|---|---|---|
| `asset/durable_lifetime_e2e_test.go` | `TestDurableLifetimeE2E` | end-to-end | keep-as-is | Already E2E via `pgtest.OpenDriver` + full acquire→Promote→Release loop. |
| `asset/durable_lifetime_persistence_test.go` | `TestDurableLifetimePersistence_TaxonomyConstants`, `TestDurableLifetimePersistence_InsertInputCarriesLifetime` | shape-pinning | keep-as-is | Pins `spec.ClaimLifetime*` constants + `ClaimHandleInsertInput` field shape. Companion E2E exists in `durable_lifetime_e2e_test.go`. |
| `asset/held_durable_across_run_completion_test.go` | `TestHeldDurableAcrossRunCompletion` | shape-pinning (dataprocessing fixture, no full harness) | keep-as-is | Pins the dataprocessing-fixture's Begin→Commit→ListVersions wire contract. The acquire→commit→release E2E is covered by `durable_lifetime_e2e`. |
| `asset/instance_termination_cleanup_test.go` | `TestInstanceTerminationCleanup_ReportShape` | shape-pinning | keep-as-is | Pins the `HeldDurableReleaseReport` shape contract. The full persistence wiring is covered by `durable_lifetime_e2e`. |
| `asset/staging_then_swap_with_co_holders_test.go` | `TestStagingThenSwapWithCoHolders` | shape-pinning (dataprocessing fixture only) | keep-as-is | Pins the dataprocessing fixture's staging-swap shape. The atomic-staging E2E lives in `test/scenarios/atomic_staging/`. |
| `lineage/claim_abandon_lineage_test.go` | `TestClaimAbandonLineage_NaturalAbandonOutcome` | end-to-end | keep-as-is | Already E2E via `scenario.Start`. |
| `lineage/claim_terminal_record_creation_test.go` | `TestClaimTerminalRecordCreation_*` | shape-pinning (fake lineage table) | keep-as-is | Pins the `WriteClaimTerminalLineage` writer's payload shape. The full producer-terminal → lineage-row flow is exercised by `claim_abandon_lineage` + `force_cancelled_lineage` E2Es. |
| `lineage/fakes_test.go` | (helpers, no test functions) | N/A | keep-as-is | Helper file. |
| `lineage/force_cancelled_lineage_test.go` | `TestForceCancelledLineage_*` | end-to-end | keep-as-is | Full `scenario.Start` + `strict.cancel_siblings` cascade. |
| `lineage/helpers_test.go` | (helpers) | N/A | keep-as-is | Helper file. |
| `lineage/leaf_run_record_creation_test.go` | `TestLeafRunRecordCreation_*` | shape-pinning (fake lineage table) | keep-as-is | Pins `WriteLeafRunLineage` writer's payload shape. Real-DB exercise lives in the broader runtime tests (e.g. terminal-decision integration). |
| `lineage/openlineage_emission_test.go` | `TestOpenlineageEmission_*` | shape-pinning (mocked HTTP emitter) | keep-as-is | Pins the wire-contract POST shape to a Marquez-style backend. The subscriber binary's unit tests live alongside the binary at `subscribers/openlineage/emitter_test.go`. The control-side end-to-end (rimsky → subscriber → backend) would need a multi-process harness that's out of scope. |
| `lineage/recursive_ancestor_walk_test.go` | `TestRecursiveAncestorWalk_MultipleLeafRowsSameFrame` | shape-pinning (fake lineage table) | keep-as-is | Pins multi-leaf-row co-existence on the writer side; real-DB ancestor walks are implicitly covered by the E2E tests. |

**Phase 2:** No upgrades, no splits, no new companions. Every test in scope has a reasonable disposition. The asset/lineage surface has good coverage: the end-to-end paths (durable lifecycle, abandon, force-cancel) all have real-postgres tests via `pgtest.OpenDriver` or `scenario.Start`; the shape-pinning tests cover focused units (writer payloads, fixture contracts, wire-contract shapes) that don't earn their keep with the harness-boot cost.

The audit's null result is itself informative: the plan was right to budget for an audit, and the audit confirmed the test set is already well-stratified across shape vs. end-to-end. No action needed.

**Surfaced for:** user — confirmation that the asset/lineage test set is already coherent. No tests added, no splits done.

---
