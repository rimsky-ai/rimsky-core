---
decision: write-semantics-three-level-structure
---

# Write semantics is advertised, narrowed, then realized

## Choice

Write-semantics support is pinned at three levels. A producer advertises an allowed set through its capabilities. An operator declares a narrowing allowed set per producer in deployment config. Each open verb returns one realized value in its acquisition result. Rimsky validates the operator's set against the producer's at startup and every realized value against both (see `concept:write-semantics`).

## Rationale

The three levels separate three authorities. The producer knows what its substrate can do, the operator decides what this deployment permits, and only the open verb knows what a specific claim got. A single per-binary capability collapses the first and the third: one producer may support in-place synchronous writes for one resource and staged-asynchronous writes for another, and a binary-wide value cannot say so. A per-claim value with no envelope above it gives rimsky nothing to check a producer's answer against.

## Alternatives

- A single per-binary capability — rejected: too coarse, because one producer's support can differ per resource.
- A per-claim value with no advertised or operator-declared envelope — rejected: unbounded, because nothing bounds what a producer may return.
