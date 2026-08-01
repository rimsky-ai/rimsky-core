-- Copyright © 2026 Fall Guy Consulting.
-- SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial
-- @concept: message
--
-- 028-messages-frame-id-index.sql
--
-- Backs ListDeliveredForFrame's dispatch-hot-path lookup and the
-- rimsky_messages_frame_id_fkey ON DELETE SET NULL maintenance during
-- frame prune, both of which previously seq-scanned the monotonically
-- growing rimsky_messages table.

CREATE INDEX idx_messages_frame_id
    ON rimsky_messages(frame_id)
    WHERE frame_id IS NOT NULL;
