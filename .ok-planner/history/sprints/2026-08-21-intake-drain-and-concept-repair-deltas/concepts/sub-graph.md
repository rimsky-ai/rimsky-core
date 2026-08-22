---
concept: sub-graph
---

# Sub-graph

## What it is

A sub-graph is a graph with a declared entry node and a declared exit node, which another node invokes by delegating to it (see `concept:delegation`). The calling node and the entry share one runtime identity: canonicalization absorbs the entry into the calling node, so the calling node dispatches the entry's executor as a single run per invocation, and the entry's own template-level node record never dispatches. The exit stays a separate child, and its writeback flows back to the calling node through the carry rule.

## Purpose

A sub-graph lets a template name a piece of graph once and invoke it from any node that delegates to it. Each invocation runs in its own execution context (see `concept:run-scope`), so the calling graph stays flat, the internals stay encapsulated against the outer graph's cascade, and the caller sees one run and one result.

## Boundaries

A sub-graph owns its template shape — the declared entry, the declared exit, and the internal node set — and the rejections registration makes over that shape. A sub-graph is closed against the graph around it: an internal node reaches only its own siblings and the entry (see `decision:subgraph-closure-no-free-upstream-reference`).

A sub-graph does not own entry absorption at canonicalization, which is `concept:delegation`. It does not own the carry settle primitive, which is `concept:child-execution`. It does not own the per-invocation run tree or the rules that aggregate a parent's state over internal children, which are `concept:node-run`.

See also: `concept:graph`, `concept:node`, `concept:node-run`, `concept:delegation`, `concept:child-execution`, `concept:run-scope`, `concept:cascade`.
