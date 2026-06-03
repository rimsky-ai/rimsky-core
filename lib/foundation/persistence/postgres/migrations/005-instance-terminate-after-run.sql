-- Copyright © 2026 Fall Guy Consulting.
-- Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
-- license. See LICENSE.agpl and COPYRIGHT at the repo root.

-- 005-instance-terminate-after-run.sql
--
-- Add the per-instance terminate_after_run flag (spec
-- 2026-06-03-instance-lifecycle-durable-by-default). Instances are durable
-- by default — once created they live until force-terminate. This opt-in
-- per-instance boolean is the only path by which an instance self-terminates,
-- and then only after its next frame ends (strict "run at most once more"
-- semantics, applied by the terminal predicate in a later change).
--
-- Default FALSE preserves durable-by-default for every existing row and any
-- create request that omits the flag. Mirrors the storage form of the
-- `paused` column declared in 001-schema.sql (BOOLEAN NOT NULL DEFAULT
-- FALSE). Pre-v1, plain additive — no compat shim.

ALTER TABLE rimsky_instances
    ADD COLUMN terminate_after_run boolean NOT NULL DEFAULT false;
