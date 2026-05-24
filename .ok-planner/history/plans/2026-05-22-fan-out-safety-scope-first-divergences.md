# Divergences — 2026-05-22-fan-out-safety-scope-first

Audit of working-tree differences vs. the literal plan text at
`.ok-planner/plans/2026-05-22-fan-out-safety-scope-first.md`. Each entry
records the textual instruction, what actually landed, and the inferred
reason. Not a critique — a separate review step covers correctness.

---

## 1. Park terminal: kept existing `Park` message; only collapsed the enum

**Plan said (Task 45):**
> Update `ParkTerminal` message:
> ```proto
> message ParkTerminal {
>   ParkReason reason = 1;
>   optional google.protobuf.Timestamp resume_at = 2;
>   optional string reason_label = 3;
> }
> ```

**What was implemented:** The existing `message Park` was kept with its
six fields (`reason`, `payload`, `resume_at`, `session_token`,
`reason_note`, `reason_label`). Only the `enum ParkReason` was collapsed
to two values. See `protocols/proto/v1/executor.proto:259-280`.

**Inferred reason:** `payload`, `session_token`, and `reason_note` are
load-bearing: `payload` and `session_token` flow back to executors as
`ResumeContext.payload` / `ResumeContext.session_token`; CLI/diagnostics
read `reason_note`. Renaming `Park`→`ParkTerminal` and dropping those
fields would have broken the resume-roundtrip path. The plan text appears
to have under-specified — `ParkTerminal { reason, resume_at,
reason_label }` would silently delete the resume payload contract.

---

## 2. Extra migration 012 for prior_dispatch persistence

**Plan said:** Migration numbers 007–011 were enumerated (Tasks 1, 2, 3,
4, 5, 6, 33.5, 46). No migration 012 was enumerated.

**What was implemented:** New migration
`foundation/persistence/postgres/migrations/012-node-runs-prior-dispatch.sql`
and the SQLite parallel add `prior_dispatch_id` (FK to
`rimsky_node_runs.id` with `ON DELETE SET NULL`) and
`prior_dispatch_disposition` (TEXT CHECK ∈ {`heartbeat_stale`,
`retry_after_error`, `recalculate`}) on `rimsky_node_runs`.

**Inferred reason:** Task 43 added the proto fields `prior_dispatch_id`
and `prior_dispatch_disposition` on `ExecuteRequest`, and Task 44 wired
them into supervisor dispatch sites. Persistence is necessary so a
recovered/restarted supervisor can re-emit the right values on a future
dispatch — the plan implicitly assumed columns existed.

---

## 3. Fan-out RunScope creation lives in `CreateFanOutChildren`, not `AcquireSubClaims`

**Plan said (Task 35):**
> Extend `AcquireSubClaimsInput` with `ParentRunScopeID`, `InstanceID`,
> `ParentGraphName`. Extend `SubClaim` with `RunScopeID`. In
> `AcquireSubClaims`, in the same tx as the sub-claim handle inserts,
> create a `fanout_partition` RunScope per partition descriptor.

**What was implemented:**
- `AcquireSubClaimsInput`
  (`runtime/runner_subclaim.go:45-83`) was NOT extended with
  `ParentRunScopeID`/`InstanceID`/`ParentGraphName`.
- `SubClaim` (`runtime/runner_subclaim.go:87-93`) does NOT carry
  `RunScopeID`.
- The fanout_partition RunScope is created in
  `CreateFanOutChildren` (`runtime/fanout_dispatch.go:257-306`), which
  also handles the get-or-create against
  `RunScopeTable.GetFanoutPartition`.

**Inferred reason:** `AcquireSubClaims` operates on
`rimsky_claim_handles` (claim-tree side) and runs inside the parent
acquisition tx. The actual rimsky_node_runs creation for fan-out
children happens later, in `CreateFanOutChildren`, after the canonical
`FanOutChildRunPlan` projection. Putting RunScope creation alongside
claim-handle inserts would have spread the new run-scope-tree logic
across two functions with no benefit, since fan-out child runs are
created in `CreateFanOutChildren` regardless. The plan appears to have
confused the claim-tree side with the run-scope-tree side.

---

