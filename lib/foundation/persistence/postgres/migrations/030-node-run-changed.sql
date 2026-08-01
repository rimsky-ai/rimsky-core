-- Copyright © 2026 Fall Guy Consulting.
-- SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial
-- @concept: run-scope
--
-- 030-node-run-changed.sql
--
-- rimsky_node_runs carried no per-run record of whether that run's own
-- settlement reported changed=true/false, so run-tree changed-aggregation
-- (walking a settled parent's children to decide the parent's own
-- changed verdict) had no persisted signal to read and treated every
-- settled child as changed.

ALTER TABLE rimsky_node_runs ADD COLUMN changed BOOLEAN NOT NULL DEFAULT FALSE;
