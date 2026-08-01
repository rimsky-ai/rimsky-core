---
story: sequenced-preserves-cascade-rounds
status: as-is
---

# Sequenced mode dispatches once per cascade round

## Role

As a template author who needs every distinct cascade round to be observable as its own dispatch — for audit, for accumulator semantics, for event-stream patterns where dropping intermediate states is wrong — I can opt my node into `cascade_mode=sequenced` and know that M cascade rounds produce M dispatches, each with the bag from its own moment, in arrival order.

## Capability

When `cascade_mode=sequenced`, the gate evaluator does NOT delete prior cascade-driven stales at pending→stale transition (the dedup that `most-recent` applies). Multiple cascade-driven stales coexist in the queue. The dispatcher claims them by the run's assigned sequence number. Each dispatch sees the bag built at its own moment (the predecessor's bag plus the wait-set overlay drained for that pending). Cascade-stale queue depth is unbounded in this mode.

## Business value

Some workloads cannot tolerate coalescing. An audit-trail executor needs to record every upstream change. An accumulator executor needs to apply every increment, not just the final value. A monitoring executor needs to see the sequence of states to detect rapid flips. For these workloads, `most-recent`'s "drop intermediate" semantic destroys data the executor exists to capture. `sequenced` mode preserves the cascade history at the cost of a potentially-unbounded queue.

