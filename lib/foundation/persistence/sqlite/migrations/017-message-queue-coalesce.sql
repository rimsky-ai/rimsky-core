-- Copyright © 2026 Fall Guy Consulting.
-- Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
-- license. See LICENSE.agpl and COPYRIGHT at the repo root.
-- @concept: message
-- @decision: message-queue-mode-per-instance

-- 017-message-queue-coalesce.sql
--
-- Retire the 'queued' frame state's supporting queued_at column and the
-- queued-frame index. Under the frame-isolation ruling, work waiting for
-- a slow instance lives at the message-pool layer (rimsky_messages with
-- delivered_at IS NULL AND cancelled = 0), not as pre-open queued
-- frames. The frame engine picks the oldest pending message per idle
-- instance and creates a running frame directly.
--
-- Adds per-instance message_queue_mode governing whether pending
-- messages coalesce (queue length ≤ 1, most recent wins) or accumulate
-- (backlog). Default 'backlog'.
--
-- Pre-v1: any pre-migration queued frames are dropped. Their trigger
-- messages stay in place (rimsky_messages.frame_id FK is ON DELETE SET
-- NULL and delivered_at is NULL for queued-frame triggers), so they
-- reappear as pending in the new pickup path.
--
-- The state CHECK constraint on rimsky_frames still permitted 'queued' as
-- a legal token after this migration; the whole state column (and its
-- CHECK) was later dropped by 021-retire-frame-state-column.sql.

DELETE FROM rimsky_frames WHERE state = 'queued';

DROP INDEX IF EXISTS idx_rimsky_frames_queued;

ALTER TABLE rimsky_frames DROP COLUMN queued_at;

ALTER TABLE rimsky_instances
    ADD COLUMN message_queue_mode TEXT NOT NULL DEFAULT 'backlog'
    CHECK (message_queue_mode IN ('backlog','coalesce'));

DROP INDEX IF EXISTS idx_messages_pending;
CREATE INDEX idx_messages_pending
    ON rimsky_messages(instance_id, received_at)
    WHERE delivered_at IS NULL AND cancelled = 0;