## 4. Callback determinism: `applyTerminal` runs outside the phase-check tx

**Plan said (Task 41):**
> ```go
> err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
>     row, err := args.Persist.Nodes().GetRunByDispatchIDForUpdate(ctx, dispatchID, tx)
>     ...
>     return applyTerminal(ctx, row, terminal, tx)
> })
> ```

**What was implemented:** The phase-check tx is its own transaction;
`applyTerminal` runs after the tx commits and opens its own state-mutation
transactions internally. See `runtime/callback.go:486-590` with
explicit comment block at lines 527-537 acknowledging:
> "there is a narrow TOCTOU window where a concurrent sweep could
> transition the run between the check and applyTerminal's first
> state-write tx... Refactoring applyTerminal to accept a tx and run
> inside the determinism tx is a larger restructure deferred past
> Phase B."

The `@blessed-invariant: Callback determinism` annotation is still
present.

**Inferred reason:** `applyTerminal` and its `applyTerminal*` dispatch
family open their own transactions internally
(`runtime/runner_terminal.go:122,208,278,752`). Threading an outer tx
through the entire family would have been a substantial refactor;
the implementer documented the TOCTOU window as a known narrow gap
(bounded by `claimed_by`) and deferred the full refactor.

---

## 5. Deleted conformance file `nodes_mark_stale_for_cascade.go`

**Plan said:** Not in Task 32's enumerated retirement list (which
covered six fanout-disambiguator conformance files).

**What was implemented:**
`foundation/persistence/conformance/nodes_mark_stale_for_cascade.go`
deleted; `t.Run("NodesMarkStaleForCascade", …)` removed from
`conformance.go::Suite` and replaced with a comment block (lines 66-72)
explaining that the test pinned the old `(nodeID, frameID, tx) (bool, error)`
signature with allocation-via-insert semantics — both of which were
retired by Task 39's simplification to a pure UPDATE keyed on `runID`.

**Inferred reason:** Task 39 made the test inexpressible. Replacement
coverage is `AffirmNodeRunRow` (Task 29).

---

## 6. Deleted `TestParkedLifecycleUnspecifiedReasonRejected`

**Plan said:** Not enumerated as a deletion target.

**What was implemented:** The test body is removed; a comment at
`test/scenarios/parked_lifecycle_test.go:208-213` documents the
retirement.

**Inferred reason:** Task 45 collapsed the enum, eliminating
`PARK_REASON_UNSPECIFIED`. proto3's zero-value slot now holds
`PARK_REASON_AWAIT_CALLBACK`. The "reject unspecified" runtime test is
no longer expressible.

---

## 7. Mutual-FK `DEFERRABLE INITIALLY DEFERRED` on migrations 007 and 010

**Plan said (Tasks 1, 33.5):** Plain `REFERENCES` clauses, no
deferrability directive.

**What was implemented:** Both migrations declare the FKs
`DEFERRABLE INITIALLY DEFERRED`:
- `foundation/persistence/postgres/migrations/007-run-scopes.sql:18`
  (`rimsky_run_scopes.instance_id`)
- `foundation/persistence/postgres/migrations/010-instances-main-run-scope.sql:11`
  (`rimsky_instances.main_run_scope_id`)
- Same on the SQLite parallels.

**Inferred reason:** The controlapi POST /instances handler creates
the RunScope and the Instance in the same tx; each row references the
other's id. Without deferrable FKs, either insert order fails the
constraint at INSERT time. Discovered during Phase F1 test runs.

---

## 8. SQLite migration 008 ordering: `child_key` before `parent_run_id`

**Plan said (Task 4):** Listed both `DROP COLUMN parent_run_id` and
`DROP COLUMN child_key` without specifying order.

**What was implemented:**
`foundation/persistence/sqlite/migrations/008-node-runs-run-scope-id.sql:19-20`
drops `child_key` first, then `parent_run_id`, with a multi-line comment
(lines 14-18) explaining: "child_key carries a column-inline CHECK that
references parent_run_id (`parent_run_id IS NULL OR child_key IS NOT
NULL`). SQLite validates remaining CHECK clauses after each DROP COLUMN;
dropping parent_run_id first would leave the child_key CHECK dangling
and fail with `no such column: parent_run_id`."

