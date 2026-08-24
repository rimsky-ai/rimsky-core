-- Copyright © 2026 Fall Guy Consulting.
-- SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial
-- @decision: service-delivery-stall-signal
--
-- 047-lifecycle-outbox-attempts.sql
--
-- A lifecycle-outbox row carries its own delivery failure state: the
-- attempt count, the time the next attempt is due, and the error the
-- last attempt returned. The drain skips a row whose next attempt is
-- still in the future, so that row's stream waits until the due time.
-- A diagnostics reader lists what a service still owes from these
-- columns.

ALTER TABLE rimsky_lifecycle_outbox
    ADD COLUMN attempt_count   INTEGER     NOT NULL DEFAULT 0,
    ADD COLUMN next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    ADD COLUMN last_error      TEXT        NOT NULL DEFAULT '';
