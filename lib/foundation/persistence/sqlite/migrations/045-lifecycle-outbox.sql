-- Copyright © 2026 Fall Guy Consulting.
-- SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial
-- @decision: lifecycle-subscriber-at-least-once-delivery
--
-- 045-lifecycle-outbox.sql
--
-- Stage each lifecycle delivery, one row per subscriber and event, in
-- the transaction that performs the transition. The fan-out deletes a
-- row once its subscriber answers. A row that remains names a delivery
-- rimsky still owes, and the reconciler drains it.
--
-- The table is append-only. Every stage takes a fresh seq, so a
-- re-staged event lands after the events staged since its first
-- staging. A subscriber hears a scope's events in the order they
-- happened.

CREATE TABLE rimsky_lifecycle_outbox (
    seq                 INTEGER PRIMARY KEY AUTOINCREMENT,
    claim_producer_name TEXT    NOT NULL,
    scope_kind          TEXT    NOT NULL CHECK (scope_kind IN ('template','instance','run_scope')),
    scope_id            TEXT    NOT NULL,
    event               TEXT    NOT NULL,
    payload             BLOB    NOT NULL,
    staged_at           TEXT    NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_rimsky_lifecycle_outbox_scope
    ON rimsky_lifecycle_outbox (scope_kind, scope_id, seq);
CREATE INDEX idx_rimsky_lifecycle_outbox_stream
    ON rimsky_lifecycle_outbox (claim_producer_name, scope_kind, scope_id, seq);
