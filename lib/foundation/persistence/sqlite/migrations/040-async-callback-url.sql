-- Copyright © 2026 Fall Guy Consulting.
-- Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
-- license. See LICENSE.agpl and COPYRIGHT at the repo root.
-- @concept: supervisor

-- 040-async-callback-url.sql
--
-- Persist the callback URL stamped into an async dispatch so the orphan
-- reap can name the URL nobody called: a misconfigured advertise host then
-- names its own cause in the reap diagnostics instead of requiring a
-- three-log correlation hunt.

ALTER TABLE rimsky_node_runs ADD COLUMN async_callback_url TEXT;
