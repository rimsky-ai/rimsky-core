-- Copyright © 2026 Fall Guy Consulting.
-- Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
-- license. See LICENSE.agpl and COPYRIGHT at the repo root.

-- 004-frame-delivery-default.sql
--
-- Flip the default FrameDeliveryMode for new instances from 'coalesce' to
-- 'serial_queue' (spec 2026-05-29-console-upstream-auth-audit-and-fixes
-- §7c). serial_queue delivers one message per frame, so each backfill is
-- its own frame/rerun/override — unambiguous, and the intuitive default;
-- coalesce becomes the opt-in mode.
--
-- The load-bearing default actually lives in the driver INSERT literal
-- (COALESCE($6, 'serial_queue') in postgres/instances.go), which decides
-- the value when the caller omits a mode. This ALTER is belt-and-suspenders
-- for any other insert path that omits frame_delivery_mode and relies on
-- the column DEFAULT. The CHECK ('serial_queue','coalesce') is unchanged.

ALTER TABLE rimsky_instances
    ALTER COLUMN frame_delivery_mode SET DEFAULT 'serial_queue';
