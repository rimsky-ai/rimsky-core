-- =====  rimsky_instances.attribute_overrides_match_counts  =====
-- Per-entry match counter for instance.attribute_overrides.by_match.
-- Array of int64 indexed by by_match entry position; incremented
-- synchronously by the supervisor on each matcher hit. Empty array
-- ('[]') for instances with no by_match entries. Per spec
-- .ok-planner/specs/2026-05-21-attribute-overrides-matcher-overlay-design.md
-- §"Persistence".
ALTER TABLE rimsky_instances
    ADD COLUMN attribute_overrides_match_counts JSONB NOT NULL DEFAULT '[]'::jsonb;
