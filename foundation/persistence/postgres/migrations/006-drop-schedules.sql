-- Copyright © 2026 Fall Guy Consulting.
-- Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
-- license. See LICENSE.agpl and COPYRIGHT at the repo root.

-- B10 / D7 / E16 of the 2026-05-15 data-platform-extensions plan: retire
-- the per-node cron-fire path. State moves out of `rimsky_schedules` and
-- the per-node `rimsky_nodes.schedule_cron` column; cron firing is now
-- owned by the bundled `sensors/sensor-cron/` service, which advertises
-- itself via the Sensor protocol and observes via
-- `POST /sensors/{watch_id}/observations`.
--
-- Pre-v1 break-freely (per .claude/rules/rules.md): drop-and-recreate, no
-- compat shim. The graph-layer `ProcessSchedules` tick is removed at the
-- same time (E16); `sensor-cron`'s tick takes over.
--
-- Spec: .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
-- Plan: .ok-planner/plans/2026-05-15-data-platform-extensions-plan.md
--   §Schedule-retirement cascade — D7 + E16 + B10 + P2 + P3.

DROP INDEX IF EXISTS rimsky_schedules_next_fire_at_idx;
DROP TABLE IF EXISTS rimsky_schedules;

ALTER TABLE rimsky_nodes DROP COLUMN IF EXISTS schedule_cron;
