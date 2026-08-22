---
concept: graph
---

# Graph

## What it is

A graph is rimsky's unit of node connectivity: a named set of nodes and the edges among them, declared by a template. A template declares its graphs either explicitly, through the template surface for graphs, or implicitly, by declaring nodes at its top level. Either form constitutes the one top-level graph, which carries a reserved name. A new instance starts from the top-level graph. Every other graph is a sub-graph (see `concept:sub-graph`), which the top-level graph or another sub-graph invokes through delegation.

## Purpose

Graphs let a template name a body of work once and invoke it from more than one place, while keeping exactly one place an instance can start from. The top-level graph gives an instance its single entry. Sub-graphs give an author reuse and nesting without opening a second way in.

## Boundaries

A graph owns the template surface that declares graphs — both the explicit form and the implicit top-level form — and the reserved name the top-level graph carries. A graph is either the top-level graph or a sub-graph, and a sub-graph declares its entry and exit points.

A graph does not own per-node lifecycle (see `concept:node`, `concept:node-run`), cascade walking (see `concept:cascade`), or sub-graph invocation semantics (see `concept:delegation`). See also `concept:sub-graph` and `concept:template`.
