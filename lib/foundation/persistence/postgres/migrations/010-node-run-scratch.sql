-- Migration 010 — Add per-dispatch executor scratch triple to rimsky_node_runs.
-- Mirrors the parked_payload_inline/handle/handle_backend pattern so spill
-- (via concept:blob-backend) reuses the same plumbing.

ALTER TABLE rimsky_node_runs
    ADD COLUMN scratch_inline               BYTEA,
    ADD COLUMN scratch_handle               TEXT,
    ADD COLUMN scratch_handle_backend       TEXT;
