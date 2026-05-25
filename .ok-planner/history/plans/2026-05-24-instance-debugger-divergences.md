# Instance-Debugger Plan — Divergences Report

**Plan:** `.ok-planner/plans/2026-05-24-instance-debugger.md`
**Spec:** `.ok-planner/specs/2026-05-24-instance-debugger-design.md`
**Audited:** 2026-05-24

This is a record of where the working tree diverges from what the plan
literally said. It is not a critique — separate review steps handle
correctness. Trivial naming, stylistic differences, and obvious
equivalents are skipped.

---

## 1. Pass 1 — `rimsky_named_locks` and `rimsky_migrations` omitted from consolidated schema

**What the plan said:** Task 3 step 1 (plan §line 76) listed the
expected tables in the consolidated `001-schema.sql` and explicitly
named `rimsky_migrations` and `rimsky_named_locks`.

**What was implemented:** Neither table appears in
`file:foundation/persistence/postgres/migrations/001-schema.sql` nor in
`file:foundation/persistence/sqlite/migrations/001-schema.sql`. A
header comment at lines 7-11 of each consolidated file documents the
omission for `rimsky_migrations` — created by the driver's `Bootstrap`
step in `code:foundation/persistence/{postgres,sqlite}/migrate.go`,
not declared in any user-facing migration. `rimsky_named_locks` is
silently absent.

**Inferred reason:** The prior `001-baseline.sql` on HEAD never
declared either table (confirmed via `git show
HEAD:foundation/persistence/postgres/migrations/001-baseline.sql`).
The plan listed them speculatively as expected schema; the implementer
correctly preserved actual schema state rather than introducing
phantom tables. Plan error.

---

## 2. Pass 1 — `idx_breakpoints_instance_active` partial-index predicate narrowed

**What the plan said:** Task 3 step 1 schema block (plan §line 106-108)
specified:
`WHERE expires_at IS NULL OR expires_at > NOW()`.

**What was implemented:**
`file:foundation/persistence/postgres/migrations/001-schema.sql#575-577`
uses only `WHERE expires_at IS NULL`. A comment at lines 569-574
explains that Postgres requires IMMUTABLE functions in index
predicates and `NOW()` is STABLE; the time-based filter is applied at
query time, with the sibling `idx_breakpoints_expires` index covering
the time-based path for the sweeper.

**Inferred reason:** Forced choice — the plan's literal SQL is
invalid under Postgres. Implementer resolved by narrowing the partial
index and pushing the time predicate to query-site WHERE clauses.

---

## 3. Pass 1 — Circular FK resolution between `rimsky_instances` and `rimsky_run_scopes`

**What the plan said:** Task 3 step 3 mentioned dependency-order
declaration; the plan did not call out the
`rimsky_instances.main_run_scope_id ↔ rimsky_run_scopes.instance_id`
mutual FK as needing special structuring.

**What was implemented:**
`file:foundation/persistence/postgres/migrations/001-schema.sql#176-177`
declares `rimsky_instances` without `main_run_scope_id`, then adds it
via `ALTER TABLE ... ADD COLUMN` after `rimsky_run_scopes` exists, with
`DEFERRABLE INITIALLY DEFERRED`. The SQLite parallel at
`file:foundation/persistence/sqlite/migrations/001-schema.sql` adds
`DEFAULT ''` on the ALTER (originally migration 010 in the SQLite
sequence had no `DEFAULT`; the consolidated baseline adds it to
support fresh-DB application).

**Inferred reason:** Forced choice — the mutual FK can't be
declared in a single `CREATE TABLE` block. The deferred-constraint +
post-creation ALTER pattern mirrors what original migration 010 did;
the `DEFAULT ''` on SQLite is novel (the original ALTER on a populated
table relied on pre-v1 "drop and recreate", but the consolidated
baseline runs on truly empty schema where no `DEFAULT` would also
work).

---

## 4. Pass 2 — `matcher.Validate` error messages aligned to existing test assertions

**What the plan said:** Task 8 step 2 (plan §line 662-706) gave
"natural wording" for each validator error, e.g.
`"matcher.node_type %q is not a declared node type"`,
`"matcher.executor %q is declared but not referenced by any template node"`,
`"matcher.graph %q (must be \"main\" or a declared sub-graph name)"`,
`"matcher.graph %q is not admissible for legacy flat templates ..."`.

**What was implemented:**
`file:foundation/matcher/validate.go#83-117` uses substrings the
existing tests already assert on: `"unknown node"`, `"unknown executor
name"`, `"unknown graph"`, `"no declared sub-graphs"`, `"executor not
referenced by any template node"`. The error-substring expectations at
`file:control/controlapi/attribute_overrides_test.go#69,104,150,409,422,435,459,485`
remain unchanged.

