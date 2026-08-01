-- Copyright © 2026 Fall Guy Consulting.
-- SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial
-- @concept: service-address-book
-- @concept: supervisor

-- 039-service-address-book.sql
--
-- The shared service address book: the control plane publishes the
-- deployment's declared executor and claim-producer-store endpoints here at
-- startup and on configuration reload; supervisors resolve names against it
-- read-through. Supervisor registration keeps only per-process facts
-- (concurrency, callback host/port), so the per-supervisor accept-list
-- columns are dropped.

CREATE TABLE rimsky_service_address_book (
    kind      TEXT NOT NULL CHECK (kind IN ('executor', 'claim_producer')),
    name      TEXT NOT NULL,
    transport TEXT NOT NULL DEFAULT '',
    endpoint  TEXT NOT NULL DEFAULT '',
    tls       TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (kind, name)
);

ALTER TABLE rimsky_supervisors
    DROP COLUMN accepted_executors,
    DROP COLUMN accepted_claim_producers;
