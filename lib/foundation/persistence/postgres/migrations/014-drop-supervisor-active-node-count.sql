-- Copyright © 2026 Fall Guy Consulting.
-- SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial
-- @concept: supervisor

-- 014-drop-supervisor-active-node-count.sql
--
-- This ordinal was previously assigned to a different, now-deleted migration
-- (014-drop-publisher-target-node.sql, retired when 001-013 were collapsed
-- into 001-initial.sql; git commit 12243806). The migration runner keys
-- idempotency on filename (persistence/migrations.go), so reusing this
-- ordinal is safe only for a pre-v1 dev database dropped and recreated
-- against the current 001-initial.sql baseline, never for one that still
-- carries the old 014's rimsky_migrations row. Post-v1, ordinals are never
-- reused.
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
