# Divergence Record — Instance Lifecycle: Durable-by-Default + Frame-End + Trace Retention

Audit of the working tree against `.ok-planner/plans/2026-06-03-instance-lifecycle-durable-by-default.md`
and `.ok-planner/specs/2026-06-03-instance-lifecycle-durable-by-default-design.md`.

The implementation is closely faithful to the plan: every named task, file, predicate
rewrite, migration, config knob, concept-doc edit, and acceptance gate landed as specified.
`go build ./...`, `make lint`, and the in-process suites (`sqlite`, `runtime`, `config`,
`signal`) all pass. The divergences below are all small — most are choices the plan
explicitly left open, recorded here for the record, not departures from explicit plan text.

## Divergences

### 1. `PruneTraceForRetention` disabled-bound handling via sentinel binds (unspecified mechanism)

- **What the plan said:** Task 15 step 2 directed implementing the reaper by deleting terminal
  frames "where the frame is terminal AND (`ended_at < cutoff` OR per-instance recency rank `>
  recentFramesKept`), combining the existing `PARTITION BY ... ORDER BY ended_at` window with the
  time predicate." It did not prescribe how a *disabled* bound (count `<= 0`, or zero `cutoff`)
  should be neutralized inside the single SQL.
- **What was implemented:** Both drivers neutralize a disabled bound with a sentinel value rather
  than two SQL variants. The count bound, when disabled, binds `countCap = 1 << 62` so the rank
  predicate never matches (`lib/foundation/persistence/postgres/frames.go:610-612`,
  `lib/foundation/persistence/sqlite/frames.go:153-157`). The time bound, when disabled, binds a
  NULL `timestamptz` (Postgres, guarded by `$2::timestamptz IS NOT NULL`) or the RFC3339 zero-time
  string (SQLite, guarded by `ranked.ended_at IS NOT NULL AND ranked.ended_at < ?`).
- **Inferred reason:** Keeps one SQL statement serving all three bound combinations instead of
  branching the query text, matching the existing single-DELETE idiom the method carried. The
  plan left the mechanism open; this is a reasonable fill.

### 2. `1 << 62` count-cap sentinel is a 64-bit-only constant (latent portability edge)

- **What the plan said:** Nothing — the sentinel is an implementation detail (see #1).
- **What was implemented:** `countCap = 1 << 62` assigned to a Go `int`
  (`lib/foundation/persistence/postgres/frames.go:610-612`,
  `lib/foundation/persistence/sqlite/frames.go:153-157`). On a 32-bit build target this untyped
  constant overflows `int` and fails to compile.
- **Inferred reason:** Rimsky ships in 64-bit containers, so the value compiles and behaves
  correctly on every real target (confirmed: `go build ./...` passes here). Recorded only because
  the sentinel is silently 64-bit-assuming; a 32-bit cross-compile would break.

### 3. SQLite event-sweep time binding differs between the two new accessors

- **What the plan said:** Task 16 step 1-2 said to add `DeleteOlderThan(ctx, cutoff)` to both
  `EventTable` (over `occurred_at`) and `NodeEventTable` (over `emitted_at`), "modeled on
  `LineageTable.DeleteOlderThan`." It did not address the SQLite TEXT-timestamp binding
  convention for each.
- **What was implemented:** `sqlite/events.go::DeleteOlderThan` binds `formatTime(cutoff)` (an
  RFC3339Nano string) for the `occurred_at < ?` comparison (`lib/foundation/persistence/sqlite/events.go:313`),
  while `sqlite/node_events.go::DeleteOlderThan` binds the raw `cutoff time.Time` for `emitted_at <
  ?` (`lib/foundation/persistence/sqlite/node_events.go:161`). Each matches its own table's write
  path — `events.Insert` writes `occurred_at` via `nowUTC()`/`formatTime` strings, and
  `node_events.Insert` binds a raw `time.Time` (`lib/foundation/persistence/sqlite/node_events.go:44`)
  — so each table's delete is self-consistent with its insert.
- **Inferred reason:** The implementer preserved each table's pre-existing time-binding convention
  rather than unifying them (CLAUDE.md "preserve each driver's existing idiom"). It is correct
  per-table, but the asymmetry between the two new methods is real and the gate test
  (`sqlite/trace_retention_test.go:185-199`) seeds `emitted_at` with the same raw-`time.Time`
  bind, so it cannot surface a mismatch between the raw-bind format and `formatTime` if one ever
  arose.

### 4. Engine test sets `terminate_after_run` via direct `UPDATE`, not the extended seed helper

- **What the plan said:** Task 8 step 1 offered two options for instance B in
  `TestDurableByDefaultVsTerminateAfterRun`: "extend the helper to set the column, or issue a
  direct `UPDATE rimsky_instances SET terminate_after_run = true WHERE id = …` after seeding."
- **What was implemented:** The direct-UPDATE option (`lib/graph/frame/engine_test.go:331-332`);
  `seedTemplateAndInstance` (`lib/graph/frame/producer_test.go:42`) was left unchanged. The test
  comment notes the seed path "now carries the flag" but the column "is also settable here so the
  test reads as a targeted lifecycle assertion."
- **Inferred reason:** Both options were sanctioned by the plan; the implementer took the
  lower-blast-radius one (no shared-helper signature change). Not a departure — recorded because
  the plan presented a fork.

### 5. Additional predicate-level driver tests beyond the plan's named RED tests

- **What the plan said:** The plan named specific RED tests per task: SQLite-only
  `TestParkedNodeRunHoldsFrameOpen` (Task 1), `TestQueuedFrameNotPromotedForTerminatedInstance`
  (Task 10), the Postgres engine test `TestDurableByDefaultVsTerminateAfterRun` (Task 8), and the
  whole-trace SQLite test (Task 13). It did not name per-driver `MarkInstanceTerminatedIfDone`
  unit tests or a Postgres mirror of the parked-hold test.
- **What was implemented:** Extra coverage was added: a Postgres mirror
  `TestPGParkedNodeRunHoldsFrameOpen` plus `TestPGMarkInstanceTerminatedIfDone{HoldsForParkedRun,
  FiresForTerminateAfterRun,SkipsDurableDefault}` (`lib/foundation/persistence/postgres/frames_parked_hold_test.go`)
  and SQLite equivalents `TestMarkInstanceTerminatedIfDone{HoldsForParkedRun,
  FiresForTerminateAfterRun,SkipsDurableDefault}` (`lib/foundation/persistence/sqlite/instance_terminate_parked_test.go`).
- **Inferred reason:** The spec's "Testing strategy" calls for predicate tests "in both drivers"
  for exactly these cases; the plan under-specified the per-driver unit coverage and the
  implementer filled it in. Additive, plan-aligned — recorded for completeness, not as a concern.

### 6. Idempotent-recreate flag behavior asserted by the spec is not covered by a test

- **What the plan said:** Neither Task 4 nor any other task required testing the idempotent
  re-create path. The *spec* (§1) states "Idempotent re-create (same `template_hash` +
  `instance_key`) ignores the flag on the request and returns the existing row's value, exactly as
  `paused` behaves."
- **What was implemented:** `TestTerminateAfterRunRoundTrip` covers create-with-flag and
  create-without-flag round trips (`lib/control/controlapi/instance_terminate_after_run_test.go`)
  but does not exercise a same-key re-create with a conflicting flag value.
- **Inferred reason:** The behavior falls out for free because the create path mirrors `paused`
  (re-create returns the existing row without re-binding the flag), and the plan never asked for
  the assertion. A load-bearing spec property is therefore unverified by an explicit test, though
  it is structurally guaranteed by the shared create path. Recorded as a coverage gap relative to
  the spec, not a plan divergence.
