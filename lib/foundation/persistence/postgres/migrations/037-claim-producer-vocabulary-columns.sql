-- Copyright © 2026 Fall Guy Consulting.
-- Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
-- license. See LICENSE.agpl and COPYRIGHT at the repo root.
-- @concept: claim-producer

-- 037-claim-producer-vocabulary-columns.sql
--
-- The last two columns carrying the retired 'store' vocabulary
-- (concept:claim-producer lists claim-store as a retired alias):
-- rimsky_node_runs.required_stores (Go: RequiredClaimProducers) and
-- rimsky_lifecycle_idempotencies.store_registration_name
-- (Go: ClaimProducerName). Rename both to match the swept Go surface,
-- completing what 033 did for rimsky_supervisors.accepted_stores.

ALTER TABLE rimsky_node_runs RENAME COLUMN required_stores TO required_claim_producers;
ALTER TABLE rimsky_lifecycle_idempotencies RENAME COLUMN store_registration_name TO claim_producer_name;
