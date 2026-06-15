---
story: cross-frame-coupling
status: as-is
---

# Template author expresses cross-frame coupling through emit-nodes and message schema

## Role

As a template author,

## Capability

I can express cross-frame coupling (back-edges in cycles, self-drain-my-queue) through emit-nodes plus the message schema, with the receiver reading the sender's data via the message body,

## Business value

so that cross-frame coupling patterns compose as graph objects rather than slipping past the runtime.

## Acceptance

I write a 2-cycle A → B → A where B's settlement triggers a message-emitter node whose dispatch emits a message that A subscribes to. When B settles, the emit-node runs, the message lands in the ledger, the next frame opens with A stale-marked, and A reads B's data through the typed-message substitution grammar in its attribute schema. Separately, I write a self-emit (a message-emitter node that subscribes to its own emit-source with a change-gate) and the loop drains until convergence.

## Falsifier

A multi-node back-edge cycle silently drops the dispatch — the message envelope appears in the ledger but no frame opens for the receiver; OR the receiver re-runs but cannot read the sender's data; OR the self-drain loops infinitely without converging.

## Proof

All-of-the-above. Executable proofs for the back-edge cycle and the self-drain convergence, plus a demo walking through the scenario succeeding.
