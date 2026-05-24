-- =====  rimsky_node_runs.settling_signal_type  =====
-- SQLite mirror of postgres migration 013. See that file for the
-- semantic rationale.
ALTER TABLE rimsky_node_runs
    ADD COLUMN settling_signal_type TEXT;
