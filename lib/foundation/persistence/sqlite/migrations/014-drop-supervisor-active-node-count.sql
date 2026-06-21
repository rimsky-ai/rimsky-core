-- 014-drop-supervisor-active-node-count.sql
--
-- Drop the cached active_node_count column from rimsky_supervisors.
-- See the postgres sibling migration for rationale.

ALTER TABLE rimsky_supervisors DROP COLUMN active_node_count;
