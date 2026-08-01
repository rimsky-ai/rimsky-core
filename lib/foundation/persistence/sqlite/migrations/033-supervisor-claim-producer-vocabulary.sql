-- Copyright © 2026 Fall Guy Consulting.
-- SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial
-- @concept: supervisor
-- @concept: claim-producer

-- 033-supervisor-claim-producer-vocabulary.sql
--
-- rimsky_supervisors.accepted_stores carried the retired 'store' vocabulary
-- (concept:claim-producer lists claim-store as a retired alias) on the
-- persisted column while the Go field itself (AcceptedClaimProducers) was
-- already swept. Rename the column to match. accepted_stores is not part
-- of any primary key, so a plain RENAME COLUMN suffices.

ALTER TABLE rimsky_supervisors RENAME COLUMN accepted_stores TO accepted_claim_producers;
