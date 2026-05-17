-- Copyright © 2026 Fall Guy Consulting.
-- Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
-- license. See LICENSE.agpl and COPYRIGHT at the repo root.

-- B10 / D7 / E16 of the 2026-05-15 data-platform-extensions plan: retire
-- the per-node cron-fire path. State moves out of `rimsky_schedules` and
-- the per-node `rimsky_nodes.schedule_cron` column; cron firing is now
-- owned by the bundled `sensors/sensor-cron/` service.
--
-- SQLite-is-dev-only + pre-v1 break-freely: SQLite's ALTER TABLE DROP
-- COLUMN works for unconstrained columns in 3.35+ (modernc.org/sqlite
-- ships a recent build), so the per-table rebuild dance from migration
-- 004 is unnecessary here.
--
-- Spec: .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
-- Plan: .ok-planner/plans/2026-05-15-data-platform-extensions-plan.md
--   §Schedule-retirement cascade — D7 + E16 + B10 + P2 + P3.

DROP INDEX IF EXISTS rimsky_schedules_next_fire_at_idx;
DROP TABLE IF EXISTS rimsky_schedules;

ALTER TABLE rimsky_nodes DROP COLUMN schedule_cron;
