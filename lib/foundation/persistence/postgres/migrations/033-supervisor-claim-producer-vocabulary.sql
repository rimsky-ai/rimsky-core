-- Copyright © 2026 Fall Guy Consulting.
-- Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
-- license. See LICENSE.agpl and COPYRIGHT at the repo root.
-- @concept: supervisor
-- @concept: claim-producer

-- 033-supervisor-claim-producer-vocabulary.sql
--
-- rimsky_supervisors.accepted_stores carried the retired 'store' vocabulary
-- (concept:claim-producer lists claim-store as a retired alias) on the
-- persisted column while the Go field itself (AcceptedClaimProducers) was
-- already swept. Rename the column to match.

ALTER TABLE rimsky_supervisors RENAME COLUMN accepted_stores TO accepted_claim_producers;
