---
story: iterative-workflows-converge
status: as-is
---

# Template author expresses iterative workflows as first-class graph cycles

## Story

As a template author, I can express an iterative or cyclic workflow — a node re-running against its own output, or a cycle of nodes walking back to its start — as a declared graph shape whose stop condition is also declared in the template (`concept:cascade-mode`, `concept:signal`), so that iteration composes with the rest of the graph, stays visible to observability as one coherent unit of work, and terminates by declared convergence rather than an operator-authored round-count ceiling.
