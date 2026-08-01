---
concept: graph
status: as-is
aliases: []
---

# Graph

## What it is

A graph is rimsky's unit of node connectivity. A template declares its graphs either explicitly, through the template DSL's graphs surface, or implicitly, by declaring nodes directly at the template's top level; either form constitutes the reserved top-level graph, named `main`. Every instance's root run-scope binds to that top-level graph at creation. Other graphs are **sub-graphs** (see `concept:sub-graph`), invocable from the top-level graph or from each other via the delegation surface.

## Boundaries

Owns: the template-DSL surface that declares graphs (the explicit graphs surface and the implicit top-level-nodes form), the reserved-name rule. Does NOT own: per-node lifecycle (see `concept:node`, `concept:node-run`), cascade walking (see `concept:cascade`), sub-graph invocation semantics (see `concept:delegation`). Adjacent: `concept:sub-graph`, `concept:delegation`, `concept:template`, `concept:node`.

## Invariants

- Every template has exactly one reserved top-level graph, named `main`. A template using the explicit graphs surface must name exactly one graph `main`; a template declaring nodes directly at the top level implicitly constitutes that same reserved graph without naming it. Every instance's root run-scope is bound to it at creation.
- A graph is either the top-level graph or a sub-graph; sub-graphs declare entry and exit points.
- Sub-graph definitions can only be referenced via delegation from a node in another graph; their nodes are never dispatched except through that delegation — they carry no independent entry point at instance creation.
- The top-level graph cannot declare entry or exit points (those have no meaning at instance level; rejected at registration).
