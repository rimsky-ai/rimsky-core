-- Copyright © 2026 Fall Guy Consulting.
-- Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
-- license. See LICENSE.agpl and COPYRIGHT at the repo root.

-- Stage-5 of the run-row lifecycle cutover (per the dispatch-9 staging plan
-- in `.ok-planner/plans/2026-05-15-data-platform-extensions-plan-notes.md`).
--
-- After Stages 1..4 the run row owns the dispatch-state lifecycle and the
-- `rimsky_nodes` row is identity-only. Stage 5 finishes the cutover by
-- re-keying the two remaining `*node_id*` columns that still pointed at
-- `rimsky_nodes` onto `rimsky_node_runs`:
--
--   1. `rimsky_claim_holders.holder_node_id` → `holder_run_id`. The
--      held-claim subgraph ledger now records which RUN holds a claim,
--      not which node. Co-holdership (the `holds:` template directive,
--      spec §Claim co-holdership) keys naturally on the run that
--      declared `holds:` at dispatch time; the inheritor path also
--      lifts to per-run identity for the same reason — multi-frame
--      instances and run-tree retention need the per-run distinction.
--
--   2. `rimsky_wait_set.{receiver,sender}_node_id` → `{receiver,sender}_run_id`.
--      The wait-set ledger that gates dispatch under the subscription-
--      cascade model lifts to per-run identity so two in-flight runs of
--      the same node-type (in different frames, or under future sub-graph
--      invocations) don't conflate their wait-sets.
--
-- Pre-v1 break-freely (per `.claude/rules/rules.md`): both tables are
-- DROP + CREATE rather than ALTER + rename. Existing rows are not
-- preserved — Stage 5 lands alongside Stage 3's nuke-and-recreate
-- semantics for the rimsky_nodes columns. Dev-DB nuke + `rimsky-migrate`
-- is the operator action.
--
-- Spec: `.ok-planner/specs/2026-05-15-data-platform-extensions-design.md`.

-- ---------------------------------------------------------------------
-- B5. rimsky_claim_holders — holder_node_id → holder_run_id
-- ---------------------------------------------------------------------

DROP TABLE IF EXISTS rimsky_claim_holders;
CREATE TABLE rimsky_claim_holders (
    id               UUID PRIMARY KEY,
    claim_handle_id  UUID NOT NULL REFERENCES rimsky_claim_handles(id) ON DELETE CASCADE,
    holder_run_id    UUID NOT NULL REFERENCES rimsky_node_runs(id)     ON DELETE CASCADE,
    state            TEXT NOT NULL CHECK (state IN ('active', 'completed', 'failed')),
    completed_at     TIMESTAMPTZ,
    frame_id         UUID,
    UNIQUE (claim_handle_id, holder_run_id)
);
CREATE INDEX IF NOT EXISTS idx_rimsky_claim_holders_claim_handle ON rimsky_claim_holders (claim_handle_id);
CREATE INDEX IF NOT EXISTS idx_rimsky_claim_holders_run         ON rimsky_claim_holders (holder_run_id);
CREATE INDEX IF NOT EXISTS idx_rimsky_claim_holders_active_subgraph
    ON rimsky_claim_holders (claim_handle_id) WHERE state = 'active';

-- ---------------------------------------------------------------------
-- B6. rimsky_wait_set — receiver_node_id → receiver_run_id;
--                       sender_node_id   → sender_run_id
-- ---------------------------------------------------------------------

DROP TABLE IF EXISTS rimsky_wait_set;
CREATE TABLE rimsky_wait_set (
    frame_id            UUID        NOT NULL REFERENCES rimsky_frames(frame_id) ON DELETE CASCADE,
    receiver_run_id     UUID        NOT NULL REFERENCES rimsky_node_runs(id)    ON DELETE CASCADE,
    sender_run_id       UUID        NOT NULL REFERENCES rimsky_node_runs(id)    ON DELETE CASCADE,
    topic_kind          TEXT        NOT NULL CHECK (topic_kind IN ('state','attribute','event')),
    subscription_scope  TEXT        NOT NULL CHECK (subscription_scope IN ('direct','instance')),
    topic_filter        JSONB,
    inserted_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (frame_id, receiver_run_id, sender_run_id, topic_kind, subscription_scope)
);
CREATE INDEX IF NOT EXISTS idx_rimsky_wait_set_receiver ON rimsky_wait_set (frame_id, receiver_run_id);
CREATE INDEX IF NOT EXISTS idx_rimsky_wait_set_sender   ON rimsky_wait_set (frame_id, sender_run_id);
