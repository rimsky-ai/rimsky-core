-- Copyright © 2026 Fall Guy Consulting.
-- Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
-- license. See LICENSE.agpl and COPYRIGHT at the repo root.
-- @concept: frame
--
-- 024-retire-frame-timeout.sql
--
-- Retire rimsky_frames.frame_timeout_ms and its >=60000 CHECK. The
-- soft stuck-frame warning it fed is retired with it. last_progress_at
-- stays as the progress clock for orphan detection, quiet-period
-- reaping, and operator-side alerting on frame progress.

ALTER TABLE rimsky_frames DROP COLUMN frame_timeout_ms;
