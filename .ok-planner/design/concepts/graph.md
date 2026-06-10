---
concept: graph
status: as-is
aliases: []
---

# Graph

## Definition

A graph is rimsky's unit of node connectivity. Templates declare one or more graphs uniformly under a top-level `graphs:` block. The reserved name `main` is the top-level graph at instance level — every instance binds its state machine to `main`. Other graphs are **sub-graphs** (see `concept:sub-graph`), invocable from `main` or from each other via `delegate:`.

## Boundaries

Owns: the template-DSL `graphs:` block, the uniform declaration shape, the reserved-name rule. Does NOT own: per-node lifecycle (see `concept:node`, `concept:node-run`), cascade walking (see `concept:cascade`), sub-graph invocation semantics (see `concept:delegation`). Adjacent: `concept:sub-graph`, `concept:delegation`, `concept:template`, `concept:node`.

## Invariants

- Every template must declare a graph named `main`. The instance state machine is bound to `main`.
- A graph is either the top-level `main` or a sub-graph (declares `entry:` + `exit:`).
- Sub-graph definitions can only be referenced via `delegate:` from a node in another graph; they're never instantiated directly at instance creation.
- The `main` graph cannot have `entry:` / `exit:` (those have no meaning at instance level; rejected at registration).
