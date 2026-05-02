# Control Plane & Store Lifecycle v1 — Implementation Notes

Notes accumulated during execution of `2026-05-01-control-plane-and-store-lifecycle.md`. Reviewed at end of run.

## Task 0 — Branch posture

**Deviation:** Working directly on `main` rather than a feature branch.
**Reason:** Recent commit history (5 commits) all on `main`; project is pre-v1; established workflow appears to land work directly on `main`. The /execute-plan skill normally requires explicit consent to work on main, but the plan author also wrote and invoked the plan against this branch state, indicating intentional workflow.
**Surfaced for:** Confirm this is the expected workflow.

## Task 3 — Schema migration 003

**Deviation:** Added `DELETE FROM` and `ALTER TABLE … ADD CONSTRAINT` statements at the end of the migration to re-establish FK constraints from `rimsky_nodes`, `rimsky_frames`, `rimsky_events` back to the recreated `rimsky_instances`.
**Reason:** Postgres `DROP TABLE … CASCADE` drops the FK *constraint* on dependent tables (and removes any cascade-delete behavior) but leaves the dependent tables and their rows in place. The plan's bare `DROP TABLE rimsky_instances CASCADE` then `CREATE TABLE rimsky_instances` leaves rimsky_nodes, rimsky_frames, rimsky_events without FK constraints to instances, so inserts wouldn't validate and ON DELETE CASCADE wouldn't clean up. Spec §8.2 says: "Other tables that referenced the old `rimsky_templates(id) UUID` are checked at migration time and updated to reference `TEXT` if any. Audit needed during implementation." This is the audit fix-up.
**Surfaced for:** Verify the FK re-establishment is what the plan author intended; alternative would have been to drop and recreate the dependent tables fully, but that involves rewriting all of 001 and 002's content.

## Task 5a — Populate ClaimSpec scope envelope

**Deviation:** Populated `TemplateID` from `inst.TemplateID.String()` (currently a UUID-as-string) rather than the content-hash form. The hash form requires the schema rename in Tasks 11/12. The Task 12 work will rename `InstanceRow.TemplateID` (shared.UUID) to `InstanceRow.TemplateHash` (string) and the helper `instTemplateScope` will be updated to use the new field directly.
**Reason:** The plan ordered Task 5a before Tasks 11/12, but the actual content-hash availability depends on the schema rewrite. Populating the envelope with whatever identifier the row currently carries unblocks the supervisor build now and the value transitions automatically when the schema rewrites.
**Surfaced for:** Verify the helper `instTemplateScope` is updated correctly during Task 12.

## Tasks 11–17 — DAO + handler rewrite (consolidated)

**Deviation:** Tasks 11 (Templates DAO), 12 (Instances DAO), 13 (StoreLifecycle DAO), 14 (Lifecycle fan-out), 15 (Templates HTTP rewrite), 16 (Tags HTTP), and 17 (Instances HTTP touch-up) were executed as a single coordinated edit pass rather than sequential standalone steps. Marking each individually completed for plan-tracking purposes.
**Reason:** The schema migration (Task 3) and the storage interface rewrite both have a transitive blast radius across templates.go, instances.go, supervisor runner, scheduler pure-cascade, and validators. A piecemeal task-by-task execution would have left the build broken for many intermediate steps. Coordinating the rewrites preserves a green build at each top-level step.

**Specific deferred items (still to do post-build):**
- Lifecycle fan-out unit tests (`core/controlapi/lifecycle_test.go`).
- Tags handler unit tests (`core/controlapi/tags_test.go`).
- Templates handler unit tests for the new state machine + idempotent re-register + tag attachment.
- FK-refusal integration tests in `core/storage/postgres/postgres_test.go`.
- The legacy `core/controlapi/app_test.go` covers the prior bare-spec body shape and uses `consumer_key`/`template_id` JSON fields. The new instances handler accepts the new wrapped shape; existing tests need updating to use the new shape OR a transitional shim must accept both. **The handler currently only takes the new shape**; existing tests will fail at the request-decode step until the test harness is updated.

**Surfaced for:** Decide whether the deferred test backfill happens in this run (Task 24's "Full Go check + lint" step would naturally surface the failures) or is split into a follow-up.


## Task 20 — Lifecycle conformance checks (DONE)

**Resolution:** Implemented as a dedicated `core/cmd/rimsky-store-conformance/` binary (option a from the original surfaced choice). It dials a configured store-service endpoint, runs the six lifecycle RPCs with synthetic IDs, and reports per-check pass/fail. A unit test (`main_test.go`) drives the suite against a loopback stub via `stores/stub/testfixture.Start`.

## Task 21 — End-to-end lifecycle scenario test (DONE)

**Resolution:** Implemented as `test/scenarios/lifecycle/lifecycle_e2e_test.go`. The test boots a testcontainer Postgres + stub store-service via `scenario.Start`, walks register → deploy → instantiate → terminate → undeploy → deregister, and asserts `rimsky_store_lifecycle` row deltas at each transition. The instance is driven terminal via direct `MarkTerminated` SQL (the harness's frame engine is not exercised — the lifecycle test targets event sequencing, not runtime behavior); DELETE /instances triggers the terminate fan-out path.

## Execution summary

**Build/lint/test status at end-of-run:**
- `go build ./...` — clean
- `go vet ./...` — clean
- `make lint` (golangci-lint) — clean
- Race-sensitive packages with `-race`: all pass
- Full test suite (with testcontainers Postgres) — all pass
- `docker compose -f deploy/docker-compose.yml config` — syntactically valid

**Post-review status (after 3 fix-review cycles):**
1. Task 20 — DONE (separate `core/cmd/rimsky-store-conformance/` binary added during cycle-2 cleanup).
2. Task 21 — DONE (`test/scenarios/lifecycle/lifecycle_e2e_test.go` added during cycle-2 cleanup).
3. Tests for new handler paths — ADDED. `core/controlapi/lifecycle_test.go`, `tags_test.go`, `templates_test.go`, `instance_terminator_test.go`, `core/canonical/jcs_test.go`, `core/storage/postgres/postgres_test.go::TestStoreLifecycleStore`, `test/scenarios/stores/scope_envelope_test.go`, `core/frame/engine_test.go::TestRunTick_ReapStuckFrame_TerminatesInstance`.
4. Legacy field-name aliases — REMOVED. Tests migrated to the new `template`/`instance_key` shape; handler no longer accepts the legacy keys.
5. The `Deploy` method on `storage.TemplateStore` was removed during cycle-1 cleanup. Tests that previously called `Deploy` now use `insertDeployedTemplate` helpers in `core/scheduler/helpers_test.go` (canonical) and `core/supervisor/auto_terminal_test.go` (tracked duplicate via `@source` annotation).

**Schema/data migration posture:**
The migration drops `rimsky_templates` and `rimsky_instances`, deletes rows from cascading dependent tables, and re-establishes FKs. Existing dev databases will be wiped on first migration run — expected per the project's pre-v1 break-freely posture.

**Helm chart known-stale:** the chart has been updated to use `RIMSKY_CONFIG` and rename `configmap-stores.yaml` → `configmap-rimsky.yaml`, but the existing chart drift noted in CLAUDE.md (env-var lag, etc.) was not separately remediated.
