-- Copyright © 2026 Fall Guy Consulting.
-- SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

-- 041-drop-max-retries-without-progress.sql
--
-- Drop the write-only per-node tuning column. The park path wrote it on
-- every parked dispatch and nothing ever read it back: the enforcement
-- half of the mechanism was never built. The read-side counter column,
-- consecutive_retries_no_progress, stays. If a no-progress retry cap is
-- ever wanted it returns as a designed feature with its own schema.

ALTER TABLE rimsky_node_runs DROP COLUMN max_retries_without_progress;
