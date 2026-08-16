---
assessment: template-sub-graph-delegation--dispatch-sub-graph
subject: story:template-sub-graph-delegation
way: dispatch-sub-graph
release: d977250c
outcome: held
warrant: experiment:template-sub-graph-delegation
---
# A node whose execution unit is a named sub-graph

The audit ran a template whose single main-graph node delegates to a named sub-graph with a declared entry, an internal node and a declared exit. The event log carried `catalog:event-kinds/subgraph.dispatched` on the calling node, so the sub-graph is what the node dispatched as its execution unit rather than something started beside it. The sub-graph's entry had no run of its own, while the internal node and the exit each ran — the entry is the caller's own frame, not an extra unit of work. A template author composes with a named unit and declares nothing about how it is scheduled.

## Unverified remainder

One sub-graph with one internal node was exercised. The demonstration does not establish nesting — a sub-graph that itself delegates to another.
