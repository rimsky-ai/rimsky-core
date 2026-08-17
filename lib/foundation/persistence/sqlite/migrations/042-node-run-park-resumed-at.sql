-- Copyright © 2026 Fall Guy Consulting.
-- SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

-- 042-node-run-park-resumed-at.sql
--
-- Mark a node-run that reached stale by a park-wake.
--
-- The most-recent cascade mode deletes prior stale cascade rows when a
-- newer round arrives. A woken parked row matched that predicate, so an
-- upstream cascade arriving during a park discarded the parked unit of
-- work. The coalescing delete now reads this column and skips such a
-- row. The gate reads it and keeps the woken row through a newer round.

ALTER TABLE rimsky_node_runs ADD COLUMN park_resumed_at TEXT NULL;
