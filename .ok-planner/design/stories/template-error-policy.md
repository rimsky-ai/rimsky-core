---
story: template-error-policy
status: as-is
---

# Template author routes error classes

## Role

As a template author writing fault-tolerant workflows, I can declare per-error-class routing actions (pass, give-up, retry, discard-claims-then-retry) and have the runtime honor each action at the appropriate error site, so that I express graceful failure handling without writing handlers.

## Capability

Per-error-class routing via the template's error-types declaration: each error class maps to one of four actions and the runtime applies it deterministically.

## Business value

Template authors express graceful failure handling declaratively, without writing handlers; the four actions cover the typical recovery shapes.

## Acceptance

A template declaring error-type entries mapping specific error classes to each of the four actions; when a node errors with a class mapped to pass, the cascade continues as if the node had succeeded; mapped to give-up, the node-run terminates and downstream nodes are not dispatched; mapped to retry, the runtime re-dispatches the node; mapped to discard-claims-then-retry, held claims are released before re-dispatch.

## Falsifier

Any of the four actions has no observable effect (the runtime acts the same regardless of the declared action), OR an action's effect doesn't match the declaration.

## Proof

Executable proof.
