-- Copyright © 2026 Fall Guy Consulting.
-- Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
-- license. See LICENSE.agpl and COPYRIGHT at the repo root.

-- Parent claim-handle aggregation columns for recursive claim-tree
-- resolution (SQLite mirror of the postgres 007 migration).
-- Cycle 4 issue C of the 2026-05-15 data-platform-extensions review.
-- See the postgres-side migration header for the design rationale.
--
-- Pre-v1 break-freely.

ALTER TABLE rimsky_claim_handles ADD COLUMN aggregation_policy TEXT NULL;
ALTER TABLE rimsky_claim_handles ADD COLUMN expected_children_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE rimsky_claim_handles ADD COLUMN committed_children_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE rimsky_claim_handles ADD COLUMN abandoned_children_count INTEGER NOT NULL DEFAULT 0;
