---
concept: terminal-tag
status: as-is
aliases: []
---

# Terminal tag

## Definition

A terminal tag is a string member of the `tags` set carried on a settling terminal verdict (Success / Error / Park). Tags are deduplicated at decode (set semantics), inert to rimsky, and serve as the discriminator subscribers match on via CEL `when:` filters over `payload.tags` on the `terminal/*` signal. The tag name MUST appear in the emitting executor's observability-advertised `declared_tags` list — the registry that template registration validates emitted tag sets against.

## Purpose

Provide a topology-visible, ledger-free discriminator on terminal verdicts. Per-emission data lives in `attributes_delta` alongside the tag, so the discriminator (tag) and the data (attributes) cleanly separate concerns.

## Boundaries

**Owns:** the set-semantics decode rule, the declared-tags validation against the executor's observability list, the substitution access at `nodes.<emitter>.attribute.<key>` for per-emission data.

**Does NOT own:** the tag name vocabulary (the executor's observability declaration is the registry), the subscription mechanism (`concept:node-subscription`), the cascade-fire mechanism (`concept:cascade`).

**Adjacent:** `concept:executor`, `concept:signal`, `concept:node-subscription`, `concept:observability`, `concept:attribute`. Distinct from `concept:tag` (alias `template-tag`), which is a movable string alias for a template hash; the two nouns share the word but have no overlap in meaning, scope, or carrier.

## Invariants

- Tags are inert; rimsky reads them only at cascade-walk CEL evaluation and at terminal persistence.
- The tag name appears in the executor's observability-declared tag set at registration; emissions of undeclared names are rejected at the supervisor's terminal handler.
