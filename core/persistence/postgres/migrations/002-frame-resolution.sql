-- 002-frame-resolution.sql
-- Frame-resolution semantics per docs/specs/2026-04-26-frame-resolution-design.md.
-- Adds the rimsky_frames table and frame_id columns. The pre-frame-resolution
-- kill_requested column was removed from 001-initial.sql directly (rules.md:
-- pre-v1, break freely); this migration assumes 001 already lacks the column.
-- Pre-v1: in-flight cascades are abandoned by transitioning stale/running nodes
-- to failed (see migration step 6).

CREATE TABLE IF NOT EXISTS rimsky_frames (
    frame_id          UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    instance_id       UUID         NOT NULL REFERENCES rimsky_instances(id) ON DELETE CASCADE,
    mode              TEXT         NOT NULL CHECK (mode IN ('coalesce','serial_queue')),
    state             TEXT         NOT NULL CHECK (state IN ('queued','running','completed','failed')),
    source_node_ids   UUID[]       NOT NULL CHECK (array_length(source_node_ids, 1) >= 1),
    queued_at         TIMESTAMPTZ  NOT NULL DEFAULT now(),
    started_at        TIMESTAMPTZ,
    ended_at          TIMESTAMPTZ,
    frame_timeout_ms  BIGINT       NOT NULL CHECK (frame_timeout_ms >= 60000),
    CONSTRAINT chk_running_has_started CHECK (state != 'running' OR started_at IS NOT NULL),
    CONSTRAINT chk_terminal_has_ended  CHECK (state NOT IN ('completed','failed') OR ended_at IS NOT NULL)
);

CREATE INDEX IF NOT EXISTS idx_rimsky_frames_queued
    ON rimsky_frames (instance_id, queued_at)
    WHERE state = 'queued';

CREATE UNIQUE INDEX IF NOT EXISTS uq_rimsky_frames_running
    ON rimsky_frames (instance_id)
    WHERE state = 'running';

CREATE UNIQUE INDEX IF NOT EXISTS uq_rimsky_frames_coalesce_queued
    ON rimsky_frames (instance_id)
    WHERE state = 'queued' AND mode = 'coalesce';

-- Abandon any in-flight cascade rows BEFORE the schema change so frame_id NOT NULL is satisfiable.
UPDATE rimsky_nodes SET state = 'failed' WHERE state IN ('stale','running');
DELETE FROM rimsky_dispatch;  -- best-effort; no frame_id retroactively assignable

-- rimsky_dispatch: add frame_id (NOT NULL after the DELETE above means the table is empty post-step-7).
ALTER TABLE rimsky_dispatch
    ADD COLUMN frame_id UUID NOT NULL REFERENCES rimsky_frames(frame_id) ON DELETE CASCADE;

CREATE INDEX IF NOT EXISTS idx_rimsky_dispatch_frame
    ON rimsky_dispatch (frame_id);
CREATE INDEX IF NOT EXISTS idx_rimsky_dispatch_frame_claimed
    ON rimsky_dispatch (frame_id) WHERE claimed_by IS NOT NULL;

-- rimsky_nodes: add frame_id (kill_requested was removed from 001-initial.sql
-- directly under the pre-v1 rules; no DROP needed here).
ALTER TABLE rimsky_nodes
    ADD COLUMN frame_id UUID REFERENCES rimsky_frames(frame_id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_rimsky_nodes_frame_state
    ON rimsky_nodes (frame_id, state)
    WHERE state IN ('stale','running');

-- rimsky_lock_holders: observability frame_id.
ALTER TABLE rimsky_lock_holders ADD COLUMN frame_id UUID;

-- rimsky_claim_holders: observability frame_id.
ALTER TABLE rimsky_claim_holders ADD COLUMN frame_id UUID;
