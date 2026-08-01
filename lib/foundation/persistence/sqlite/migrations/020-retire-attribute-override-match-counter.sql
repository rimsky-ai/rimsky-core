-- Copyright © 2026 Fall Guy Consulting.
-- SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial
-- @concept: instance
-- @concept: attribute
-- @decision: frame-isolation-is-structural

ALTER TABLE rimsky_instances DROP COLUMN attribute_overrides_match_counts;
