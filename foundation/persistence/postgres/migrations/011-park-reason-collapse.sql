-- =====  parked_reason CHECK collapse  =====
-- Per spec .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md
-- §ParkReason collapse: the proto enum is now a closed two-value set
-- (PARK_REASON_AWAIT_CALLBACK | PARK_REASON_SNOOZE). The storage
-- CHECK on col:rimsky_node_runs.parked_reason mirrors it.
--
-- Pre-v1 break-freely: existing rows with the old 7-value taxonomy
-- are rewritten to the closest value in the closed set, then the
-- CHECK is replaced:
--   signal_wait / awaiting_human / callback_wait → await_callback
--   time_wait / retry_backoff                    → snooze
--   unspecified / other (legacy free-form)       → await_callback
ALTER TABLE rimsky_node_runs
    DROP CONSTRAINT IF EXISTS rimsky_node_runs_parked_reason_check;

UPDATE rimsky_node_runs SET parked_reason = 'await_callback'
    WHERE parked_reason IN ('signal_wait', 'awaiting_human', 'callback_wait');
UPDATE rimsky_node_runs SET parked_reason = 'snooze'
    WHERE parked_reason IN ('time_wait', 'retry_backoff');
UPDATE rimsky_node_runs SET parked_reason = 'await_callback'
    WHERE parked_reason IN ('unspecified', 'other');

ALTER TABLE rimsky_node_runs
    ADD CONSTRAINT rimsky_node_runs_parked_reason_check
    CHECK (parked_reason IS NULL OR parked_reason IN ('await_callback', 'snooze'));
