-- 002-tags.sql
-- Per spec .ok-planner/specs/2026-05-19-multi-instance-template-ergonomics-design.md
-- Item 4: Operator-facing tags on rimsky_nodes.

ALTER TABLE rimsky_nodes
    ADD COLUMN tags TEXT[] NOT NULL DEFAULT '{}';

-- GIN index supports tag-filtered listings (the primary read pattern
-- introduced with the column). Sibling array columns in 001-baseline.sql
-- (accepted_executors, accepted_stores, required_stores) use the bare
-- TEXT[] shape without an index; this column gets one because filtered
-- list endpoints scan it on every request.
CREATE INDEX rimsky_nodes_tags_idx ON rimsky_nodes USING GIN (tags);
