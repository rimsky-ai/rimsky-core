-- Copyright © 2026 Fall Guy Consulting.
-- SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial
-- @decision: service-delivery-stall-signal
--
-- 049-service-delivery-stalls.sql
--
-- One row per (service, outbox) whose oldest pending delivery has
-- waited longer than service_delivery.stall_after. Every runtime role
-- drains the same outbox, so the row is what makes the stall an edge
-- rather than a per-pass repeat: the insert that creates it writes the
-- stall entry, the delete that removes it writes the recovery entry,
-- and a losing racer writes nothing.

CREATE TABLE rimsky_service_delivery_stalls (
    service       TEXT NOT NULL,
    outbox        TEXT NOT NULL,
    stalled_since TEXT NOT NULL,
    PRIMARY KEY (service, outbox)
);
