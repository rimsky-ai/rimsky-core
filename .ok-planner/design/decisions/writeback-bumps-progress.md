---
decision: writeback-bumps-progress
---

# Attribute writeback bumps the progress timestamp

## Choice

Each mid-dispatch attribute writeback callback updates the dispatch row's progress timestamp in the same transaction as the attribute write.

## Rationale

A genuine attribute writeback is itself a liveness signal — the executor did real work and is reporting it. Bumping the progress timestamp as a side effect avoids an extra round-trip when writeback is already happening.

## Alternatives

Require an explicit keepalive call even alongside writeback — rejected because it is redundant when the writeback already proves liveness.
