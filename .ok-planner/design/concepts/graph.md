---
concept: graph
status: as-is
aliases: []
---

# Graph

## Definition

A graph is rimsky's unit of node connectivity. Templates declare one or more graphs uniformly via the template DSL's graphs surface. A reserved top-level graph name designates the instance-level graph — every instance binds its state machine to that top-level graph. Other graphs are **sub-graphs** (see `concept:sub-graph`), invocable from the top-level graph or from each other via the delegation surface.

## Boundaries

Owns: the template-DSL surface that declares graphs, the uniform declaration shape, the reserved-name rule. Does NOT own: per-node lifecycle (see `concept:node`, `concept:node-run`), cascade walking (see `concept:cascade`), sub-graph invocation semantics (see `concept:delegation`). Adjacent: `concept:sub-graph`, `concept:delegation`, `concept:template`, `concept:node`.

## Invariants

- Every template must declare the reserved top-level graph. The instance state machine is bound to it.
- A graph is either the top-level graph or a sub-graph; sub-graphs declare entry and exit points.
- Sub-graph definitions can only be referenced via delegation from a node in another graph; they're never instantiated directly at instance creation.
- The top-level graph cannot declare entry or exit points (those have no meaning at instance level; rejected at registration).
