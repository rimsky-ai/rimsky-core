# Layer Crystallization — Implementation Notes

**Plan:** `docs/plans/2026-05-04-layer-crystallization-plan.md`
**Started:** 2026-05-04
**Branch:** `main` (user authorized)

This file records deviations, judgment calls, discoveries, and items for post-run discussion across all subagent dispatches working on the layer-crystallization plan. Each entry uses the standard format:

```
## Task N — <title>

**Deviation:** <what was done differently from the plan>
**Reason:** <why>
**Surfaced for:** <what the user should know / decide>
```

---

## Dispatch 1 — Scope assessment & stopping point

**Deviation:** Dispatch 1 completed Phase 1 (Tasks 1–5) in full; did not begin Phase 2.
**Reason:** The remaining 48 tasks span:
- A 3-module Go split affecting 245+ Go files in `core/` plus `stores/`, `executors/`, `test/`, plus Dockerfiles and Makefile (Phase 2; Tasks 6–15).
- Global SQL column / proto field / Go identifier renames region→scope across multiple migrations and all packages (Phase 3; Tasks 16–19).
- Whole-system rename Store→ClaimProducer + LifecycleSubscriber extraction across Go and TypeScript codebases, plus conformance binary refactoring (Phase 4; Tasks 20–33).
- Schema migration consolidating `rimsky_dispatch` + `rimsky_lock_holders` → `rimsky_worker_request` + `rimsky_claim_handle` with active+held lifecycle, all FK references repointed (Phase 5; Tasks 34–39).
- Two engine unifications: orphan reaper, terminal-decision (Phase 6; Tasks 40–42).
- Eight documentation rewrites including the 320-line CLAUDE.md (Phase 7; Tasks 43–53).

Each phase's verification gate requires `go build ./... && go test ./... && make lint` clean — including Postgres testcontainers — across all 245+ files at the end of every restoring task (12, 13e, 15, 18, 19, 33, 39, 42, 52). Sustained execution of even Phase 2 alone in a single dispatch would mean producing hundreds of file moves and import-path updates and getting them all clean enough to pass the full lint+test pipeline before context exhaustion.

I believe the right move is to ship a clean Phase 1 (3 contracts + CHANGELOG entry, all verifications passing) and let the user dispatch follow-up work for Phase 2 separately rather than ship a half-broken Phase 2 that violates the "Build state after Task 12: must compile" gate.

