-- Copyright © 2026 Fall Guy Consulting.
-- SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial
-- @concept: api-key
--
-- 050-api-key-expiry-event.sql
--
-- The instant the expiry sweep appended this key's auth.key_expired
-- event. Every runtime role runs the same sweep, so the column is what
-- makes the expiry an edge rather than a per-pass repeat: the update
-- that stamps it selects the row, and a losing racer selects nothing.

ALTER TABLE rimsky_api_keys ADD COLUMN expiry_event_at TIMESTAMPTZ;

UPDATE rimsky_api_keys
   SET expiry_event_at = expires_at
 WHERE expires_at IS NOT NULL
   AND expires_at <= now()
   AND expiry_event_at IS NULL;
