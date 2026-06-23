-- Copyright © 2026 Fall Guy Consulting.
-- Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
-- license. See LICENSE.agpl and COPYRIGHT at the repo root.

-- 014-drop-supervisor-active-node-count.sql
--
-- Drop the cached active_node_count column from rimsky_supervisors.
-- See the postgres sibling migration for rationale.

ALTER TABLE rimsky_supervisors DROP COLUMN active_node_count;
