---
concept: terminal-tag
---

# Terminal tag

## What it is

A terminal tag is a string member of the tag set the executor attaches to a settling verdict — a `terminal/success`, a `terminal/error/<class>`, or a park (the audit-only `transient/park`). Tags are deduplicated at decode (set semantics) and serve as the ephemeral discriminator subscribers match on via CEL filters over `payload.tags`; a tag never merges into node-attribute state. The tag name MUST belong to the emitting executor's observability-advertised declared-tag set, enforced in two gates: at template registration, tag literals referenced in a subscription's `payload.tags` filter are checked against the sender's declared-tag set and an undeclared reference is rejected, refusing registration; at runtime, every tag actually carried by a settling verdict is checked against the same declared-tag set.

## Purpose

Provide a topology-visible, ledger-free, **emission-scoped** discriminator on the executor's verdict. Tags carry a per-emission label whose lifetime is the emission itself: a tag rides the audit row and, on run-terminating signals, the subscriber's CEL evaluation; it never merges into node-attribute state. This separates the discriminator role (ephemeral tag) from the state-mutation role (persistent attribute), which the same verdict carries side by side via `concept:attribute`'s attributes-delta channel. Subscribers that want to fire on "this terminal was a rate-limit" predicate against tags; subscribers that want to fire on "this terminal changed an attribute to X" predicate against attributes-delta. Each role has its own slot with its own lifecycle.

## Boundaries

**Owns:** the set-semantics decode rule, the declared-tags validation against the executor's observability list, the emission-scoped (non-persistent) lifecycle of the tag set on every verdict payload that carries one.

**Does NOT own:** the tag name vocabulary (the executor's observability declaration is the registry), the subscription mechanism (`concept:node-subscription`), the cascade-fire mechanism (`concept:cascade`), the persistent attribute mutation that a settling terminal may also carry (`concept:attribute`).

**Adjacent:** `concept:executor`, `concept:signal`, `concept:node-subscription`, `concept:observability`, `concept:attribute`. Distinct from `concept:tag` (alias `template-tag`), which is a movable string alias for a template hash; the two nouns share the word but have no overlap in meaning, scope, or carrier.

## Invariants

- A declared tag is inert to node state past the runtime declared-tag gate: rimsky reads it only at cascade-walk CEL evaluation (on run-terminating signals) and at signal persistence. A tag never merges into the per-run attribute ledger and never carries forward to the next dispatch. An undeclared tag is not inert — it is itself the trigger the runtime declared-tag gate acts on (see below), a distinct read site from cascade-walk and persistence.
- Tag enforcement runs in two gates. At template registration, tag literals referenced in a subscription's `payload.tags` filter are checked against the sender executor's declared-tag set; an undeclared reference is rejected as a registration error, so the subscription never registers. At runtime, as the executor's wire outcome is resolved into its internal settling verdict — before that verdict reaches terminal dispatch — every tag the verdict actually carries is checked against the same declared-tag set; any undeclared tag converts the verdict into an `executor_protocol_violation` error terminal.
- Tags ride the run-terminating `terminal/success` and `terminal/error/<class>` payloads alongside the attributes-delta slot; the two are independent and compose freely in a CEL `when:` filter. Tags also ride `transient/park` payloads (a single leaf — see `concept:signal`) for audit forensics, but park signals are audit-only — subscribers cannot fire on `payload.tags` from a park; the tag-as-discriminator role surfaces to subscribers only via the eventual run-terminating settlement.
