-- Copyright © 2026 Fall Guy Consulting.
-- SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial
-- @decision: promotion-lineage-record-after-commit
--
-- 044-producer-verb-outbox-pending-lineage.sql
--
-- Carry a data-processing promotion's claim-terminal lineage record on
-- its outbox row until the producer's commit response lands.
--
-- The record's version identifier arrives in that response. Settlement
-- wrote the record before the response, so every promotion's record
-- carried an empty version. Settlement now stages the record here, and
-- the commit response writes it once, with the version.

ALTER TABLE rimsky_producer_verb_outbox ADD COLUMN pending_lineage_record BLOB;
