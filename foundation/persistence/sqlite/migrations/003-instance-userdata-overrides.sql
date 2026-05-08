-- 2026-05-07 rimsky_instances.userdata_overrides — per-instance JSON
-- overrides deep-merged into per-node userdata at dispatch time.
--
-- Shape (validated by control-api at instance-create; opaque to rimsky
-- at dispatch per @blessed-invariant 11):
--
--   {
--     "by_executor": {"<executor-name>": { ...userdata-fragment... }},
--     "by_node":     {"<node-name>":     { ...userdata-fragment... }}
--   }
--
-- Both keys optional. Empty object `{}` means no overrides — that is
-- the documented default and the column NOT NULL DEFAULT enforces it
-- so dispatch-time reads are unconditional.

ALTER TABLE rimsky_instances
    ADD COLUMN userdata_overrides TEXT NOT NULL DEFAULT '{}';
