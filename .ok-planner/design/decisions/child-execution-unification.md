---
decision: child-execution-unification
status: as-is
---

# One dispatch primitive, one settlement primitive for child execution

## Choice

A single dispatch-children primitive performs the run-side dispatch of child executions, and a single settle-children primitive performs their settlement, for both delegation and fan-out (see `concept:child-execution`). Delegation wraps the pair with one partition / carry-verbatim policy / entry absorbed; fan-out with N partitions / author policy. No schema change; the `delegate:` and `fan_out:` template surfaces are unchanged.

## Rationale

Delegation is fan-out with N=1; two parallel implementations of one primitive is the duplicated-path disease — a defect fixed in one path is reintroduced by the other.

## Alternatives

A shared settlement library only (rejected: leaves the dispatch-side hand-parity seam alive).
