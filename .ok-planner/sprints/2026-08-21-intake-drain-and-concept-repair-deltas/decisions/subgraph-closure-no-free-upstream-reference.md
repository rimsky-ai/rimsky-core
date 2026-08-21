---
decision: subgraph-closure-no-free-upstream-reference
---

# Sub-graph nodes cannot read the calling graph by free reference

## Choice

A node inside a sub-graph cannot read attributes from the calling graph's upstream nodes by free reference. Calling-graph state reaches a sub-graph only through the calling node's own inputs (see `concept:attribute`, `concept:delegation`, `concept:run-scope`).

## Rationale

A sub-graph is reusable because its inputs are the whole of what it depends on. A free reference would tie a sub-graph to the node names of one calling graph, so a second graph could not call it. Threading state through the calling node keeps a sub-graph's contract visible at the call site, which is where an author reads it. The cost is that an author restates a value on the calling node rather than reaching past it.

## Alternatives

- Give sub-graph nodes lexical access to the calling graph's upstream attributes — rejected: the sub-graph binds to one caller's node names and stops being callable from another graph.
