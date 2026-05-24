-- =====  rimsky_node_runs.settling_signal_type  =====
-- Carries the canonical signal type-path (concept:signal) of the run's
-- settling resolution: terminal/success, terminal/error/<class>,
-- terminal/park/<reason>, terminal/infra/<reason>. NULL while the run
-- is in-flight (pending / active / held / parked).
--
-- Per spec .ok-planner/specs/2026-05-23-signal-taxonomy-and-policy-decoupling-design.md
-- §Phase 5. Strictly more expressive than the retired last_outcome
-- column (which migration 014 drops). Cascade-fire is subscriber-driven
-- (signal-type-path match + CEL when:), not gated on this column —
-- this column is informational + drives the substitution-visibility
-- gate and lineage projection.
ALTER TABLE rimsky_node_runs
    ADD COLUMN settling_signal_type TEXT;
