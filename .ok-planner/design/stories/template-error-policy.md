---
story: template-error-policy
status: as-is
---

# Template author routes error classes

## Role

As a template author writing fault-tolerant workflows, I can declare per-error-class routing actions (`pass`, `give_up`, `retry`, `discard_claims_then_retry`) and have the runtime honor each action at the appropriate error site, so that I express graceful failure handling without writing handlers.

## Capability

Per-error-class routing via `error_types:` template entries: each error class maps to one of four actions and the runtime applies it deterministically.

## Business value

Template authors express graceful failure handling declaratively, without writing handlers; the four actions cover the typical recovery shapes.

## Acceptance

A template declaring `error_types:` entries mapping specific error classes to each of the four actions; when a node errors with a class mapped to `pass`, the cascade continues as if the node had succeeded; mapped to `give_up`, the node-run terminates and downstream nodes are not dispatched; mapped to `retry`, the runtime re-dispatches the node; mapped to `discard_claims_then_retry`, held claims are released before re-dispatch.

## Falsifier

Any of the four actions has no observable effect (the runtime acts the same regardless of the declared action), OR an action's effect doesn't match the declaration.

## Proof

Executable proof.

## Notes

2026-06-08 — Story landed via spec 2026-06-08-design-corpus-bootstrap.
