-- Copyright © 2026 Fall Guy Consulting.
-- Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
-- license. See LICENSE.agpl and COPYRIGHT at the repo root.
-- @concept: instance
-- @concept: attribute
-- @decision: frame-isolation-is-structural

ALTER TABLE rimsky_instances DROP COLUMN attribute_overrides_match_counts;
