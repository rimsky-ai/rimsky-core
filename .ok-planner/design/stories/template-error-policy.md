---
story: template-error-policy
status: as-is
---

# Template author routes error classes

## Role

As a template author writing fault-tolerant workflows, I can declare per-error-class routing actions (pass, give-up, retry, release-and-requeue) and have the runtime honor each action at the appropriate error site, so that I express graceful failure handling without writing handlers.

## Capability

Per-error-class routing via the template's error-types declaration: each error class maps to one of four actions and the runtime applies it deterministically.

## Business value

Template authors express graceful failure handling declaratively, without writing handlers; the four actions cover the typical recovery shapes.

