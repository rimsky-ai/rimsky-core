---
concept: sub-graph
status: as-is
aliases: []
---

# Sub-graph

## Definition

A sub-graph is a graph with declared entry and exit nodes; invocable from another node via a delegate directive (see `concept:delegation`). The calling node and the sub-graph's entry node share runtime identity (the same persisted node record, same executor — see `concept:delegation`); the exit node remains a separate child whose writeback flows back to the calling node via the carry-rule.

## Boundaries

Owns: the sub-graph template-DSL shape (declared entry and exit nodes plus an internal node set), the canonicalization-time entry absorption + exit carry-rule, the edge-case rejections at registration. Does NOT own: per-invocation run trees (see `concept:node-run`, `concept:delegation`), aggregation rules over internal children (see `concept:node-run` state-aggregation table). Adjacent: `concept:graph`, `concept:delegation`, `concept:node`, `concept:cascade` (sub-graph encapsulation).

## Invariants

- A sub-graph MUST declare both an entry and an exit. Templates declaring a sub-graph without one are rejected at registration.
- Entry and exit MUST be distinct nodes. A sub-graph whose entry and exit name the same node is rejected.
- Internal nodes can only reference other internal nodes within the same sub-graph or the entry alias (which resolves to the calling node per-invocation). References to outer-graph nodes or to other sub-graphs' internals are rejected at template registration.
- All internal nodes MUST be reachable from the entry and MUST feed the exit; disconnected internals are rejected at registration.
- Recursive sub-graphs (a sub-graph delegating to itself, directly or via a cycle) are rejected at registration.
- The `main` graph cannot be a sub-graph; a `main` graph carrying entry/exit declarations is rejected.
