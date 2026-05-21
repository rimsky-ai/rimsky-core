-- =====  rimsky_wait_set  (mark-don't-delete on drain)  =====
-- Pre-v1 additive: drain marks the row's drained_at timestamp
-- instead of deleting the row.  Eligibility predicate updates to
-- "no rows with drained_at IS NULL." Drained rows are queryable by
-- the substitution-context builder.  Per spec
-- .ok-planner/specs/2026-05-20-attribute-pull-resolution-design.md
-- §"Wait-set — mark-don't-delete on drain".
ALTER TABLE rimsky_wait_set ADD COLUMN drained_at TIMESTAMPTZ;
