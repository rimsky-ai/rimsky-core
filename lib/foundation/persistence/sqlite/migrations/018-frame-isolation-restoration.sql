-- Copyright © 2026 Fall Guy Consulting.
-- SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial
-- @concept: node
-- @concept: frame
-- @decision: frame-isolation-is-structural

-- 018-frame-isolation-restoration.sql
--
-- Restores frame isolation as a structural invariant (see
-- decision:frame-isolation-is-structural). Drops rimsky_nodes.frame_id
-- (a per-frame owning-frame pointer written on every cascade insert
-- and state transition — a frame-processing mutation of the identity
-- row) and rimsky_nodes.updated_at (bumped on every transition — same
-- violation). Under frame isolation the node identity row is
-- immutable during frame processing; the running frame is obtained
-- from rimsky_frames directly.

ALTER TABLE rimsky_nodes DROP COLUMN frame_id;
ALTER TABLE rimsky_nodes DROP COLUMN updated_at;
