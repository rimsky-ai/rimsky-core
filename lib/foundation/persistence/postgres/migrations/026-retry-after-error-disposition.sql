-- Copyright © 2026 Fall Guy Consulting.
-- SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial
-- @concept: node-run
--
-- 026-retry-after-error-disposition.sql
--
-- Extend the recovery-aware dispatch disposition vocabulary to the full
-- ruled set {stale_recovery, retry_after_error, recalculate}: an
-- error-policy-resolved retry stamps retry_after_error on the superseding
-- dispatch, alongside the quiet-period stale_recovery stamp and the
-- operator recalculate stamp.

ALTER TABLE rimsky_node_runs
    DROP CONSTRAINT rimsky_node_runs_prior_dispatch_disposition_check;

ALTER TABLE rimsky_node_runs
    ADD CONSTRAINT rimsky_node_runs_prior_dispatch_disposition_check
    CHECK (prior_dispatch_disposition IS NULL
           OR prior_dispatch_disposition IN ('stale_recovery','retry_after_error','recalculate'));
