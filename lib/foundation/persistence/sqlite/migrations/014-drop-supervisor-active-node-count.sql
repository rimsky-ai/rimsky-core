-- Copyright © 2026 Fall Guy Consulting.
-- SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

-- 014-drop-supervisor-active-node-count.sql
--
-- This ordinal was previously assigned to a different, now-deleted migration
-- (retired when the prior 001-013 sequence collapsed into 001-initial.sql).
-- See the postgres sibling migration for the full rationale and the reuse
-- caveat.
--
-- Drop the cached active_node_count column from rimsky_supervisors.

ALTER TABLE rimsky_supervisors DROP COLUMN active_node_count;
