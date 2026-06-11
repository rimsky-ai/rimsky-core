-- Copyright © 2026 Fall Guy Consulting.
-- Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
-- license. See LICENSE.agpl and COPYRIGHT at the repo root.

-- 008-claim-handle-payload.sql
--
-- Persist the producer-supplied claim payload on the rimsky_claim_handles
-- row. Mirror of the postgres-side 008 migration; see that file for the
-- rationale. SQLite stores the bytes verbatim (no JSONB type — TEXT is
-- the canonical raw-JSON column type in the project's sqlite schema).

ALTER TABLE rimsky_claim_handles
    ADD COLUMN payload TEXT;
