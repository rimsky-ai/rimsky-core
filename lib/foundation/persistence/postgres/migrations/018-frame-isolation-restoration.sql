-- Copyright © 2026 Fall Guy Consulting.
-- Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
-- license. See LICENSE.agpl and COPYRIGHT at the repo root.
-- @concept: node
-- @concept: frame
-- @decision: frame-isolation-is-structural

-- 018-frame-isolation-restoration.sql
--
-- Restores frame isolation as a structural invariant (see
-- decision:frame-isolation-is-structural). Drops columns on rimsky_nodes
-- that were mutated by frame processing. Node identity rows are now
-- immutable during frame processing; all per-frame state lives on
-- rimsky_node_runs and rimsky_node_attributes.
--
-- rimsky_nodes.frame_id was a per-frame owning-frame pointer written on
-- every cascade insert and state transition. Under frame isolation, the
-- current frame is obtained from the frame engine (rimsky_frames.state =
-- 'running' per instance) and never from the node identity row. The
-- column and its FK are dropped; consumers that read `frame_id` off the
-- node row (RecalculateNode, wakeParkedNode) rewrite to query the
-- running frame directly.
--
-- rimsky_nodes.updated_at was bumped on every state transition, making
-- the identity row a moving target under frame work. Dropped; created_at
-- alone records instance-creation time.

ALTER TABLE rimsky_nodes DROP COLUMN frame_id;
ALTER TABLE rimsky_nodes DROP COLUMN updated_at;