**Inferred reason:** Spec intent override — the implementer chose
to preserve existing test assertions over the plan's literal wording.
Lowest-risk alternative was to align validator output with test
expectations rather than touching dozens of test substrings.

---

## 5. Pass 4 — Resume schema lookup uses snapshot fallback path, not the runner-dispatch wiring

**What the plan said:** Task 20 step 1 (plan §line 1148-1165) noted
two implementation paths for `lookupEffectiveSchemaForHit` and called
the snapshot-write side "an acceptable fallback" if reusing the
runner-dispatch path required "substantial refactor".

**What was implemented:**
`file:runtime/breakpoint_resume.go#136-149` reads
`snapshot.effective_schema` directly from the hit row's snapshot map;
no runner-dispatch wiring at all. Pass 5's `buildSnapshot`
(`file:runtime/breakpoint_eval.go#293-296`) populates the field from
`CheckpointContext.EffectiveSchema`, which is threaded in by
`file:runtime/runner_dispatch.go#483`. When the field is absent the
resume path logs a Warn and skips pre-merge validation, deferring to
the supervisor-side defense-in-depth gate.

**Inferred reason:** Cleaner shape — the plan authorized this
fallback explicitly. Avoids a runner-dispatch-side helper that would
have crossed the Pass 4 / Pass 5 boundary.

---

## 6. Pass 4 — `CheckpointContext.EffectiveSchema` is a new field; plan pseudocode omitted it

**What the plan said:** Task 23 step 1 (plan §line 1272-1287) listed
the `CheckpointContext` struct fields; no `EffectiveSchema` field
appeared in the pseudocode.

**What was implemented:**
`file:runtime/breakpoint_eval.go#70-79` adds `EffectiveSchema
map[string]any` with a doc comment explaining its role in
resume-time overlay validation. This is the carrier for divergence #5
above.

**Inferred reason:** Cleaner shape — the field exists only to
serve the snapshot-fallback path in divergence #5, which the plan
authorized at §line 1165.

---

## 7. Pass 4 — `instances.SetPaused` shapes differ between Postgres and SQLite

**What the plan said:** Task 18 step 4 (plan §line 1058) specified
`SetPaused(ctx, instanceID, paused bool, tx) (priorValue bool, err
error)` on InstanceTable with no shape prescription.

