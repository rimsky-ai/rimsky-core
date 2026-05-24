-- =====  rimsky_node_runs.run_scope_id  =====
-- SQLite parallel of postgres migration 008. Per spec
-- .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md.
--
-- SQLite does not cascade index drops automatically — must DROP
-- INDEX explicitly before dropping the columns the indexes reference.
-- The CHECK constraint on child_key is column-inline (unnamed); dropping
-- the column removes it.
--
-- SQLite version dependency: ALTER TABLE ... DROP COLUMN was added in
-- SQLite 3.35.0 (2021-03-12). Rimsky uses the bundled
-- `modernc.org/sqlite` driver (pure-Go, no system libsqlite linkage)
-- which is built against a recent SQLite, so this is satisfied by
-- construction; flagging the version constraint here in case the
-- driver is ever swapped for the C `mattn/go-sqlite3` binding linked
-- against an older system libsqlite.
--
-- NOT SAFE for populated databases. The ADD COLUMN below uses
-- `DEFAULT ''` so the NOT NULL constraint is satisfied on existing
-- rows, but '' is not a valid foreign-key reference to
-- rimsky_run_scopes(id). SQLite does NOT validate existing rows on ADD
-- COLUMN ... REFERENCES, so pre-existing rimsky_node_runs rows would
-- silently end up with orphan FK pointers. Pre-v1 break-freely covers
-- this — operators must drop the dev database before re-running
-- migrations; do NOT run this migration against a populated SQLite
-- database. The postgres parallel doesn't have this hazard because
-- postgres ADD COLUMN NOT NULL fails on populated tables and forces
-- the drop-and-recreate path explicitly.

DROP INDEX IF EXISTS uq_node_runs_in_flight_per_root_node;
DROP INDEX IF EXISTS uq_node_runs_in_flight_per_child;
DROP INDEX IF EXISTS idx_node_runs_parent_run_id;

-- Drop child_key first: it carries a column-inline CHECK that references
-- parent_run_id (parent_run_id IS NULL OR child_key IS NOT NULL). SQLite
-- validates remaining CHECK clauses after each DROP COLUMN; dropping
-- parent_run_id first would leave the child_key CHECK dangling and fail
-- with "no such column: parent_run_id".
ALTER TABLE rimsky_node_runs DROP COLUMN child_key;
ALTER TABLE rimsky_node_runs DROP COLUMN parent_run_id;

-- ON DELETE CASCADE mirrors postgres 008 so dropping a RunScope walks
-- the dispatch rows it owns.
ALTER TABLE rimsky_node_runs
    ADD COLUMN run_scope_id TEXT NOT NULL REFERENCES rimsky_run_scopes(id) ON DELETE CASCADE DEFAULT '';

CREATE UNIQUE INDEX uq_node_runs_in_flight_per_run_scope
    ON rimsky_node_runs (node_id, run_scope_id)
    WHERE phase IN ('pending', 'active', 'held', 'parked');

CREATE INDEX idx_node_runs_run_scope ON rimsky_node_runs (run_scope_id);
