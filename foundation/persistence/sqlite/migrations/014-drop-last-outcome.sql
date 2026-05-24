-- 014-drop-last-outcome.sql
-- SQLite mirror of postgres migration 014. SQLite 3.35+ supports DROP
-- COLUMN natively (modernc.org/sqlite v1.50.1 bundles a sqlite well
-- past 3.35). See the postgres mirror for the semantic rationale.
ALTER TABLE rimsky_node_runs DROP COLUMN last_outcome;