**What was implemented:**
- Postgres
  (`file:foundation/persistence/postgres/instances.go#396-422`) uses a
  two-step `SELECT ... FOR UPDATE` + `UPDATE` pattern inside the
  caller's tx: the SELECT acquires the row lock before the UPDATE runs,
  so two concurrent `SetPaused(true)` callers serialize and the second
  observes prior=true (driving the handler's 409 path). The
  implementation also skips the UPDATE when the row is already at the
  requested value (no WAL traffic on no-op toggles).
- SQLite
  (`file:foundation/persistence/sqlite/instances.go#353-376`) uses a
  plain SELECT-then-UPDATE inside the caller's tx, relying on `BEGIN
  IMMEDIATE` writer serialisation for atomicity (commented).

**Inferred reason:** Cycle 2 follow-through. The original Postgres
implementation used a CTE-based atomic prior-value capture (`WITH prev
AS (SELECT paused...), upd AS (UPDATE...) SELECT prev.paused FROM
prev, upd`); that pattern was tried and abandoned because the CTE
bound `prev` to the statement-level snapshot — both concurrent
`SetPaused(true)` racers observed the pre-update `paused = false` and
both reported `prior=false`, hiding the 409 idempotency case. The
two-step `SELECT FOR UPDATE` + `UPDATE` under the caller's tx is the
standard "read the row, decide based on it, write" pattern and
restores the 409 distinction. SQLite was already on a two-statement
shape (CTE with UPDATE doesn't return the pre-update value cleanly
under modernc.org/sqlite); cycle 2 brought the Postgres shape into
alignment.

---

## 8. Pass 4 — `SelectCandidates` row-lock narrowed to `FOR UPDATE OF d`

**What the plan said:** Task 19 step 2 (plan §line 1070) said add the
join + `AND i.paused = false`; no mention of the `FOR UPDATE` row
target.

**What was implemented:**
`file:foundation/persistence/postgres/queue.go#235` changed the lock
clause from the plan-implied `FOR UPDATE SKIP LOCKED` to `FOR UPDATE
OF d SKIP LOCKED`, so the new `rimsky_instances` JOIN does not extend
the lock target. The lock stays on `rimsky_node_runs d` only.

**Inferred reason:** Cleaner shape — implementer correctly
recognized that adding a JOIN without scoping the FOR UPDATE would
take a row lock on the instance row each tick, contending with normal
instance-row writes (e.g., pause/resume API calls). Narrowing is
required for correctness of the new join.

---

## 9. Pass 4 — GET `/instances/{id}` projection surfaces `paused`

**What the plan said:** Task 17 (plan §line 1010-1037) covered the
create path's `paused` flag handling; it did not explicitly authorize
extending the GET response shape.

**What was implemented:**
`file:control/controlapi/instances.go#140,152` adds `Paused bool
json:"paused"` to the `instanceItem` JSON projection used by GET
responses, with the field populated from `InstanceRow.Paused`.

**Inferred reason:** Cleaner shape / natural completion — adding a
new persisted column without surfacing it in the GET projection would
leave operators unable to inspect the pause state via the normal read
path. Implementer treated this as the obvious complement to the create
path.

---

## 10. Pass 5 — Typed-enum constants used instead of plan's bare strings

**What the plan said:** Task 23's pseudocode (plan §line 1359, etc.)
referenced `"notify_only"`, `"drop_oldest"`,
`"block_dispatch"`, `"auto_resume_after_ttl"` as bare strings.

**What was implemented:**
`file:runtime/breakpoint_eval.go#172,216,223` uses the typed
constants `persistence.BreakpointModeNotifyOnly`,
`persistence.OverflowDropOldest`,
`persistence.OverflowBlockDispatch`,
`persistence.OverflowAutoResumeAfterTTL` (declared by Pass 1 Task 5).

**Inferred reason:** Cleaner shape — Pass 1 introduced the typed
constants to be the canonical Go-side surface; using bare strings
here would have re-introduced the stringly-typed surface the constants
exist to eliminate.

---

## 11. Pass 5 — Callback path's after_terminal `Graph` / `ChildKey` documented as potentially empty

**What the plan said:** Task 25 step 3 (plan §line 1617) said "Mirror
at `runtime/callback.go:594` — same shape, possibly different local
variable names".

**What was implemented:**
`file:runtime/callback.go#615-635` resolves `scope` via
`resolveAcqScope(ctx, args, acq)` and passes `acq.GraphName` /
`scope.PartitionKey`. The call-site comment at lines 609-614
documents that "AsyncContext does NOT carry GraphName /
scope.PartitionKey (the callback path skips L5 attribute overrides per
the comment at acq construction), so the matcher's graph / child_key
keys evaluate against empty strings — callers writing breakpoints
intended to fire on async-callback terminals should leave those keys
absent (wildcard) per spec §4.4."

**Inferred reason:** Cleaner shape — implementer surfaced a
behavioral subtlety the plan didn't flag (the callback path's scope
resolution doesn't fully populate the matcher's fields) as a call-site
comment for future readers.

---

## 12. Pass 8 — `TestConcurrentFrameCorrectness` uses `FrameResolutionCoalesce`

**What the plan said:** Task 39 (plan §line 1945-1951) referred to
spec §10.2 "Concurrent-frame correctness" without specifying the
frame-resolution mode.

**What was implemented:**
`file:test/scenarios/breakpoints/concurrent_frame_correctness_test.go#52-57`
explicitly sets `FrameResolutionMode: node.FrameResolutionCoalesce`
with a comment: "Coalesce so both root nodes (worker_a and worker_b)
live in the SAME frame — that's the only mode in which both root
dispatches can be in-flight concurrently. Under serial_queue each
root gets its own frame and frames are dispatched one at a time, which
would serialize worker_a (paused) and worker_b."

**Inferred reason:** Forced choice — the default `serial_queue`
would have serialised the two roots and defeated the scenario's
purpose (verifying breakpoint blocking is per-dispatch, not
per-instance).

---

## 13. Pass 8 — `TestHitQueueOverflowDropOldest` drives `runtime.EvaluateBreakpoints` directly

**What the plan said:** Task 40 (plan §line 1955-1957) said "150
dispatches, 50 dropped, dropped_count = 50" suggesting real
dispatches.

**What was implemented:**
`file:test/scenarios/breakpoints/hit_queue_overflow_drop_oldest_test.go#43-99`
brings up the harness with `NoSupervisor: true` + `NoScheduler: true`,
creates the instance paused, and invokes `runtime.EvaluateBreakpoints`
directly 150 times with a rotating synthetic dispatch ID. A comment
explains: "We drive `runtime.EvaluateBreakpoints` directly against the
harness's persistence rather than going through 150 real dispatches
— that keeps the test focused on the overflow/eviction contract (the
per-dispatch runtime is exercised by the other scenarios)."

