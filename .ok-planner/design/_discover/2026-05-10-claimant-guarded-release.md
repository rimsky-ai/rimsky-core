---
topic: claimant-guarded-release
kind: invariant
---

# Every DELETE and claim-release predicate gates on `holder_supervisor_id = supervisor_id`

## Description

Every `DELETE FROM rimsky_claim_handle` and every `UPDATE rimsky_worker_request SET claimed_by = NULL` carries an `AND … = supervisor_id` predicate. The heartbeat-refresh `UPDATE` inherits the same predicate (`AND holder_supervisor_id = $1`) even though it's a write rather than a release, so that a stale heartbeat from a foreign supervisor cannot extend a row it doesn't own.

This is the claimant-guarded-release rule, annotated at:

- `foundation/integration/supervisor.go:25-38` (the integration-side comment block).
- `foundation/persistence/postgres/claim_handles.go:16-19` (claim-handle CRUD).
- `foundation/persistence/postgres/queue.go:265-309` (`ReleaseClaim`).
- `foundation/persistence/sqlite/claim_handles.go:7-8` and `foundation/persistence/sqlite/queue.go:354` (SQLite mirror).
- `foundation/integration/orphan_reaper.go` (orphan reaper sites use the same predicate).

The rule's value is concentrated at the orphan-reaper site: the periodic sweep runs every tick (default `5 × heartbeat_interval` cutoff — `foundation/integration/conductor.go:30-50`). Without `AND holder_supervisor_id = $1`, the sweep could race against an owner whose heartbeat is about to refresh and clobber a live row. With the predicate, even if the timing argument fails (clock skew, scheduler precision), the live owner's identity prevents the clobber: the SQL `WHERE` simply doesn't match.

The verify-before-run guard is the second guard at the same column: after the acquisition tx commits, the runner re-reads `rimsky_worker_request.claimed_by` and bails if ownership moved (`foundation/integration/runner_acquire.go:756-810`). The two guards together make "still owned by this supervisor" the only safe predicate for any mutation.

The `holder_supervisor_id` column is non-nullable on the live row (`foundation/persistence/postgres/migrations/001-initial.sql:189`). A claim-handle row with `holder_supervisor_id = NULL` is never reachable through any acquisition path; ZERO rows in production should ever have a null holder_supervisor_id.

CLAUDE.md "Blessed invariants" §4 lists the four call surfaces (queue, claim-handles, integration runner, scheduler orphan reaper). Scenario tests in `test/scenarios/locks/` exist specifically to regress against violations — particularly `verify_before_run_race_test.go` and the related orphan-reap stress tests.

## Code surface

- `foundation/persistence/postgres/claim_handles.go:16-200` — annotated CRUD; DELETE/UPDATE sites.
- `foundation/persistence/postgres/queue.go:265-309` — `ReleaseClaim`.
- `foundation/persistence/postgres/queue.go:309-330` — `GetClaimedBy` (the verify-before-run read).
- `foundation/persistence/sqlite/claim_handles.go` and `foundation/persistence/sqlite/queue.go` — SQLite mirrors.
- `foundation/integration/orphan_reaper.go` (entire file).
- `foundation/integration/supervisor.go:25-38` — claimant-guarded-release annotation.
- `foundation/persistence/postgres/migrations/001-initial.sql:170-209` — `rimsky_claim_handle` schema (holder_supervisor_id NOT NULL).
- `test/scenarios/locks/` — regression tests.

## Prose surface

- `CLAUDE.md` "Blessed invariants" §4.
- `docs/concepts/named-lock.md` — "Lock release is claimant-guarded: only the supervisor that acquired the lock can release it."
- `docs/concepts/claim-handle.md` — orphan-reaper cutoff and claim handles.
- `.ok-planner/specs/2026-05-04-foundation-contract.md` — release predicates required by the foundation.

## Adjacent topics

- `2026-05-10-verify-before-run-guard` — the read-time guard at the same column.
- `2026-05-10-orphan-reaper-no-producer-abandon` — orphan reaper uses the same predicate.
- `2026-05-10-auto-terminal-aggregate-resolution` — auto-terminal deletes claim_handle rows claimant-guarded.
- `2026-05-10-worker-request-phase-lifecycle` — `claimed_by` is non-null only while `phase='active'`.

## Observations

- The convention is "annotated, not enforced by a tool." A new mutation path (release / heartbeat / sweep / repair) is required to include the predicate by code review and the regression tests in `test/scenarios/locks/`. There is no lint that catches an `UPDATE rimsky_claim_handle SET ...` that omits the supervisor predicate.
- The heartbeat-refresh case is mentioned but easy to overlook in code review: a heartbeat looks like a routine update, not a release, but the same predicate applies. `foundation/persistence/postgres/claim_handles.go` carries the heartbeat sites alongside the deletion sites.
- The annotation explicitly lists four call surfaces; if a fifth is added (e.g., a future "claim-handle repair" admin operation), it must be added to the annotation block as well — a documentation discipline rather than a lint.
- A `holder_supervisor_id = NULL` row would be a sign of bug; the schema constraint makes this unreachable, but the column is `NOT NULL` rather than `NOT NULL CHECK (holder_supervisor_id != uuid_nil())` — defensive zero-UUID checks are not present.
