---
resolved_by: spec:2026-05-14-subscription-cascade-and-quality-of-life-design
tension: dependency-overloaded-bundle
category: overloaded
status: resolved
affects:
  - subscription
  - cascade
  - node
  - wait-set
---

# `dependencies:` bundled three distinct primitives

## What was muddy

The pre-2026-05-14 `dependencies:` block on a template node simultaneously declared:

1. **Read access** — which upstream attributes substitution refs could walk.
2. **Cascade subscription** — which upstream terminals propagate stale to me.
3. **Eligibility gate** — which upstreams must be `fresh` before I dispatch.

The three were coupled at template-author time and at runtime: a node that needed to read an upstream attribute had to declare it as a dependency, which also opted in to cascade fan-out and eligibility gating regardless of whether either was desired.

## Why it mattered

Templates couldn't express "I want to read this attribute but not gate dispatch on it" or "I want cascade propagation but not the read access." Operator-facing prose conflated the three meanings, making cascade behavior hard to reason about ("which dependencies actually gate me right now?").

## Resolution

Resolved by `.ok-planner/specs/2026-05-14-subscription-cascade-and-quality-of-life-design.md`. The bundle decomposes into three primitives:

- **Read access** lives in the substitution grammar (`{{nodes.X.attribute.Y}}`).
- **Cascade subscription** lives in `subscribes:` (explicit) plus auto-inferred from substitution refs (implicit). See `concept:node-subscription`.
- **Eligibility gating** lives in `rimsky_wait_set` rows populated by cascade walks and drained on settled state. See `concept:wait-set`.

The pessimistic-invalidate + drain rule keeps the eligibility surface uniform across topic kinds (state / attribute / event) without re-coupling them at declaration time.
