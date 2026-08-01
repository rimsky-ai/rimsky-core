---
story: iterative-workflows-converge
status: as-is
---

# Template author expresses iterative workflows as first-class graph cycles

## Story

As a template author, I can express an iterative or cyclic workflow — a node that re-runs against its own output, or a cycle of nodes that walks back to its starting node — as a declared graph shape in the template. The iteration composes with the rest of the graph, is visible to observability, and terminates declaratively rather than requiring an operator-authored round-count ceiling.

A node can subscribe back into its own dispatch chain: either as a self-edge (a node subscribes to its own emissions) or as a two-node (or longer) cycle in which a downstream node's emission wakes an upstream node. The cycle unfolds as successive cascade rounds within a single frame — each round is a fresh node-run appended to the frame's queue, not a new frame. Termination is declared, not hard-coded: a CEL `when:` predicate on a cycle subscription evaluates false and drops the wake; or the diff-gate on `attribute/<key>/changed` (see `concept:signal`) suppresses the wake once the looped attribute stops changing; or a per-node `cascade_mode` (see `concept:cascade-mode`) collapses queued cascade rounds down to the intended cardinality. All three termination surfaces express the stop condition in the declarative template.

Iterative computation is a first-class declarative graph object. Convergence, ordering, and dedup live in the template's subscriptions and gates — not in executor-private state, not in operator-supplied round-count ceilings, not in message-driven workarounds. Because every round shares one frame, the iteration is one atomic unit of work at the instance-lifecycle layer — one frame boundary, one message-triggering event, one cascade closure. Observability tools see the iteration as a coherent sequence of node-runs inside one frame rather than as a sequence of separately-triggered instance activations.
