---
concept: terminal-tag
status: as-is
aliases: []
---

# Terminal tag

## Definition

A terminal tag is a string member of the tag set carried on a settling terminal verdict (success, error, or park). Tags are deduplicated at decode (set semantics), inert to rimsky, and serve as the discriminator subscribers match on via CEL filters over the verdict's tag set on the terminal signal family. The tag name MUST appear in the emitting executor's observability-advertised declared-tag set — the registry that template registration validates emitted tag sets against.

## Purpose

Provide a topology-visible, ledger-free discriminator on terminal verdicts. Per-emission data rides in the verdict's attribute-delta payload alongside the tag, so the discriminator (tag) and the data (attributes) cleanly separate concerns.

## Boundaries

**Owns:** the set-semantics decode rule, the declared-tags validation against the executor's observability list, the per-emitter attribute substitution-leaf form through which subscribers read per-emission data.

**Does NOT own:** the tag name vocabulary (the executor's observability declaration is the registry), the subscription mechanism (`concept:node-subscription`), the cascade-fire mechanism (`concept:cascade`).

**Adjacent:** `concept:executor`, `concept:signal`, `concept:node-subscription`, `concept:observability`, `concept:attribute`. Distinct from `concept:tag` (alias `template-tag`), which is a movable string alias for a template hash; the two nouns share the word but have no overlap in meaning, scope, or carrier.

## Invariants

- Tags are inert; rimsky reads them only at cascade-walk CEL evaluation and at terminal persistence.
- The tag name appears in the executor's observability-declared tag set at registration; emissions of undeclared names are rejected at the supervisor's terminal handler.
