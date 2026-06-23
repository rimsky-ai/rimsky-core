---
decision: child-execution-unification
status: retired
---

# One dispatch primitive, one settlement primitive for child execution

## Retirement note

Superseded by `decision:fan-out-and-delegation-are-distinct-mechanisms`. The unification claim was wrong on two counts: dispatch is shared only through a thin helper (`DispatchChildren`) that accepts a partitions × children matrix, with delegation and fan-out passing structurally different shapes through it (delegation = 1 partition × N internal nodes; fan-out = N partitions × 1 cloned node); settlement was never unified — `SettleFromDelegate` and `SettleFromFanoutChild` are separate functions with different inputs and different bodies, because the two operations have structurally different fan-in mechanisms (sub-graph exit → 1 attribute carry vs N clones → claim-handle aggregation). The "delegation is fan-out with N=1" framing in the rationale also doesn't survive scrutiny — delegation dispatches the sub-graph's distinct internal nodes, not clones of the calling node.

## Original choice (retained for history)

A single dispatch-children primitive performs the run-side dispatch of child executions, and a single settle-children primitive performs their settlement, for both delegation and fan-out (see `concept:child-execution`). Delegation wraps the pair with one partition / carry-verbatim policy / entry absorbed; fan-out with N partitions / author policy. Both the `delegate:` and `fan_out:` template surfaces author over the same dispatch / settle pair.

## Original rationale (retained for history)

Delegation is fan-out with N=1; two parallel implementations of one primitive is the duplicated-path disease — a defect fixed in one path is reintroduced by the other.

## Original alternatives (retained for history)

A shared settlement library only (rejected: leaves the dispatch-side hand-parity seam alive).
