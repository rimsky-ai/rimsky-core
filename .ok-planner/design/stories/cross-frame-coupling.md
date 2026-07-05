---
story: cross-frame-coupling
status: as-is
---

# Template author expresses iterative workflows through messages

## Role

As a template author, I want to express iterative or cyclic workflows (back-edges, self-iterating drains) so that loop patterns compose as first-class graph objects rather than slipping past the runtime through hidden state.

## Capability

A node can emit a message that another node (or the same node) subscribes to. The emission opens a new frame in which the subscriber wakes; the receiver reads the emitter's data through the typed-message substitution grammar in its attribute schema (or directly via `nodes.<emitter>.attribute.<X>`). A CEL `when:` predicate on the emit-node's subscription is the author's bounding mechanism for the loop — it can suppress emission based on the upstream's outcome, and the loop ends when the predicate evaluates false. Cross-frame convergence via `attribute/<key>/changed` diff-gating is NOT part of this capability; the diff-gate is an intra-frame optimization on cascade rounds, not a cross-frame termination mechanism (frames are perfectly isolated per `concept:frame`, so a same-value re-settle in one frame is not observable to a downstream node's dispatch decision in a different frame).

## Business value

Iterative and cyclic workflows compose as first-class graph objects. Each iteration is visible as a discrete frame; convergence conditions live in the template's declarative CEL predicates rather than in runtime-private state. Authors don't have to instrument workflows with side-channel counters or rely on implicit timeouts — the platform's message-emit primitive expresses the loop and the `when:` predicate expresses the termination.

## Acceptance

A 2-node back-edge `A → B → emit-node → message → A` works: B settles, the emit-node fires, the message lands in the ledger, a new frame opens with A woken, and A reads B's value via the message body. When the emit-node carries a CEL `when:` gate that evaluates false against B's outcome, no further emit fires and the loop ends.

## Falsifier

A back-edge cycle silently drops the dispatch (message envelope appears in the ledger but no frame opens for the receiver); OR the receiver re-runs but cannot read the sender's data; OR the emit-node's CEL `when:` predicate evaluates false yet a further message emits.

## Proof

An executable proof: a back-edge cycle test with a CEL `when:` gate on the emit-node's subscription that proves emission stops when the predicate evaluates false. Assertion is on the number of emitted messages (and the frames they open), not on any cross-frame diff-gate power.
