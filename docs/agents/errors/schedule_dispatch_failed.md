---
error: schedule_dispatch_failed
surfaced_to: operator
---

# Schedule dispatch failed

## What it means

A scheduled fire-time arrived but the corresponding node could not be dispatched. The schedule advances to the next fire-time per its cron expression; the missed fire is NOT backfilled.

## When it happens

The target node was missing, the instance had been deleted, the executor was unreachable, or claim/lock acquisition failed and the scheduler did not retry within the configured window.

## What to do

Check the dispatch failure event in the event log to identify the root cause. Schedule cron advances from the recorded next-fire-at, not from the wall clock — if you need to re-fire the missed time manually, use the admin force-fire endpoint (`POST /admin/scheduled-nodes/{node_id}/force-fire`).

## See also

- [`../../concepts/cascade.md`](../../concepts/cascade.md)
- [`../../concepts/node.md`](../../concepts/node.md)