**Inferred reason:** Cleaner shape — testing 150 actual dispatches
would be substantially slower and exercise per-dispatch surfaces the
other scenarios already cover; the overflow contract is purely about
the evaluator + persistence interaction.

---

## 14. Pass 8 — `helpers_test.go` shared across breakpoint scenarios

**What the plan said:** No plan task enumerated a shared helpers
file under `test/scenarios/breakpoints/`.

**What was implemented:**
`file:test/scenarios/breakpoints/helpers_test.go` (253 lines) provides
shared HTTP+persistence shims: `breakpointCreate`, `breakpointResume`,
`breakpointDelete`, `instancePause`, `instanceResume`, `postJSON`,
`waitForHitOnBreakpoint`, `waitForHitCount`, `getBreakpointRow`,
`getHitRow`, `stubObservedCount`, `waitForStubObservedCount`,
`createInstanceWithPause`.

**Inferred reason:** Cleaner shape — used by ~12 scenario files; the
duplication threshold (cold-read guideline: 3+ call sites) is clearly
met. Earns its keep.

---

## 15. Pass 8 — `select_candidates_paused` test placed in conformance harness, not in the per-backend queue test

**What the plan said:** Task 19 step 5 (plan §line 1073) said "Add a
persistence-level test in `foundation/persistence/postgres/queue_test.go`
... Mirror for SQLite."

**What was implemented:**
`file:foundation/persistence/conformance/select_candidates_paused.go`
is a single test in the cross-driver conformance harness, wired into
`file:foundation/persistence/conformance/conformance.go#62` as
`testSelectCandidatesSkipsPausedInstances`. The test runs against
both Postgres and SQLite from a single source.

**Inferred reason:** Cleaner shape — a per-backend pair of duplicate
tests is what the conformance harness exists to avoid.

---

## 16. Pass 9 — `concepts.md` TOC was not regenerated

**What the plan said:** Task 53 step 3 (plan §line 2157) said "this
file is auto-generated and refreshed by `execute-plan` when a plan
touches `concepts/`. The new `concepts/breakpoint.md` file ... will
trigger the regeneration when `execute-plan` finishes this plan. **Do
not hand-edit `concepts.md`**".

**What was implemented:** `file:.ok-planner/design/concepts.md` does
not mention `breakpoint`. The new concept file
`file:.ok-planner/design/concepts/breakpoint.md` exists, but the TOC
entry was never added. Per the plan's own instruction, this is "a
generator bug to surface, not something to paper over with a
hand-edit." Recording for visibility.

**Inferred reason:** End-of-plan generator step skipped (or
generator absent). Plan-authorized "do not hand-edit" stance left the
TOC stale.

---

## 17. Out-of-plan working-tree changes

The following modifications appear in the working tree but are not
part of this plan's scope (they trace to the `2026-05-24-parallel-worktree-dev-ports`
sketch):

- `file:control/cli/embedded/deploy/docker-compose.yml` — env-var
  overridable host ports.
- `file:control/cli/embedded/deploy/store-filesystem.yml`
- `file:deploy/docker-compose.yml`
- `file:deploy/dev-up.sh` (new) — dev launcher script.

Not divergences from this plan; flagged for completeness so reviewers
don't mistake them for breakpoint-related work.

---

## Items that match the plan

The following plan elements landed substantively as written, with no
meaningful divergence:

- All 6 new action verbs in `code:control/controlapi/actions.go::v1Actions`.
- `code:control/cli/roles/debug-operator.json` matches spec §8 verbatim.
- All 5 new error sentinels in
  `code:foundation/shared/errors.go#30-34`.
- HTTP error mappings in
  `code:control/controlapi/app.go::writeError#286-308`.
- The 4 breakpoint route registrations + the 2 pause/resume routes,
  the `paused: true` flag on `POST /instances`, and the GET projection.
- The MCP `resources/list` / `resources/read` dispatch + the
  `breakpointResourceCatalog` Layer-3 isolation per spec §11.
- The `foundation/matcher` package extraction, the delegation from
  `runtime/attribute_overrides.go::evaluateMatcher`, and the
  re-wrap-as-`errAttributeOverridesInvalid` at the by_match
  validator boundary.
- Reaper integration on the scheduler tick (12th sweep position).
- 14 scenario test files under `test/scenarios/breakpoints/` covering
  spec §10.2 cases.
- 9 concept-doc Notes entries and the new
  `.ok-planner/design/concepts/breakpoint.md`.
- The `signal_type` filter prefix-match using
  `signal.ValidateSubscriptionType` at create-time and
  `TypePath.HasPrefix` at evaluator-time.
- Cascade-delete behavior on `rimsky_breakpoint_hits.breakpoint_id`
  unblocking `waitForResume` (treated as auto-resume with no overlay).