**Inferred reason:** Forced by SQLite's CHECK-validation semantics
during ALTER TABLE; discovered during migration execution.

---

## 9. SQLite migration 008: `DEFAULT ''` on `run_scope_id NOT NULL`

**Plan said (Task 4):** `ALTER TABLE rimsky_node_runs ADD COLUMN
run_scope_id TEXT NOT NULL REFERENCES rimsky_run_scopes(id)`. No
DEFAULT.

**What was implemented:** Added `DEFAULT ''` at line 23 of the SQLite
migration.

**Inferred reason:** SQLite cannot add a `NOT NULL` column without a
DEFAULT to a populated table. The pre-v1 break-freely posture says this
shouldn't happen in dev (drop-and-recreate), but the migration runs
through the unmodified migrator path; the sentinel `''` value satisfies
the constraint at ADD COLUMN time and is meaningless because the table
is empty when this migration runs.

---

## 10. Extra index `idx_node_runs_run_scope` on postgres migration 008

**Plan said (Task 3):** Created one new unique partial index
`uq_node_runs_in_flight_per_run_scope` plus dropped the old indexes.

**What was implemented:**
`foundation/persistence/postgres/migrations/008-node-runs-run-scope-id.sql:26`
also creates `CREATE INDEX idx_node_runs_run_scope ON rimsky_node_runs
(run_scope_id)`. Same on the SQLite parallel (line 29).

**Inferred reason:** Tree-walk / fan-out aggregation queries scan
`rimsky_node_runs` by `run_scope_id`. The partial unique index only
covers in-flight phases; an unconditional secondary index supports
forensic, aggregation, and lineage queries against terminated runs.

---

## 11. `LockKindScope` Go constant kept its name; only the string value changed

**Plan said (Task 5/6):** Updated CHECK enum from `'scope'` to
`'claim_scope'` at the SQL layer.

**What was implemented:** At the Go layer in
`foundation/persistence/claim_handles.go:19-29`, the constant is still
named `LockKindScope` but its value is `"claim_scope"`. Comments at
lines 21-27 document the intentional asymmetry.

**Inferred reason:** Renaming the Go identifier would have rippled
through every call site (Task 81-class noise). The string value is what
matches the migration-009 CHECK, and that's the only place where the
constant actually needs to agree with the schema. Implementer judged
the rename ergonomically expensive without semantic gain.

---

## 12. New method `GetFailedTerminalRunScopeID` on NodeTable

**Plan said:** Not enumerated.

**What was implemented:** New method on `NodeTable`
(`foundation/persistence/nodes.go:178-190`) with postgres + sqlite
impls (`postgres/nodes.go:699`, `sqlite/nodes.go:567`). Used by
`control/controlapi/nodes.go:255` in the operator-reset path.

**Inferred reason:** Under RunScope-first, `NodeRow.RunScopeID` only
surfaces the in-flight RunScope (nil for a failed node). The reset path
needs to reset `last_outcome` on the most-recent **failed-terminal**
row, which lives in a different RunScope than the new one being
re-dispatched. The method exists to plug this lookup hole.

---

## 13. Phase G real bugs found and fixed during verification

**Plan said (Task 88):** "If any audit returns findings, address them
before declaring the plan complete." (No bug list specified.)

**What was implemented:** Six bugs discovered during verification
sweeps and fixed in scope:

1. **`MarkSourceNodeStale` missing `run_scope_id` in INSERT** — fix on
   both postgres and sqlite backends.
2. **`SweepStaleHeartbeats` zombie-row retire** — added explicit
   `RemoveForNodeInTx` step at `runtime/conductor.go:124-132` to retire
   the zombie row so the `(node_id, run_scope_id)` in-flight slot frees
   up before the recovery Enqueue (the NOT EXISTS guard on the new
   unique index blocks otherwise).
3. **`pullHardDepUpstreams` parked-probe ordering** — reordered so the
   parked-receiver probe runs before the cascade-walk continuation.
4. **`cascadeMessageSubscribersInTx` missing AffirmNodeRunRow per receiver** —
   added at `runtime/message_delivery.go:304`.
