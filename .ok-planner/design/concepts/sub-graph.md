---
concept: sub-graph
status: as-is
aliases: []
---

# Sub-graph

## Definition

A sub-graph is a graph with declared entry and exit nodes; invocable from another node via a delegate directive (see `concept:delegation`). The calling node and the sub-graph's entry share runtime identity: the entry is absorbed into the calling node at canonicalization, so the calling node dispatches with the entry's executor as a single run per invocation (see `concept:delegation`); the entry's own template-level node record is never dispatched. The exit node remains a separate child whose writeback flows back to the calling node via the carry-rule.

## Boundaries

Owns: the sub-graph template-DSL shape (declared entry and exit nodes plus an internal node set), the edge-case rejections at registration. Does NOT own: entry absorption at canonicalization (see `concept:delegation`), the carry settle primitive (see `concept:child-execution`), per-invocation run trees (see `concept:node-run`, `concept:delegation`), aggregation rules over internal children (see `concept:node-run` state-aggregation table). Adjacent: `concept:graph`, `concept:delegation`, `concept:node`, `concept:node-run`, `concept:cascade` (sub-graph encapsulation).

## Invariants

- A sub-graph MUST declare both an entry and an exit. Templates declaring a sub-graph without one are rejected at registration.
- Entry and exit MUST be distinct nodes. A sub-graph whose entry and exit name the same node is rejected.
- Internal nodes can only reference other internal nodes within the same sub-graph or the entry alias (which resolves to the calling node per-invocation). References to outer-graph nodes or to other sub-graphs' internals are rejected at template registration.
- All internal nodes MUST be reachable from the entry and MUST feed the exit; disconnected internals are rejected at registration.
- Recursive sub-graphs (a sub-graph delegating to itself, directly or via a cycle) are rejected at registration.
- The `main` graph cannot be a sub-graph; a `main` graph carrying entry/exit declarations is rejected.
