-- =====  Scope → ClaimScope rename  =====
-- Rename rimsky_claim_handles.scope_data → claim_scope_data.
-- Update lock_kind CHECK constraint enum: 'scope' → 'claim_scope'.
-- Drop+recreate claim_handle_kind_fields which embeds both the old
-- enum value AND the old column name.
-- Rename index idx_rimsky_claim_handles_scope → ..._claim_scope.
-- Per spec
-- .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md
-- §"Rename 2".

ALTER TABLE rimsky_claim_handles RENAME COLUMN scope_data TO claim_scope_data;

-- Update the data first so the new CHECK constraint passes.
UPDATE rimsky_claim_handles SET lock_kind = 'claim_scope' WHERE lock_kind = 'scope';

-- Drop and recreate the column-inline lock_kind CHECK. Postgres
-- auto-named it; resolve the name via pg_constraint and drop.
DO $$
DECLARE
    cname TEXT;
BEGIN
    SELECT conname INTO cname
      FROM pg_constraint
     WHERE conrelid = 'rimsky_claim_handles'::regclass
       AND contype = 'c'
       AND pg_get_constraintdef(oid) LIKE '%lock_kind = ANY%';
    IF cname IS NOT NULL THEN
        EXECUTE format('ALTER TABLE rimsky_claim_handles DROP CONSTRAINT %I', cname);
    END IF;
END $$;

ALTER TABLE rimsky_claim_handles ADD CONSTRAINT rimsky_claim_handles_lock_kind_check
    CHECK (lock_kind IN ('named', 'claim_scope'));

-- Drop and recreate the kind-fields CHECK with renamed identifiers.
-- Shape per migration 001-baseline.sql:349-352.
ALTER TABLE rimsky_claim_handles DROP CONSTRAINT claim_handle_kind_fields;
ALTER TABLE rimsky_claim_handles ADD CONSTRAINT claim_handle_kind_fields CHECK (
    (lock_kind = 'named'       AND lock_name IS NOT NULL AND producer_name IS NULL     AND claim_scope_data IS NULL     AND intent IS NULL    AND realized_write_semantics IS NULL) OR
    (lock_kind = 'claim_scope' AND lock_name IS NULL     AND producer_name IS NOT NULL AND claim_scope_data IS NOT NULL AND intent IN ('r', 'rw'))
);

-- Index rename + predicate update for the renamed enum value.
DROP INDEX IF EXISTS idx_rimsky_claim_handles_scope;
CREATE INDEX idx_rimsky_claim_handles_claim_scope
    ON rimsky_claim_handles (producer_name) WHERE lock_kind = 'claim_scope';