5. **`pure_cascade::transitionPureCascade` affirm-then-recalculate** —
   added the affirm step at `graph/scheduler/pure_cascade.go:203`
   before recalculation.
6. **`applyTerminalCompleteSubgraphExit` sub-graph exit cascade bridge missing** —
   restored the cascade bridge at the exit terminal.
7. **Instance `Delete` runtime FK cascade** — both postgres and sqlite
   `instances.Delete` (postgres/instances.go:215, sqlite/instances.go:203)
   now explicitly DELETE through the run-scope tree because the FKs on
   `rimsky_run_scopes.instance_id` and `rimsky_node_runs.run_scope_id`
   were declared without ON DELETE CASCADE and SQLite can't ALTER FK
   semantics. The runtime cascade preserves the contract that deleting
   an instance drops everything beneath it.

**Inferred reason:** Project rule "Fix Every Bug You Find" — bugs
exposed by the reshape were fixed in scope rather than logged.

---

## 14. Phase G test adjustment: `TestScheduler_StaleHeartbeat_Reenqueues`

**Plan said:** Not enumerated as a test change.

**What was implemented:**
`graph/scheduler/scheduler_test.go:276-285` changed from "total
rimsky_node_runs row count = 1" to "in-flight row count
(phase ∈ {pending,active,held,parked}) = 1". The test comment
documents the rationale: under the new SweepStaleHeartbeats behavior,
two rows exist (retired zombie at phase='completed' + new pending row);
the in-flight count is the right assertion.

