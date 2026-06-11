---
concept: sub-graph
status: as-is
aliases: []
---

# Sub-graph

## Definition

A sub-graph is a graph with declared `entry:` and `exit:` nodes; invocable from another node via `delegate: <graph-name>`. The calling node and the sub-graph's entry node share runtime identity (the same persisted node record, same executor — see `concept:delegation`); the exit node remains a separate child whose writeback flows back to the calling node via the carry-rule.

## Boundaries

Owns: the sub-graph template-DSL shape (`entry:`, `exit:`, internal `nodes:`), the canonicalization-time entry absorption + exit carry-rule, the edge-case rejections at registration. Does NOT own: per-invocation run trees (see `concept:node-run`, `concept:delegation`), aggregation rules over internal children (see `concept:node-run` state-aggregation table). Adjacent: `concept:graph`, `concept:delegation`, `concept:node`, `concept:cascade` (sub-graph encapsulation).

## Invariants

- A sub-graph MUST declare both `entry:` and `exit:`. Templates declaring a sub-graph without one are rejected with `subgraph_missing_entry_exit`.
- Entry and exit MUST be distinct nodes. `entry == exit` is rejected (`subgraph_entry_equals_exit`).
- Internal nodes can only reference other internal nodes within the same sub-graph or the entry alias (which resolves to the calling node per-invocation). References to outer-graph nodes or to other sub-graphs' internals are rejected at template registration (`subgraph_external_reference`).
- All internal nodes MUST be reachable from `entry` and MUST feed `exit` (no disconnected internals; reject as `subgraph_internal_disconnected`).
- Recursive sub-graphs (a sub-graph delegating to itself, directly or via a cycle) are rejected as `subgraph_recursion_unsupported`.
- The `main` graph cannot be a sub-graph (no `entry:` / `exit:`; reject).
