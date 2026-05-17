-- Copyright © 2026 Fall Guy Consulting.
-- Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
-- license. See LICENSE.agpl and COPYRIGHT at the repo root.

-- Run-row lifecycle flip (data-platform-extensions stage 1).
-- Spec: .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
-- Plan: .ok-planner/plans/2026-05-15-data-platform-extensions-plan.md
--
-- Pre-v1 break-freely: this migration lifts the "at most one in-flight run
-- per node" invariant from a hard `UNIQUE (node_id)` constraint into a
-- partial unique index over the in-flight phases. Terminal rows (phase IN
-- ('completed','failed')) survive past the active terminal so that
-- C5 frame-end + E10 retention + run-tree aggregation can read the row's
-- terminal `state` / `last_outcome` after the active phase closes.
--
-- The Go-side `RemoveForNodeInTx` flips from DELETE to UPDATE (phase →
-- terminal) and `EnqueueInTx` rewrites the upsert to "insert when no
-- in-flight row exists" so retry / pure-cascade re-enqueue paths admit
-- a fresh row alongside the terminal one. See `foundation/persistence/
-- postgres/queue.go` for the matching Go change.

-- Extend the phase CHECK to admit the new terminal value 'failed' so
-- terminal-by-failure rows can be distinguished from terminal-by-success
-- rows. The pre-existing 'completed' value stays as the success terminal;
-- 'failed' is the give_up / park-timeout / orphan-failure terminal.
ALTER TABLE rimsky_node_runs
    DROP CONSTRAINT IF EXISTS rimsky_node_runs_phase_check;
ALTER TABLE rimsky_node_runs
    ADD CONSTRAINT rimsky_node_runs_phase_check
    CHECK (phase IN ('pending','active','held','parked','completed','failed'));

-- Drop the hard UNIQUE (node_id) constraint that capped the table at one
-- row per node. Replace with partial unique indexes over the in-flight
-- phases that distinguish root and child runs:
--
--   * Root runs (parent_run_id IS NULL): at most one in-flight row per
--     node_id — the original invariant carries over.
--   * Child runs (parent_run_id IS NOT NULL): at most one in-flight row
--     per (parent_run_id, child_key) — fan-out + sub-graph dispatch
--     creates N child rows that share node_id (fan-out reuses the leaf
--     node-id; sub-graph children carry the internal-node alias as the
--     child_key) and must coexist while the parent waits on aggregation.
--
-- The earlier single index keyed on `(node_id)` alone collided with the
-- fan-out child INSERTs since all sub-claim children share the parent
-- node's id. Splitting by parent_run_id IS NULL / NOT NULL preserves
-- the original guarantee for top-level runs while admitting the
-- (parent, child_key) idempotency the run-tree relies on.
--
-- The constraint name is the postgres-generated default for a
-- table-level `UNIQUE (node_id)` clause; the IF EXISTS guard keeps the
-- migration idempotent against fresh + re-run installs.
ALTER TABLE rimsky_node_runs
    DROP CONSTRAINT IF EXISTS rimsky_node_runs_node_id_key;

DROP INDEX IF EXISTS uq_node_runs_in_flight_per_node;

CREATE UNIQUE INDEX IF NOT EXISTS uq_node_runs_in_flight_per_root_node
    ON rimsky_node_runs (node_id)
    WHERE parent_run_id IS NULL
      AND phase IN ('pending','active','held','parked');

CREATE UNIQUE INDEX IF NOT EXISTS uq_node_runs_in_flight_per_child
    ON rimsky_node_runs (parent_run_id, child_key)
    WHERE parent_run_id IS NOT NULL
      AND phase IN ('pending','active','held','parked');

-- Drop the now-misnamed pending-only / heartbeat-only / phase indexes
-- and replace with an updated set that excludes terminal rows. Existing
-- indexes that don't filter on phase (`idx_rimsky_node_runs_frame`) stay
-- intact — terminal rows in the same frame are still wanted by
-- observability queries.
DROP INDEX IF EXISTS rimsky_node_runs_pending_idx;
CREATE INDEX IF NOT EXISTS rimsky_node_runs_pending_idx
    ON rimsky_node_runs (enqueued_at) WHERE phase = 'pending';
