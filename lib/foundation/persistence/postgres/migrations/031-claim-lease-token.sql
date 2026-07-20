-- Copyright © 2026 Fall Guy Consulting.
-- Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
-- license. See LICENSE.agpl and COPYRIGHT at the repo root.
-- @concept: claim-tree
-- @concept: terminal-resolution
--
-- 031-claim-lease-token.sql
--
-- Batch sub-claims minted by SplitScope had no per-lease identity: a
-- producer resolving a terminal verb by scope alone could attribute a
-- stale holder's Commit/Abandon to a successor's live lease on a
-- byte-equal scope. The producer-minted lease token from
-- SubScopeDescriptor.lease_token is persisted on the sub-claim handle and
-- carried on the outbox row so terminal verbs echo it back for
-- lease-identity matching.

ALTER TABLE rimsky_claim_handles ADD COLUMN producer_lease_token TEXT NOT NULL DEFAULT '';
ALTER TABLE rimsky_producer_verb_outbox ADD COLUMN lease_token TEXT NOT NULL DEFAULT '';
