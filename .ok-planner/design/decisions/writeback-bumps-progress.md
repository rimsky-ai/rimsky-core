---
decision: writeback-bumps-progress
status: as-is
aliases: []
---

# Attribute writeback bumps last_progress_at

## Choice

Each attribute writeback callback (`POST /v1/runs/{run_id}/attributes`) updates `last_progress_at` on the dispatch row in the same transaction as the attribute write.

## Rationale

A genuine attribute writeback is itself a liveness signal — the executor did real work and is reporting it. Bumping `last_progress_at` as a side effect avoids an extra round-trip when writeback is already happening.

## Alternatives

Require an explicit keepalive call even alongside writeback — rejected because it is redundant when the writeback already proves liveness.
