-- Migration 010 — Add per-dispatch executor scratch triple to rimsky_node_runs.
-- Mirrors the parked_payload_inline/handle/handle_backend pattern.

ALTER TABLE rimsky_node_runs ADD COLUMN scratch_inline BLOB;
ALTER TABLE rimsky_node_runs ADD COLUMN scratch_handle TEXT;
ALTER TABLE rimsky_node_runs ADD COLUMN scratch_handle_backend TEXT;
