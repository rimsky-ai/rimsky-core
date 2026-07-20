-- Copyright © 2026 Fall Guy Consulting.
-- Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
-- license. See LICENSE.agpl and COPYRIGHT at the repo root.
-- @concept: supervisor

-- 014-drop-supervisor-active-node-count.sql
--
-- Drop the cached active_node_count column from rimsky_supervisors.
--
-- The column was a periodic cache populated by each supervisor's livenessTick
-- (`UPDATE rimsky_supervisors SET active_node_count = N` every 5s) and read
-- only by GET /v1/health. The cache strategy itself was expensive — every
-- supervisor's tick scanned ALL running rows in rimsky_node_runs to count its
-- own — and the value can be computed on-demand at health-call time directly
-- from rimsky_node_runs (filter by state='running' and assigned_supervisor_id).
-- Removing the cache eliminates a periodic table scan per supervisor and ends
-- the confusion where "liveness" plumbing still appeared to exist but no
-- longer carried safety semantics (orphan detection moved to per-dispatch
-- last_progress_at + max_quiet_period, now baked into the 001 baseline).

ALTER TABLE rimsky_supervisors DROP COLUMN IF EXISTS active_node_count;
