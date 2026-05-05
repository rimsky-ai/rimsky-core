---
error: orphaned_claim_lost_race
surfaced_to: executor
---

# `orphaned_claim_lost_race`

## What it means

The supervisor began running a node, then re-checked claim ownership immediately before dispatching, and found another supervisor had taken over. The node was not dispatched.

## When it happens

Two supervisors briefly contended for the same claim. The losing supervisor backs off; the winning supervisor proceeds. This is a normal contention outcome under multi-replica deployments — not a fault.

## What to do

No action required. The claim will be re-attempted on the next scheduling tick if needed. If you see this error frequently (more than once every few seconds), check that supervisor heartbeat intervals and orphan-reaper cutoffs are configured consistently across replicas.

## See also

- [`../../concepts/claim-handle.md`](../../concepts/claim-handle.md)
- [`../../concepts/claim.md`](../../concepts/claim.md)