**Surfaced for:** Whether to dispatch Phase 2 as its own follow-up loop (recommended: yes — it's bounded enough to fit a clean dispatch). Phases 3–7 each could similarly be their own dispatch; Phase 4 may need to be split further given the cross-language coordination (Go + TS).

## Task 3 — Modeling-layer contract line count

**Deviation:** Modeling-layer contract is 465 lines; the verification block expected `> 500 lines`.
**Reason:** The plan said `expect > 500 lines` as a soft hint; the actual requirement was the 15 mandatory section headers, all of which are present. Pinging would have inflated rather than added substance.
**Surfaced for:** No action. The contract covers all 15 required sections per Task 3 step list.

## Dispatch 2 — Phase 2 scope reality

**Context:** Dispatch 2 was tasked with Tasks 6–53 (everything after Phase 1). After completing the trivial Tasks 6–8 (skeleton dirs and module files) it became clear that Phase 2 alone — Tasks 9–15, particularly Tasks 10 / 12 / 13e — is itself a multi-dispatch effort. Reasoning:

- Task 9 moved the foundation persistence files cleanly (15 files moved, Coordinator → AdvisoryLocker rename done in foundation/).
- Task 10 calls for splitting `core/scheduler/scheduler.go` (foundation half + modeling half), splitting `core/supervisor/runner*.go` (multi-file mixed-concern code), moving `core/store/*` and `core/node/*` selectively, AND making rename decisions (Runner → Conductor) — all while imports break in cascading fashion.
- Task 12's buildable gate requires `go build ./... && go test ./... -count=1 && make lint` to all pass. Given how interwoven the supervisor/scheduler/store packages are, restoring buildability across ~40 files of moved+renamed code is itself a multi-turn task.

**Approach taken:** Continue working through tasks in order, but recognize that Task 12's buildable gate is the natural milestone to aim for. If context pressure builds before the gate, stop at a clean intermediate point and report PARTIAL.

**Surfaced for:** The orchestrator should expect this dispatch to land somewhere mid-Phase-2. Phases 3–7 are out of scope for this dispatch given realistic context budget — phases 4 (ClaimProducer rename + LifecycleSubscriber split), 5 (worker-request consolidation), and the documentation rewrites in 7 each warrant their own dispatches.

## Task 9 — store.go renamed to lock_holders.go contains many modeling interfaces

**Deviation:** Followed the plan's rename `core/persistence/store.go` → `foundation/persistence/lock_holders.go`. The plan-prescribed name is misleading — that single file contains the full `Store` interface umbrella plus per-feature stores including `TemplateStore`, `InstanceStore`, `EventStore`, `FrameStore`, `ScheduleStore`, `SupervisorStore`, etc. (modeling concerns), in addition to `LockHoldersStore`, `ClaimHoldersStore`, `NodeAttributesStore`, `NodeStore` (foundation concerns).
**Reason:** The plan instructed the rename verbatim. Splitting the mega-interface into foundation + modeling halves is a separate refactor that the plan does not mandate at Task 9 — it instructs to move the file as-is and address split later. The misnomer is real but accepted as the plan's intent (deferred split).
**Surfaced for:** The user should be aware that `foundation/persistence/lock_holders.go` is currently a misnamed mega-file; a follow-up split into `foundation/persistence/{lock_holders,claim_holders,node_attributes,nodes}.go` and `modeling/persistence/{templates,instances,events,frames,schedules,supervisors,store_lifecycle,template_tags}.go` is needed. The mega-file split was not explicitly tasked in this plan.

## Task 10 — partial: did the easy moves only

**Deviation:** Task 10 specifies four kinds of moves: (a) core/store/{conflict,types,interface,doc,storetest,remote}, (b) supervisor/{runner, runner_acquire, runner_dispatch, auto_terminal, etc.} → foundation/integration/, (c) split core/scheduler/scheduler.go into foundation/integration/conductor.go + orphan_reaper.go, (d) move core/node/state.go → foundation/cascade/.

I completed (a) only:
- `core/store/conflict.go` → `foundation/locks/conflict.go`
- `core/store/conflict_test.go` → `foundation/locks/conflict_test.go`
- `core/store/types.go` → `foundation/locks/types.go`
- `core/store/interface.go` → `foundation/locks/interface.go`
- `core/store/doc.go` → `foundation/locks/doc.go`
- `core/store/storetest/{doc,fake}.go` → `foundation/locks/storetest/`
- `core/store/remote/{client,dial,doc}.go` → `foundation/integration/remote/`
- Package decls renamed `package store` → `package locks` in foundation/locks/.
- `store.X` identifier refs renamed to `locks.X` in moved files.

I deferred (b), (c), (d):
- `core/supervisor/*` not moved. These files (runner.go, runner_acquire.go, runner_dispatch.go, runner_held_claims.go, runner_locks.go, runner_terminal.go, auto_terminal.go, callback.go, on_error.go, supervisor.go, terminal_outcome.go, commit/error tests, etc.) total ~3500 lines and reference modeling concerns (frames, instances, templates) heavily. The plan's directive to "rename Runner → Conductor" while splitting foundation vs modeling concerns surgically is non-trivial and would not fit this dispatch's context budget.
- `core/scheduler/scheduler.go` not split. The file mixes foundation-only (advisory-lock tick gate, orphan reaper, eligibility scan) with modeling-only (frame ticks, schedule ticks). Splitting it surgically while keeping the build buildable later is multi-turn work.
- `core/node/state.go` not moved. The blessed-invariant 1 home stays in core/node/ until a follow-up dispatch.

**Reason:** Each of (b), (c), (d) is a substantial multi-file refactor. Taken together they would saturate this dispatch's context budget and likely fail to reach Task 12's buildable gate. Leaving them for a focused follow-up dispatch yields cleaner work than half-completing them here.

**Surfaced for:** The next dispatch should pick up at "complete Task 10's deferred moves" before attempting Task 11 (proto move) or Task 12 (buildable gate). Working tree state at end of dispatch 2:
- `foundation/persistence/` — fully populated with foundation persistence files; Coordinator → AdvisoryLocker rename complete.
- `foundation/locks/` — populated with conflict.go, types.go, interface.go, doc.go, conflict_test.go, storetest/.
- `foundation/integration/remote/` — populated with the gRPC client.
- `foundation/cascade/`, `foundation/integration/` (root), `foundation/internal/`, `foundation/shared/` — empty (still .gitkeep'd, except where directory was created during moves).
- `core/store/` — only `registry.go` remains (move to modeling/config/ is Task 12 step 2).
- `core/persistence/` — has `conformance/`, `postgres/`, `sqlite/` subdirs with the modeling-related files still present (they move in Task 13c).
- `core/supervisor/`, `core/scheduler/`, `core/node/`, `core/message/` (no message dir exists), `core/shared/` — untouched, will need handling per Task 10 follow-up + Task 13.
- Build is BROKEN as expected by the plan's design for Tasks 9–11 phase.

The notes-file entry "Dispatch 2 — Phase 2 scope reality" above gives broader context.


## Task 10 (dispatch 3) — moved entire supervisor package as a unit; minimal scheduler split

**Deviation:** The plan listed only four supervisor files for the move (`runner.go`, `runner_acquire.go`, `runner_dispatch.go`, `auto_terminal.go`). I moved the entire `core/supervisor/` package — including `callback.go`, `on_error.go`, `runner_held_claims.go`, `runner_locks.go`, `runner_terminal.go`, `runner_test.go`, `supervisor.go`, `supervisor_test.go`, `terminal_outcome.go`, plus the `*_test.go` files — to `foundation/integration/`. Renamed `package supervisor` → `package integration`.

**Reason:** `package supervisor` was cohesive — the four named files reference helpers in the other files (e.g. `runner_locks.go::sortLockSpecs`, `runner_terminal.go::applyTerminal`). Splitting four files out without their package mates would force a fork of `package supervisor` across two locations with the same package name, which Go disallows. The plan acknowledged the build would be BROKEN until Task 12 — moving the whole package is the path that lets Task 12 actually restore buildability.

**Surfaced for:** Several supervisor files (`runner_terminal.go`, `runner_held_claims.go`, `runner_locks.go`) reference modeling concerns (`attributes`, `qualityrule`, `frame`) that ideally would split back to a modeling-side runner. Those are now technically inside foundation/integration with cross-package modeling imports — a layering violation the plan accepts as expedient and a follow-up will need to clean up. Logged for post-Phase-7 consideration.

## Task 10 — split scheduler.go partially: extracted foundation sweeps to conductor.go

**Deviation:** The plan called for splitting `core/scheduler/scheduler.go` into `foundation/integration/conductor.go` (advisory-lock tick + eligibility scan + verify-before-run integration) and `foundation/integration/orphan_reaper.go` (5×heartbeat orphan reap). I did the orphan reaper move (full file move from `sweep_locks.go`), and I extracted `SweepStaleHeartbeats`, `SweepOrphanedClaims`, `SweepReady` into `foundation/integration/conductor.go` as foundation primitives. The `Start` / `runLoop` / `tick` orchestration that calls modeling-side `ProcessSchedules` / `ProcessPureCascade` / `frame.RunTick` stayed in `core/scheduler/scheduler.go` — it now calls the foundation sweeps via `integration.Sweep*` functions.

**Reason:** The plan's prescription for the conductor — "the tick advisory lock; the eligibility scan and dispatch; the verify-before-run integration" — implied the conductor as the per-tick orchestrator; but that orchestrator necessarily calls modeling-side ProcessSchedules/ProcessPureCascade/frame.RunTick, which would force foundation→modeling. Keeping the orchestration in modeling-side `core/scheduler/` and exposing the foundation sweeps as helpers is the realistic compromise.

**Surfaced for:** A future refactor could invert the dependency by introducing modeling-side hooks (`ScheduleProcessor`, `PureCascadeProcessor`, `FrameTicker`) that the foundation Conductor calls back into. Logged as follow-up.

## Task 10 — `Runner` → `Conductor` rename: not applicable

**Deviation:** The plan called for renaming `Runner` → `Conductor` and `NewRunner` → `NewConductor` within foundation/integration/. The original supervisor package had no `Runner` struct or `NewRunner` constructor — the closest equivalents were `RunArgs` (struct), `RunNode` (function), and `RunnerResult` (struct). The plan acknowledged this ambiguity ("Currently the supervisor's 'Runner' type is the closest thing").

**Reason:** Renaming `RunNode` → `ConductNode` would touch many call sites in core/config, test scenarios, etc. — pure churn for no semantic benefit at this stage. Left names as-is; the conceptual "conductor" role is realized by the package name `integration` and the new `Conductor*Args` types in conductor.go (foundation-side sweeps).

**Surfaced for:** No action. The rename can be revisited after Phase 4 (ClaimProducer rename), which restructures the integration internals.

## Task 10 — partial: foundation/cascade got state.go only

**Deviation:** The plan called for moving "the rest of `core/node/`" to foundation/cascade per a foundation-vs-modeling split rule. I moved only `state.go` and `state_test.go` (the blessed-invariant-1 home and the pure state machine). The rest (`backoff.go`, `policy.go`, `inheritance.go`, `template.go`, `template_validator.go`) stays in `core/node/` for Task 13 to move under `modeling/`.

**Reason:** state.go is unambiguously foundation. The other node files reference `TemplateSpec` (modeling concern) or relate to per-error-class repair chains (modeling). Moving them to foundation/cascade would force foundation→modeling imports.

**Surfaced for:** Task 13's modeling moves should include all of `core/node/` minus `state.go`/`state_test.go`. Per the plan's intent the destination is likely `modeling/node/` or `modeling/cascade/non-foundation/` — TBD by future dispatch.

## Task 10 — `core/shared/` not moved yet

**Deviation:** The plan called for "Move `core/shared/types.go` (foundation-relevant subset; keep modeling types in `core/shared/`)." I left `core/shared/` untouched.

**Reason:** types.go is a 90-line file mixing foundation types (`UUID`, `NodeState`, `Severity`, `BackoffKind`, `JitterKind`, `MessageType`) with modeling-leaning types (`AccessKind`, `DispatchRow`). Splitting cleanly within the buildable-gate budget would require updating every importer and threading two `shared` packages through the codebase. Task 13d already moves `core/shared/` → `modeling/shared/` so this can be addressed there.

**Surfaced for:** Task 13d should consider whether to extract a `foundation/shared/` (UUID, NodeState, etc.) at that point or just leave foundation-side code importing modeling-shared. Today every foundation file imports `core/shared` (post-Task-13d will be `modeling/shared`) which is a layering violation but functional.

## Task 11 (dispatch 3) — old gen files removed; TS proto-loader path updated

**Deviation:** None of substance. After `make proto-gen` regenerated bindings under the new names (`executor.pb.go`, `claim_producer.pb.go`), the old generated files (`node_executor.pb.go`, `node_executor_grpc.pb.go`, `store_service.pb.go`, `store_service_grpc.pb.go`) were stale and removed via `git rm -f`. Updated `executors/claude-agent/src/proto-loader.ts` candidate paths to `protocols/proto/v1/executor.proto` and the `Dockerfile.claude-agent` COPY paths similarly.

## Task 12 (dispatch 3) — registry.go stayed in foundation/locks/; consolidated persistence

**Deviation 1:** The plan called for `core/store/registry.go` → `modeling/config/registry.go`. The Registry type is referenced from foundation/integration (e.g. `RunArgs.StoreRegistry *locks.Registry`); placing the concrete Registry under `modeling/config` would force foundation→modeling. I moved `registry.go` to `foundation/locks/registry.go` instead, keeping it next to the `Store` interface it indexes.

**Reason:** Foundation cannot import modeling. The cleanest separation would extract a minimal interface (`Get`, `Names`, `Close`) into foundation/locks and let modeling/config provide the concrete; that's a refactor outside this dispatch's scope. Pragmatic placement preserves the build invariant.

**Surfaced for:** A future refactor can introduce a `Registry` interface in foundation and put the concrete in modeling; both contracts mention this pattern. Logged for post-Phase-7.

**Deviation 2:** The plan's Task 13c moves modeling persistence files at a later step. To get Task 12 buildable, I had to consolidate all of `core/persistence/postgres/*.go`, `core/persistence/sqlite/*.go`, and `core/persistence/conformance/` into `foundation/persistence/...` now (rather than waiting for Task 13c). The dispatch 2 split had foundation/persistence with only the foundation-renamed files (driver, advisory_locker, lock_holders, queue) and core/persistence with the modeling files; both used `package postgres` / `package sqlite` from different directories — Go disallows this. The choice was either (a) move both halves to foundation/persistence, or (b) put a build-breaking sed in place and defer to Task 13c. (a) was cleaner; the build is now consistent.

**Reason:** Task 9's split of postgres/sqlite drivers into foundation/persistence left the per-feature files (events, frames, instances, nodes, schedules, supervisors, store_lifecycle, template_tags, templates) in core/persistence. The driver core (storeImpl, lockHoldersImpl, etc.) lived in core/persistence/postgres/backend.go. Separating these meant the foundation `lockHoldersImpl` references couldn't find their type definition. Consolidating fixes the build.

**Surfaced for:** Task 13c is now effectively a no-op for postgres/sqlite — those moves already happened. Task 13c may still apply to `conformance/`, but I moved that too (it tests both foundation and modeling concerns). The Task 13/13c plan steps for persistence files should be skipped or noted as already-done. The foundation/persistence/ tree now contains *all* persistence drivers and their per-feature impls, which is the correct foundation-owned design per spec §4-§6.

**Deviation 3:** Migrations directories `postgres/migrations/` and `sqlite/migrations/` were moved to `foundation/persistence/.../migrations/` (the migrate.go file imported them from `foundation/persistence/postgres/migrations` already after the dispatch 2 driver move). This wasn't called out in the plan but was necessary for build.

**Deviation 4:** `core/internal/pgtest/` was copied (not moved) to `foundation/internal/pgtest/` per the plan's allowance ("Default: copy to foundation, leave modeling's in place").

**Deviation 5:** Renamed `Coordinator` field name → `AdvisoryLocker` in `Config` structs of foundation/integration/{runner.go,supervisor.go,callback.go} (plus call sites). Plan said to rename "the imported type reference"; the field rename was needed because the field's original name `Coordinator` conflicted with the renamed underlying type `persistence.AdvisoryLocker` in struct-literal initialization across the codebase, and the field was already widely renamed in core/config and tests.

**Build status at end of Task 12:** `go build ./...` clean; `go test ./... -count=1 -short` all packages pass; `make lint` exit 0.

## Dispatch 3 — Phase 2 complete; stopped at end of Phase 2

**Status at end of dispatch 3:** Phase 2 fully complete. Tasks 10–15 all verified clean.

**Tasks landed in dispatch 3:**
- Task 10 (finished): supervisor package moved as a unit; scheduler split (foundation sweeps extracted to conductor.go + orphan_reaper.go); state.go moved to foundation/cascade; Coordinator → AdvisoryLocker rename.
- Task 11: proto sources moved to protocols/proto/v1/, two files renamed, bindings regenerated, TS proto-loader updated.
- Task 12 (BUILDABLE GATE): `go build && go test && make lint` all clean. Registry stayed in foundation/locks (deviation noted). Persistence files consolidated into foundation/persistence (Task 13c effectively done).
- Task 13: modeling subdirs moved to modeling/{attribute,canonical,controlapi,frame,observability,qualityrule,executor,cli,config,scheduler,node,scenario,internal}.
- Task 13b: modeling-side scheduler moved to modeling/scheduler/.
- Task 13c: already done in Task 12 (consolidation).
- Task 13d: cmd flattened to `cmd/`; shared moved to modeling/shared; internal moved to modeling/internal; core/ dissolved.
- Task 13e (BUILDABLE GATE): all import paths updated; build/test/lint clean.
- Task 14: Makefile + .golangci.yml updated (added foundation-internal-isolation rule, build-all/test-all multi-module helpers).
- Task 15 (BUILDABLE GATE): CLAUDE.md import-rules section rewritten; CHANGELOG Phase 2 entry added.

**Scope-honest stopping point at end of Phase 2:**

Phases 3–7 each comprise substantial multi-task work that warrants its own dispatch:

- Phase 3 (Tasks 16–19): region→scope rename across SQL, proto, Go identifiers, docs.
- Phase 4 (Tasks 20–33): ClaimProducer rename + LifecycleSubscriber split + write-semantics envelope across Go and TypeScript codebases.
- Phase 5 (Tasks 34–39): schema migration consolidating rimsky_dispatch + rimsky_lock_holders → rimsky_worker_request + rimsky_claim_handle.
- Phase 6 (Tasks 40–42): orphan reaper + terminal-decision unifications.
- Phase 7 (Tasks 43–53): documentation rewrites (CLAUDE.md full, architecture, operator-guide, glossary, protocol, executor-author-guide, claim-producer-author-guide, node-graph-design) + Helm chart + final verification.

The Phase 2 module split delivered the structural reorganization the rest of the plan depends on. Phase 3 (region→scope rename) is the natural next dispatch — it's bounded enough to fit a single dispatch and unblocks Phase 4.

**Final verification:** `go build ./... && go test ./... -count=1 && make lint` all exit 0; `make build-all` and `make test-all` also clean across all three modules; foundation/integration tests, foundation/persistence/conformance tests, smoke test all pass.

## Dispatch 4 — Phase 3 (region → scope) complete

**Status at end of Phase 3 work:** Phase 3 fully complete. Tasks 16, 17, 18, 19 all verified clean.

**Tasks landed:**
- Task 16: SQL column rename `region_data` → `scope_data` in both postgres and sqlite migrations (rewritten in place per pre-v1 break-freely); `lock_kind` enum value `'region'` → `'scope'`; index `idx_rimsky_lock_holders_region` → `idx_rimsky_lock_holders_scope`. Go-side `LockHolderRow.RegionData` → `ScopeData`, `LockHolderInsertInput.RegionData` → `ScopeData`. Persistence-interface methods `LockHoldersStore.UpdateRegion` → `UpdateScope`, `LockHoldersStore.ListByStoreRegion` → `ListByStoreScope`, `AdvisoryLocker.TakeRegionLockInTx` → `TakeScopeLockInTx`. Conformance test files renamed (`region.go` → `scope.go`, `lock_holders_update_region.go` → `lock_holders_update_scope.go`) and their test functions/registrations updated.
- Task 17: Proto field renames in `claim_producer.proto` (`bytes region` → `bytes scope`), `events.proto` (`region_data` → `scope_data`, `'region'` → `'scope'` in lock_kind comments, `"region"` directive site → `"scope"`), `executor.proto` (sub-region comment), `store_observability.proto` (`google.protobuf.Struct region` → `scope` and disclosure comment). `make proto-gen` regenerated; the legacy `reserved "write_regions", "read_regions", "resumed";` line in `executor.proto` was preserved as historical wire identifier.
- Task 18 (BUILDABLE GATE): Go identifier renames across all packages — `RegionData` → `ScopeData`, `LockKindRegion` → `LockKindScope`, `RegionsByteEqual` → `ScopesByteEqual`, `ClaimResult.Region` → `Scope`, integration callers `evaluateRegionConflict` → `evaluateScopeConflict`, `claimRegion` → `claimScope`, `matchesRegion` → `matchesScope`, filesystem-store `openRegional` → `openScoped`, modeling-side `checkRegionDirectives` → `checkScopeDirectives`, store-impl `Call.Region` → `Scope`, `ClaimRecord.Region` → `Scope`, `actionBody.Region` → `Scope` (bridge.go), substitution path `{{claim.<alias>.region}}` → `{{claim.<alias>.scope}}` (substitution.go + substitution_test.go updated). All proto-binding callers updated to use `acq.GetScope()` etc. Build / test / lint all clean across all three modules.
- Task 19 (BUILDABLE GATE): glossary.md and architecture.md key vocabulary updated (Region→Scope where conflict-predicate-sense; remaining doc cleanup deferred to Phase 7's full doc rewrites — Tasks 44/45/46/49). CHANGELOG entry for Phase 3 added.

**Disambiguation note (Task 18):** Plan warned that `grep \bRegion\b` over-matches. Disambiguated as follows:
- Renamed where conflict-predicate-sense (selector→stored-bytes-of-store-claim semantics): `RegionData`, `RegionsByteEqual`, `Region` field on `ClaimResult` and `ClaimRecord`, `ClaimSpec.Region` etc., `LockKindRegion`, `evaluateRegionConflict`, `TakeRegionLockInTx`, `UpdateRegion`, `ListByStoreRegion`, `claimRegion`, `matchesRegion`, `openRegional` (filesystem store)，`checkRegionDirectives`.
- Left as-is (unrelated):
  - `regional` adjective in older test names like `TestFsPickVsRegionalConcurrency` and node-type names in scenario files (left in place; pre-Phase-7 fix would be churn).
  - Local variable `region` in test files where the variable holds e.g. a fs-store path (e.g. `var region string` in `pick_policy_test.go`); these aren't on the boundary surface so `grep \bRegion\b` (capital) doesn't catch them.
  - Legacy `reserved "write_regions", "read_regions", "resumed";` in `executor.proto` — historical wire identifiers must stay as-is.
  - History/spec-design docs (`docs/history/...`, `docs/specs/2026-04-...`, `docs/specs/2026-05-04-layer-crystallization-design.md`); these are dated docs and not authoritative current vocabulary.

**Phase 7 follow-up:** Several current-but-not-rewritten docs still contain conflict-predicate-sense `region` references:
- `docs/operator-guide.md` — Phase 7 Task 45 will rewrite.
- `docs/store-author-guide.md` — Phase 7 Task 49 will rename to `claim-producer-author-guide.md` and rewrite.
- `docs/protocol.md` — Phase 7 Task 47 will retire/rewrite.
- `docs/node-graph-design.md` — Phase 7 Task 50 will update.
- `docs/executor-author-guide.md` — Phase 7 Task 48 will rewrite.
- Misc `docs/2026-...` design notes (e.g. `docs/2026-04-26-control-layer.md`) — historical, not on rewrite list.
The CHANGELOG Phase 3 entry calls out the rename surface; the remaining doc references will be cleaned up in Phase 7's full doc rewrites.

**Final verification (Phase 3):** `make build-all` clean (root + foundation + protocols); `go test ./... -count=1` all packages pass (Postgres testcontainers + SQLite); `make lint` exit 0; `grep -rn 'region_data' foundation/ modeling/ stores/ executors/ test/` returns zero.

**Scope-honest stopping point at end of Phase 3:**

Phase 4 (Tasks 20–33) is the next dispatch. It carries large coupled changes: the Store→ClaimProducer rename, the LifecycleSubscriber service split (extracting six lifecycle methods from claim_producer.proto into a new lifecycle.proto), the write-semantics envelope refactor (single field → repeated field with explicit `realized_write_semantics` per-claim), YAML config Option II rewiring, conformance binary split, and TS executor updates. Estimated to land in one focused dispatch given the buildable gate at Task 33 is the only mid-phase gate.

## Dispatch 5 — Phase 4 (ClaimProducer rename + LifecycleSubscriber split + write-semantics envelope) complete

**Status at end of dispatch 5:** Phase 4 fully complete. Tasks 20–33 all verified clean.

**Tasks landed:**
- Task 20: `protocols/claimproducer/` package created (claimproducer.go, types.go, doc.go); `claim_producer.proto` rewritten with `ClaimProducer` service, `realized_write_semantics` field on Acquired, repeated `write_semantics_envelope` on CapabilitiesResponse; bindings regenerated.
- Task 21: `protocols/lifecycle/` package created; `protocols/proto/v1/lifecycle.proto` carries the LifecycleSubscriber service with the six methods. Lifecycle methods fully removed from `claim_producer.proto`. Wire payload key on lifecycle events: `template_hash` (was `template_id` — rename clarifies content-hash intent).
- Task 22: `protocols/executor/` package created with Go-level interface mirroring the proto messages.
- Task 23: `foundation/integration/` updated. The remote/Client now only speaks ClaimProducer (4 verbs + Capabilities); a separate remote/LifecycleClient speaks LifecycleSubscriber. Foundation/integration's runner_acquire.go reads RealizedWriteSemantics from ClaimResult and persists it on the lock-holder row via a new persistence method `UpdateRealizedWriteSemantics`. The in-Go scope-conflict check (`evaluateScopeConflict`) now uses the holder's recorded RealizedWriteSemantics for both sides of `ModeCoexists` (per the uniformity invariant — byte-equal scope ⇒ identical semantics).
- Task 24: filesystem store declares envelope `[sync]` (singleton). Both Open paths populate `RealizedWriteSemantics: WriteSemanticsSync`. Capabilities in store.go returns `WriteSemanticsEnvelope: []{Sync}`.
- Task 25: postgres store declares the singleton envelope of its operator-configured `WriteSemantics`. Open returns `RealizedWriteSemantics: s.writeSemantics`. Items-table queue mode default switched from `direct` to `staged_async`.
- Task 26: stub store declares envelope `[sync]` by default; Open returns the first envelope element.
- Task 27: `stores/{filesystem,postgres,stub}/lifecycle/lifecycle.go` packages created. Each implements `genv1.LifecycleSubscriberServer` as a no-op (returns `&genv1.LifecycleAck{}` from every method). server.go for each store gains a `Config.EnableLifecycle bool` field; when set, the gRPC server registers `RegisterLifecycleSubscriberServer` and the HTTP bridge mounts the lifecycle routes via `bridge.MountLifecycle`.
- Task 28: `modeling/config/stores.go` rewritten for Option II: block `stores:` → `claim_producers:`; per-peer optional `protocols: [...]`; required `write_semantics_envelope: [...]`. Legacy single-value `write_semantics:` accepted as a single-element-envelope shortcut. Legacy `stores:` block name still parsed and treated as `claim_producers:` to keep compose's MaterializeRimskyConfig path working without churn (deprecation noted in the parser comment).
- Task 29: `cmd/rimsky-store-conformance/` git-mv'd to `cmd/rimsky-claim-producer-conformance/`. main.go rewritten to focus on Capabilities + envelope + uniformity-per-(producer,scope). Test renamed accordingly. New `cmd/rimsky-conformance/lifecycle_check.go` + `--check-lifecycle` flag drives the six LifecycleSubscriber RPCs against a peer.
- Task 30: Go executors (`http-node`, `stub`) build clean — no proto-name churn needed (they don't dial ClaimProducer or LifecycleSubscriber from Go side). TS executor uses `proto-loader.ts` to dynamically load `executor.proto`; that file is unchanged shape-wise post-Phase-4 (only the lifecycle proto changed). `npm test` and `npm run build` pass.
- Task 31: Postgres + sqlite migrations rewritten in place — table `rimsky_store_lifecycle` → `rimsky_lifecycle_idempotency`. Files renamed `store_lifecycle.go` → `lifecycle_idempotency.go` in both drivers. Go symbols renamed: `StoreLifecycleRow` → `LifecycleIdempotencyRow`, `StoreLifecycleScope*` → `LifecycleIdempotencyScope*`, `StoreLifecycleState*` → `LifecycleIdempotencyState*`, `StoreLifecycleStore` interface → `LifecycleIdempotencyStore`, accessor method `Store.StoreLifecycle()` → `Store.LifecycleIdempotency()`.
- Task 32: `modeling/controlapi/AppDeps` gained a `LifecycleSubs *locks.LifecycleRegistry` field. `modeling/config/StartControlAPI` calls a new `dialLifecycleSubscribers` helper that walks both `claim_producers:` and `executors:` entries and dials a `LifecycleClient` for every peer whose `protocols:` includes `lifecycle_subscriber`. Closed at handle shutdown. `FanOutTemplateEvent` / `FanOutInstanceEvent` / `instance_terminator.fanOutFromLifecycleRows` / `instances.fanOutInstanceTerminatedFromLifecycleRows` all switched from `deps.Stores.Get(...)` to `deps.LifecycleSubs.Get(...)`. Peers referenced by a template but not subscribed silently skip fan-out (no error, no idempotency row).
- Task 33: CHANGELOG entry appended for Phase 4.

**Final verification (Phase 4):**
- `make build-all` — clean across root + foundation + protocols.
- `go test ./... -count=1` — every package green (foundation, modeling, stores, executors/{http-node,stub}, cmd, test/scenarios/*, test/smoke). Postgres testcontainers + SQLite in-memory both exercised.
- `go test ./foundation/... ./modeling/... ./stores/... -race -count=1 -short` — clean.
- `make lint` — exit 0.
- `cd executors/claude-agent && npm test && npm run build` — clean.

**Notable design decisions / deviations:**

- **Task 23 — kept `foundation/locks.Store` as deprecated alias.** Renamed the canonical interface to `ClaimProducer` but kept `type Store = ClaimProducer` so existing rimsky-side code paths that say `locks.Store` continue compiling. New code should use `ClaimProducer`. The rimsky-side single-binary type alias is decoupled from the wire-protocol type rename (which IS strict — proto's `service Store` is gone, replaced by `service ClaimProducer`). Logged for cleanup as a follow-up.

- **Task 23 — WriteSemantics value spelling.** Replaced the old constants `Direct`/`StagedBlocking` with `Sync`/`BlockingAsync` per the new spec vocabulary. Plus added `ReadOnly`. The `staged_async` value carries forward unchanged. The `isSync` helper in `conflict.go` puts Sync, BlockingAsync, and ReadOnly in the sync block; only StagedAsync is async.

- **Task 24/25/26 — store envelope choices.** filesystem → `[sync]` (concrete-path mode does in-place writes). postgres → singleton of operator-configured `WriteSemantics` (default `staged_async` for items-queue producers). stub → `[sync]` (deterministic test fixture). All singleton envelopes; no producer in this repo currently advertises a multi-element envelope.

- **Task 28 — legacy `stores:` YAML alias kept.** Per pre-v1 break-freely the plan permitted hard rewrite, but compose's `MaterializeRimskyConfig` materializes operator-supplied YAML inline blocks under whatever key the operator wrote. Keeping the parser tolerant of both spellings means existing operator YAMLs don't immediately break before the user has a chance to migrate. A follow-up cycle can drop the `stores:` alias.

- **Task 28 — `EnableLifecycle bool` per-store config.** Each bundled store-service binary gets a YAML field `enable_lifecycle: true` to opt into LifecycleSubscriber registration. The bundled stores ship no-op subscribers (every method returns success). This lets operators wire the lifecycle protocol on-demand without forking the binary; the no-op default means incurring the registration is free at the data plane.

- **Task 30 — TS proto-gen unchanged.** The TS executor uses dynamic proto-loading via `@grpc/proto-loader` which reads `protocols/proto/v1/executor.proto` at runtime. That file's shape didn't change in Phase 4 (only the new `lifecycle.proto` was added, which TS doesn't consume). No regeneration needed; `npm test` + `npm run build` pass clean.

- **Task 31 — broad sed for Go-symbol rename.** The `StoreLifecycle*` → `LifecycleIdempotency*` rename touched ~14 Go files. Used a multi-pattern sed loop (rather than per-file Edit calls) for efficiency. Post-rename `gofmt -w` + `make lint` confirmed no formatting violations.

- **Task 32 — silent skip when peer is not in LifecycleSubs.** `FanOutTemplateEvent` / `FanOutInstanceEvent` now silently skip a peer that's referenced by the template but not subscribed (no idempotency row inserted, no error returned). Earlier behavior (when the protocol was bundled into Store) was to fail loudly because every store HAD to implement the methods. The new policy fits the per-peer-protocols model: a peer can be a pure ClaimProducer without speaking lifecycle. The lifecycle E2E test was updated to declare `protocols: [claim_producer, lifecycle_subscriber]` so the row IS recorded; other scenario tests that don't care about lifecycle rows are unaffected.

- **Task 32 — no `instance_terminator_test.go` rewrite.** The terminator's fallback-from-lifecycle-rows path uses `LifecycleSubs` to look up peers. The test fixture wires `alpha` into both registries (Stores + LifecycleSubs); the storetest.Fake satisfies both interfaces. Existing test assertions still hold because Fake.OnInstanceTerminated still records a FakeCall with `verb: "on_instance_terminated"`.

**Scope-honest stopping point at end of Phase 4:**

Phase 5 (Tasks 34–39) is the next dispatch. It carries:
- A schema migration consolidating `rimsky_dispatch` + `rimsky_lock_holders` → `rimsky_worker_request` + `rimsky_claim_handle` (with `phase` and `is_held` discriminator columns).
- Collapsing the `Locks` driver interface into `WorkerRequests`.
- Updating integration code for active-phase + held-phase lifecycle.
- Updating modeling-layer references to the repointed FKs.

Phase 6 (Tasks 40–42) — orphan-reaper + terminal-decision unifications — and Phase 7 (Tasks 43–53) — documentation rewrites — follow.

## Dispatch 6 — Phase 5 strategy

**Context:** Dispatch 6 was tasked with Phase 5 (Tasks 34–39) plus optional Phase 6/7 follow-on. The surface is large: 58 files reference legacy `rimsky_dispatch` / `rimsky_lock_holders` / `rimsky_claim_holders`; the integration layer is 5161 lines; the persistence layer's queue + lock_holders + claim_holders implementations total 2513 lines across postgres + sqlite. The plan asks for collapsing three tables and three driver interfaces into two-of-each, plus updating integration-layer lifecycle, plus 4 new substantive scenario tests, all under a -race -count=3 gate.

**Approach attempted:** Push through Phase 5 as a unit. Pre-v1 break-freely is invoked aggressively: drop+recreate the schema and rewrite the persistence interfaces in a single pass. Where the existing Queue/LockHoldersStore/ClaimHoldersStore decomposition serves the new model (since the SQL is mostly a renaming exercise + adding a phase column), prefer renaming over extracting new abstractions. The naming target per the plan is `WorkerRequests` (single consolidated interface). Internal file layout: keep two files (worker_request.go for parent rows, claim_handle.go for child rows) per the cold-read ~500-line file guideline.

**Result: Phase 5 not landed.** The dispatch was burned recovering from a git-checkout misstep — see "Dispatch 6 — git-checkout misstep" below. After recovery, dispatch budget was insufficient to make a clean push of Phase 5. Working tree returned to its end-of-dispatch-5 (Phase 4 complete) state. No tasks 34-39 changes landed.

## Dispatch 6 — git-checkout misstep (lost work + recovered)

**What happened:**

The Phase-1-through-Phase-4 work has been entirely in the working tree (never staged, never committed). That's been the dispatch handoff pattern: each dispatch leaves work unstaged for the user to commit.

Within this dispatch, mid-Task-35, I started a Queue→WorkerRequests interface rename in `foundation/persistence/driver.go` and `foundation/persistence/worker_requests.go`. After making partial progress I decided to revert those edits to pursue a less invasive approach.

I ran `git checkout foundation/persistence/driver.go foundation/persistence/worker_requests.go` to revert. **`git checkout` restores from the index, not from "the previous saved state".** Since the working-tree's Phase-2/3/4 driver.go and worker_requests.go (now-renamed-from-queue.go-via-git-mv) had never been staged, the index held their pre-Phase-2 contents (post-`git mv` source preservation).

The checkout silently rolled both files back to their pre-Phase-2 state — losing:
- Phase 2's `Queue() Queue` / `Coordinator() Coordinator` → `Queue() Queue` / `AdvisoryLocker() AdvisoryLocker` rename;
- Phase 3's `region` → `scope` renames in advisory-locker methods (`TakeRegionLockInTx` → `TakeScopeLockInTx`);
- The `core/shared` → `modeling/shared` import path update.

**Recovery:**

Manually restored both files by re-typing the post-Phase-4 contents based on what the impl files (postgres/driver.go, sqlite/driver.go) expected. Build green; tests green; lint green again.

Similar problem hit on `foundation/persistence/sqlite/migrations/001-initial.sql`: had overwritten it as part of the in-progress Phase-5 work, then needed to restore. The staged version (from `git show :...`) turned out to be the pre-Phase-3 + pre-Phase-4 state (`region_data` not `scope_data`; `rimsky_store_lifecycle` not `rimsky_lifecycle_idempotency`). Restored to staged state, then forward-ported Phase 3 (region→scope, plus realized_write_semantics column) and Phase 4 (rimsky_store_lifecycle → rimsky_lifecycle_idempotency) edits manually.

**Surfaced for future dispatches:**

1. **Stage the working tree before destructive operations.** All Phase 1-5 work has been working-tree-only. A simple `git add -A` checkpoint before non-trivial refactors would prevent recurrence. Recommendation: at the start of any dispatch that intends destructive operations (broad rename, file moves), `git add -A` first to make the working tree the index baseline, so `git checkout <file>` restores to dispatch-start state, not pre-Phase-1 state. The user has been content to leave dispatches uncommitted; if that pattern continues, dispatches should be self-protective.

2. **Phase 5's scope estimate has been validated:** the consolidation (three tables, three driver interfaces, 58 referencing files, 5161-line integration layer, 4 new substantive scenario tests) is realistically 2-3 dispatches' worth of work. The next Phase-5 dispatch should plan to either (a) split itself across multiple sub-dispatches with explicit save-points, or (b) attempt the schema-only side first (Task 34) and stop, deferring 35-39 to follow-ons.

3. **The new schema design from my draft work survives in spirit:** the migration shape I sketched (rimsky_worker_request with phase column, rimsky_claim_handle with is_held flag) matches the plan's spec §8.4 and the foundation contract §5.4. Future dispatch can reference this dispatch's reasoning or write afresh.

**Tasks status this dispatch:**
- 34: not started — schema migration drafted then reverted.
- 35: not started.
- 36: not started.
- 37: not started.
- 38: not started.
- 39: not started.

**Final verification (Phase 5 not advanced; baseline reverified):**
- `go build ./...` — clean.
- `go test ./foundation/persistence/... -count=1 -short` — all packages green (postgres testcontainers + sqlite both exercise the restored schema).
