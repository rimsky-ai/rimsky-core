-- Copyright © 2026 Fall Guy Consulting.
-- Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
-- license. See LICENSE.agpl and COPYRIGHT at the repo root.

-- Land lineage forensics extension.
-- Spec: dispatch attached to plans/2026-05-15-data-platform-extensions-plan
-- (lineage + events extensions follow-up).
--
-- Pre-v1 break-freely (per .claude/rules/rules.md). We:
--
--   1. Rename the `claim_commit` record_kind to `claim_terminal`. Every
--      claim-handle terminal (Commit, natural Abandon, force-cancelled)
--      now emits a row; the discriminator between them is the new
--      `outcome` column on the row.
--
--   2. Add an `outcome TEXT NOT NULL DEFAULT 'committed'` column on
--      `rimsky_lineage`. The default covers any pre-existing
--      `claim_commit` rows (in dev / testcontainers only; pre-v1 no
--      production data).
--
--   3. Replace the record_kind CHECK constraint to include
--      `claim_terminal` and drop `claim_commit`.

-- 1. Add the outcome column with the safe default.
ALTER TABLE rimsky_lineage
    ADD COLUMN IF NOT EXISTS outcome TEXT NOT NULL DEFAULT 'committed'
        CHECK (outcome IN ('committed','abandoned','force_cancelled'));

-- 2. Rename existing `claim_commit` rows to `claim_terminal`. The
--    pre-existing rows are all Commits (the old kind only emitted on
--    Commit), so `outcome='committed'` from step 1's default is correct.
UPDATE rimsky_lineage
   SET record_kind = 'claim_terminal'
 WHERE record_kind = 'claim_commit';

-- 3. Replace the record_kind CHECK constraint. Drop the prior constraint
--    (named via the conventional pattern that postgres assigns when no
--    explicit name was given to the original CHECK in 002) and add a
--    fresh one with the new allowed set.
ALTER TABLE rimsky_lineage
    DROP CONSTRAINT IF EXISTS rimsky_lineage_record_kind_check;
ALTER TABLE rimsky_lineage
    ADD CONSTRAINT rimsky_lineage_record_kind_check
    CHECK (record_kind IN ('leaf_run','claim_terminal'));
