-- 2026-05-05 last_outcome + last_progress_at: support for the
-- reactive-loops + lifecycle-handlers spec.
-- See .ok-planner/specs/2026-05-05-reactive-loops-and-lifecycle-handlers-design.md §2.4.

ALTER TABLE rimsky_nodes
    ADD COLUMN last_outcome TEXT;

ALTER TABLE rimsky_frames
    ADD COLUMN last_progress_at TIMESTAMPTZ NOT NULL DEFAULT now();