**Inferred reason:** Consequence of the zombie-row retire fix
(divergence #13.2 above).

---

## 15. Test surfaces added: `ObservedRequest`, `CallbackRegistry()`, `GetMainRunScopeID`, `SCENARIO_DEBUG`

**Plan said:** Task 55 mentioned the existing `Stub.Observed()`
recording; otherwise these surfaces were not enumerated.

**What was implemented:**

- `executors/stub/stub.go::ObservedRequest` (lines 122-131) gained three
  fields: `DispatchID`, `PriorDispatchID`, `PriorDispatchDisposition`.
- `runtime.Handle.CallbackRegistry()` exposed at
  `runtime/supervisor.go:191-200`.
- `config.SupervisorHandle.CallbackRegistry()` exposed at
  `control/config/supervisor.go:88` (with adapter
  `supervisorHandleWithRegistry` at line 173).
- `graph/scenario/harness.go:541-563` added
  `Harness.GetMainRunScopeID(instanceID)`.
- `graph/scenario/harness.go:211-214` added `SCENARIO_DEBUG=1` env
  switch for surfacing supervisor logs during scenario debugging.

**Inferred reason:** Test scaffolding for the new E2E scenarios
(F1-F4 and S1-S4) and conformance tests required these surfaces; they
weren't enumerated because the plan focused on production paths.

---

## 16. `Task 35` claim-tree fields preserved on input

**Plan said (Task 35 step 2):** Extend `AcquireSubClaimsInput` with
three new fields (`ParentRunScopeID`, `InstanceID`, `ParentGraphName`).

**What was implemented (related to divergence #3 above):** The fields
were NOT added to `AcquireSubClaimsInput`. The struct retains its
existing claim-tree-oriented shape. The new fields the plan called for
were added downstream on `FanOutChildRunPlan` / `CreateFanOutChildren`
parameters instead.

**Inferred reason:** Same as divergence #3 — the function operates on
the claim-tree side, not the run-scope-tree side. The plan's
recommended extension would have been dead context inside
`AcquireSubClaims`.

---

## 17. New `runtime/runner_acquire_scope.go` (untracked)

**Plan said:** Not enumerated.

**What was implemented:** New file `runtime/runner_acquire_scope.go`
holds RunScope-related helpers shared by the acquisition path.

**Inferred reason:** Refactor to keep `runner_acquire.go` under the
~500-line cold-read guideline as RunScope-related helpers grew during
Phase B.

---

## 18. `Task 41` retired `populateAcquisitionLineageFields`; populate from row in same tx

**Plan said (Task 41):**
> Retire `populateAcquisitionLineageFields` (or update it to do the
> simpler RunScope-first lookup).

**What was implemented:** Replaced with two narrower helpers:
- `acq.RunScopeID` is populated **inside** the phase-check tx directly
  from the `NodeRunRow`'s `run_scope_id` field
  (`runtime/callback.go:570`).
- `populateInstanceLineageFields` (lines 588) opens a separate
  read-only tx for the instance-row fields only (`template_hash`,
  instance params).

Comment at lines 567-569 explains: "no separate RunTree.GetByID needed
under RunScope-first; the row carries run_scope_id."

**Inferred reason:** Cleaner separation: the determinism tx owns the
phase check + scope resolution; the lineage population is read-only and
can run after. Splits the old `populateAcquisitionLineageFields` along
the natural tx boundary.

---

## 19. `Task 38` cascade walker: comment on `applyTerminal` tx structure

**Plan said:** Pseudocode for the affirm-then-read pattern with
`wakeParkedReceiverInTx` chained inline.

**What was implemented:** Matches the plan's affirm-then-read pattern.
Additional explicit `AffirmNodeRunRow` calls landed in
`cascadeMessageSubscribersInTx` (per divergence #13.4) — those weren't
enumerated in the plan but the same reshape applies.

**Inferred reason:** The plan focused on `cascadeSubscribersStaleInTx`
and `pullHardDepUpstreams`; `cascadeMessageSubscribersInTx` is the
parallel structure on the unified message-pass path and needed the
same fix.

---

## 20. Conformance subtests grouped under named parents

**Plan said (Tasks 28-31):** Each was a single subtest registration.

**What was implemented:** Each is grouped under a named parent in
`foundation/persistence/conformance/conformance.go:78-104` with nested
`t.Run(...)`. E.g., `RunScopeLifecycle` parents five sub-cases; nine
sub-cases under `RunStateWritesIsolated`.

**Inferred reason:** Better Go-test output organization; the plan
implicitly described the test-case set without prescribing a flat vs.
hierarchical registration shape.

---

## 21. Migrations 007-012 are untracked, not staged

**Plan said:** Tasks 1, 2, 3, 4, 5, 6, 33.5, 46 each call out the
migration files explicitly.

**What was implemented:** All migration files exist on disk but appear
as untracked in `git status` (they have never been `git add`ed). Same
for the new `_test.go` files under `runtime/` and `test/scenarios/`,
the new `foundation/persistence/run_scopes.go`, the new conformance
files, and the new concept doc `claim-scope.md` / `run-scope.md`.

**Inferred reason:** Not a divergence in behavior — only in the git
staging state. The implementer's workflow apparently leaves new files
untracked until a final commit step. Recorded so the reviewer doesn't
miss any of them.

---

## Items NOT divergent (sanity checks)

These were explicitly investigated and confirmed faithful to the plan:

- All 11 migrations 007-012 in both backends carry the schema spelled
  out by the plan (modulo divergences #2, #7-#10 above).
- Park `enum ParkReason` collapsed to two values; `@blessed-invariant`
  block present.
- `prior_dispatch_id` (field 14) and `prior_dispatch_disposition`
  (field 15) + `PriorDispatchDisposition` enum on `ExecuteRequest`.
- ClaimScope rename swept across all enumerated sites (Tasks 80–83);
  no remaining bare `scope` claim-bytes identifiers per Audit G.
- New concept docs `run-scope.md` and `claim-scope.md`; old `scope.md`
  deleted; `concepts.md` TOC updated.
- All 9 enumerated conformance test files exist, registered, named per
  plan.
- All 8 enumerated E2E scenarios (F1-F4, S1-S4) exist as files.
- `MarkStaleForCascade` simplified to `(runID, frameID, tx) error`.
- `AffirmNodeRunRow` interface + impls per spec.
- `@blessed-invariant` annotations at the four enumerated sites (Tasks
  50.5, 51, 52, 53).

---

**End of divergence record.** 21 meaningful divergences, mostly forced
choices (mutual-FK deferral, SQLite ALTER ordering, bug fixes
discovered in scope) or cleaner-shape substitutions (RunScope creation
in `CreateFanOutChildren`, `applyTerminal` tx structure). One
under-specified plan item (Park message shape, #1) and one
plan-omitted-but-necessary migration (012 for prior_dispatch, #2).
