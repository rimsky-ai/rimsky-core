-- =====  rimsky_instances.attribute_overrides_match_counts  =====
-- SQLite parallel of postgres migration 006. JSON stored as TEXT;
-- semantics match. Per spec
-- .ok-planner/specs/2026-05-21-attribute-overrides-matcher-overlay-design.md
-- §"Persistence".
ALTER TABLE rimsky_instances
    ADD COLUMN attribute_overrides_match_counts TEXT NOT NULL DEFAULT '[]';
