-- Copyright © 2026 Fall Guy Consulting.
-- SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial
-- @concept: supervisor

-- 040-async-callback-url.sql
--
-- Persist the callback URL stamped into an async dispatch so the orphan
-- reap can name the URL nobody called: a misconfigured advertise host then
-- names its own cause in the reap diagnostics instead of requiring a
-- three-log correlation hunt.

ALTER TABLE rimsky_node_runs ADD COLUMN async_callback_url TEXT;
