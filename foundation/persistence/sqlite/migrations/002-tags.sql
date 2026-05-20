-- 002-tags.sql
-- Per spec .ok-planner/specs/2026-05-19-multi-instance-template-ergonomics-design.md
-- Item 4: Operator-facing tags on rimsky_nodes.
--
-- SQLite stores the array as JSON-encoded TEXT, following the convention
-- documented at 001-baseline.sql#17 (sibling arrays: accepted_stores
-- #116, required_stores #134). The persistence layer marshals []string
-- to JSON on insert and unmarshals on scan.
--
-- SQLite has no GIN equivalent; tag-filtered listings scan the
-- JSON-encoded TEXT column at query time.

ALTER TABLE rimsky_nodes
    ADD COLUMN tags TEXT NOT NULL DEFAULT '[]';
