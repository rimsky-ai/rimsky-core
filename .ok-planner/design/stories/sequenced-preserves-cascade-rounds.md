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

## Acceptance

An author writes a graph in one frame where A re-runs M times via an intra-frame cascade self-edge (bounded by a CEL `when:` predicate on the self-edge's subscription per `concept:cascade`) and B has `cascade_mode=sequenced`. Each cascade-driven pending for B accumulates in the dispatcher's queue (none are deleted by mode rule). B's initial in-flight dispatch settles, then the dispatcher claims the M queued stales by assigned sequence number. B dispatches M times, each invocation seeing the bag captured at its own moment.

## Falsifier

B dispatches fewer than M+1 times — observable by counting executor invocations across the frame. OR B's dispatches are out of sequence order — observable by comparing each dispatch's bag to A's value history against the assigned sequence number. OR two of B's post-settle dispatches see the same bag — observable by comparing bag values across invocations.

## Proof

The intra-frame mechanism is exercised by cascade-walker unit tests in `lib/runtime`. A scenario-level proof driving A's multiple cascade rounds via the intra-frame cascade self-edge pattern (the same pattern used by the session-resume proof) is deferred — the mechanism's queue-depth-preservation guarantee is verified at the unit level; the scenario proof will land in the intra-frame proof-cluster follow-up.
