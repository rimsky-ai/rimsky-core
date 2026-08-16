---
assessment: template-sub-graph-delegation--settle-on-sub-graph-outcome
subject: story:template-sub-graph-delegation
way: settle-on-sub-graph-outcome
release: d977250c
outcome: held
warrant: experiment:template-sub-graph-delegation
---
# The calling node settles when the sub-graph settles, carrying its outcome

The sub-graph's exit carried its outcome back to the caller (`catalog:event-kinds/subgraph.exit_carry`), and the caller's settling signal followed that carry in event order — which is the "settles once the sub-graph settles" the story promises, observed rather than inferred. On a second template that makes the sub-graph's exit fail, the caller settled failed carrying the sub-graph's outcome rather than succeeding. The composition therefore propagates in both directions, so a reusable unit's verdict is the calling node's verdict.

## Unverified remainder

Two outcomes — the sub-graph succeeding and its exit failing — were exercised. The demonstration does not establish what the caller does when a node inside the sub-graph parks rather than reaching a terminal.
