---
story: iterative-workflows-converge
status: as-is
---

# Template author expresses iterative workflows as first-class graph cycles

## Role

As a template author, I can express an iterative or cyclic workflow — a node that re-runs against its own output, or a cycle of nodes that walks back to its starting node — as a declared graph shape in the template. The iteration composes with the rest of the graph, is visible to observability, and terminates declaratively rather than requiring an operator-authored round-count ceiling.

## Capability

A node can subscribe back into its own dispatch chain: either as a self-edge (a node subscribes to its own emissions) or as a two-node (or longer) cycle in which a downstream node's emission wakes an upstream node. The cycle unfolds as successive cascade rounds within a single frame — each round is a fresh node-run appended to the frame's queue, not a new frame. Termination is declared, not hard-coded: a CEL `when:` predicate on a cycle subscription evaluates false and drops the wake; or the diff-gate on `attribute/<key>/changed` (see `concept:signal`) suppresses the wake once the looped attribute stops changing; or a per-node `cascade_mode` (see `concept:cascade-mode`) collapses queued cascade rounds down to the intended cardinality. All three termination surfaces express the stop condition in the declarative template.

## Business value

Iterative computation is a first-class declarative graph object. Convergence, ordering, and dedup live in the template's subscriptions and gates — not in executor-private state, not in operator-supplied round-count ceilings, not in message-driven workarounds. Because every round shares one frame, the iteration is one atomic unit of work at the instance-lifecycle layer — one frame boundary, one message-triggering event, one cascade closure. Observability tools see the iteration as a coherent sequence of node-runs inside one frame rather than as a sequence of separately-triggered instance activations.

## Acceptance

A two-node back-edge cycle A → B → A closes within one frame. The frame opens on an initial trigger; A dispatches (round 1) and settles with a tag that matches B's CEL `when:` predicate; B dispatches on A's `terminal/success`; B's `terminal/success` back-edges to A which re-dispatches (round 2); on round 2 A settles with a tag that no longer matches B's predicate; B does not re-fire; the cycle terminates. Exactly one frame; A runs twice; B runs once. A separate self-edge variant (the intra-frame self-cascade proofs under `concept:cascade-mode`) closes the same way against the same-frame node-run queue.

## Falsifier

A back-edge cycle whose successor round opens a new frame instead of appending a new node-run to the current frame's queue (violates the intra-frame invariant); OR a cycle that runs unbounded even when its CEL `when:` predicate evaluates false or the diff-gate should suppress the successor wake (declarative termination fails to stop the loop).

## Proof

An executable two-node back-edge scenario: starter → A (round 1) → B (matches `when:`) → A (round 2) → B (fails `when:`) → cycle ends. Assertions: exactly one frame carries the entire cycle; A runs exactly twice; B runs exactly once; the frame's node-run ledger shows both A dispatches inside the same frame boundary.
