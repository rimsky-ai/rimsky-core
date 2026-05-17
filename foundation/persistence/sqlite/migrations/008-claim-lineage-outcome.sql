-- Copyright © 2026 Fall Guy Consulting.
-- Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
-- license. See LICENSE.agpl and COPYRIGHT at the repo root.

-- SQLite mirror of postgres migration 008-claim-lineage-outcome.
-- Pre-v1 break-freely: rename `claim_commit` → `claim_terminal` and add
-- the `outcome` column. SQLite does not support DROP CONSTRAINT on
-- CHECK; the destructive rewrite uses the standard table-rename pattern
-- so the new CHECK can be declared from scratch.

-- 1. Build the new table shape.
CREATE TABLE rimsky_lineage__new (
    id           TEXT PRIMARY KEY,
    record_kind  TEXT NOT NULL CHECK (record_kind IN ('leaf_run','claim_terminal')),
    instance_id  TEXT NOT NULL REFERENCES rimsky_instances(id) ON DELETE CASCADE,
    frame_id     TEXT NOT NULL,
    observed_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    record       TEXT NOT NULL,
    outcome      TEXT NOT NULL DEFAULT 'committed'
        CHECK (outcome IN ('committed','abandoned','force_cancelled'))
);

-- 2. Copy + rename `claim_commit` to `claim_terminal`. Pre-existing rows
--    are all Commits, so the default outcome='committed' is correct.
INSERT INTO rimsky_lineage__new (id, record_kind, instance_id, frame_id, observed_at, record, outcome)
SELECT id,
       CASE record_kind
           WHEN 'claim_commit' THEN 'claim_terminal'
           ELSE record_kind
       END,
       instance_id, frame_id, observed_at, record, 'committed'
  FROM rimsky_lineage;

-- 3. Replace the old table.
DROP TABLE rimsky_lineage;
ALTER TABLE rimsky_lineage__new RENAME TO rimsky_lineage;

-- 4. Re-create the indexes the original migration declared.
CREATE INDEX IF NOT EXISTS idx_lineage_run
    ON rimsky_lineage(record_kind, json_extract(record, '$.run_id'));
CREATE INDEX IF NOT EXISTS idx_lineage_claim
    ON rimsky_lineage(record_kind, json_extract(record, '$.claim_handle_id'));
