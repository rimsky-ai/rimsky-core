-- Copyright © 2026 Fall Guy Consulting.
-- Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
-- license. See LICENSE.agpl and COPYRIGHT at the repo root.

-- Parent claim-handle aggregation columns for recursive claim-tree
-- resolution. Cycle 4 issue C of the 2026-05-15 data-platform-extensions
-- review: the prior `resolveParentClaimChain` decided parent Commit /
-- Abandon from the just-resolved child's seedOutcome alone, which is
-- correct for `strict.cancel_siblings:true` but wrong for `best_effort`,
-- `threshold(N)`, and (depending on resolution order) `strict.cancel_siblings:false`.
--
-- To express the full aggregation rule table, we persist three new
-- columns on the parent claim_handle row:
--   - aggregation_policy JSONB         — snapshotted at parent acquire time
--   - expected_children_count INT      — incremented at each sub-claim INSERT
--   - committed_children_count INT     — incremented at each child Commit
--   - abandoned_children_count INT     — incremented at each child Abandon
--
-- The recursive walker (`resolveParentClaimChain`) reads them inside the
-- parent's SELECT … FOR UPDATE and fires Commit / Abandon per the policy:
--
--   strict                 — any abandoned → Abandon; else Commit
--   threshold(max_failures)— abandoned > max_failures → Abandon; else Commit
--   best_effort            — committed > 0 → Commit; else Abandon
--   first                  — committed > 0 → Commit; else Abandon (all-abandoned)
--
-- Counters are bumped on the parent inside the same tx as the child's
-- terminal Delete (`ResolveClaimHandleTerminal`), so the read at parent
-- resolution sees an atomically consistent view of all children's outcomes.
-- This is invisible to non-fan-out callers: `expected_children_count = 0`
-- means "no children expected" and `resolveParentClaimChain` is never
-- invoked (only callers that set `ParentClaimHandleID` recurse).
--
-- Pre-v1 break-freely (per .claude/rules/rules.md): drop-and-recreate, no
-- compat shim.
--
-- Spec: .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
-- §Recursive scope partitioning + §State aggregation rules.

ALTER TABLE rimsky_claim_handles
    ADD COLUMN IF NOT EXISTS aggregation_policy JSONB NULL;

ALTER TABLE rimsky_claim_handles
    ADD COLUMN IF NOT EXISTS expected_children_count INTEGER NOT NULL DEFAULT 0;

ALTER TABLE rimsky_claim_handles
    ADD COLUMN IF NOT EXISTS committed_children_count INTEGER NOT NULL DEFAULT 0;

ALTER TABLE rimsky_claim_handles
    ADD COLUMN IF NOT EXISTS abandoned_children_count INTEGER NOT NULL DEFAULT 0;
