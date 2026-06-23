---
story: cross-frame-coupling
status: as-is
---

# Template author expresses iterative workflows through messages

## Role

As a template author, I want to express iterative or cyclic workflows (back-edges, self-iterating drains) so that loop patterns compose as first-class graph objects rather than slipping past the runtime through hidden state.

## Capability

A node can emit a message that another node (or the same node) subscribes to. The emission opens a new frame in which the subscriber wakes; the receiver reads the emitter's data through the typed-message substitution grammar in its attribute schema (or directly via `nodes.<emitter>.attribute.<X>`). Loops have explicit bounding mechanisms — a CEL `when:` predicate on the emit-node's subscription can suppress emission based on the upstream's outcome, and the diff-gate on `attribute/<key>/changed` (per `concept:signal`) suppresses the wake automatically when the looped value stabilizes.

## Business value

Iterative and cyclic workflows compose as first-class graph objects. Each iteration is visible as a discrete frame; convergence conditions live in the template's declarative subscriptions and gates rather than in runtime-private state. Authors don't have to instrument workflows with side-channel counters or rely on implicit timeouts — the platform's existing primitives express both the loop and its termination.

## Acceptance

A 2-node back-edge `A → B → emit-node → message → A` works: B settles, the emit-node fires, the message lands in the ledger, a new frame opens with A woken, and A reads B's value via the message body. When the emit-node carries a CEL `when:` gate that evaluates false against B's outcome, no further emit fires and the loop ends.

A self-iterating loop `worker → emit-node → drain/tick → worker` converges via the diff-gate: when the emit-node subscribes only to the worker's `attribute/<key>/changed` (not to the worker's `terminal/success`), the diff-gate suppresses emit-node's wake once the worker's `<key>` stops changing, and no more frames open. The loop is bounded by the worker's value-convergence pattern, not by a frame-count ceiling — a stabilized value produces exactly the expected number of iterations.

## Falsifier

A back-edge cycle silently drops the dispatch (message envelope appears in the ledger but no frame opens for the receiver); OR the receiver re-runs but cannot read the sender's data; OR a self-iterating loop whose looped attribute has stabilized continues to emit (the diff-gate fails to stop the loop, and the run is bounded only by a frame-count ceiling rather than by value convergence).

## Proof

Executable proofs: a back-edge cycle test with a CEL gate that proves emission stops when `when:` evaluates false; a self-drain test where the worker's looped attribute stabilizes and the test asserts the loop fires exactly the expected number of frames (no runaway-prevention ceiling tolerated), proving the diff-gate actually stops the loop.
