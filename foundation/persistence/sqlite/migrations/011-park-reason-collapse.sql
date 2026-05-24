-- =====  parked_reason CHECK collapse  =====
-- SQLite parallel of postgres migration 011. Per spec
-- .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md
-- §ParkReason collapse.
--
-- SQLite cannot ALTER a CHECK constraint in place. The baseline
-- rimsky_node_runs table in 001-baseline.sql does NOT declare a
-- CHECK on parked_reason (only on phase/state/last_outcome), so
-- there is no existing constraint to drop. We just rewrite the
-- legacy values to the closed-set values. The closed-set
-- discipline is enforced by the proto wire layer + the runtime
-- handler; storage-level CHECK is a postgres-only refinement.
UPDATE rimsky_node_runs SET parked_reason = 'await_callback'
    WHERE parked_reason IN ('signal_wait', 'awaiting_human', 'callback_wait');
UPDATE rimsky_node_runs SET parked_reason = 'snooze'
    WHERE parked_reason IN ('time_wait', 'retry_backoff');
UPDATE rimsky_node_runs SET parked_reason = 'await_callback'
    WHERE parked_reason IN ('unspecified', 'other');
