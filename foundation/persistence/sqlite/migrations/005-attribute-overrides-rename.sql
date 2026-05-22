-- =====  rimsky_instances.userdata_overrides → attribute_overrides  =====
-- Per spec
-- .ok-planner/specs/2026-05-20-userdata-collapse-into-attributes-design.md
-- §"Persistence".
ALTER TABLE rimsky_instances
    RENAME COLUMN userdata_overrides TO attribute_overrides;
