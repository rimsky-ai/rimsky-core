---
decision: tag-based-subscription
status: as-is
aliases: []
---

# Subscriptions move to terminal/* + CEL tag filter

## Choice

The `event/<name>` leaf is removed from `concept:signal`'s taxonomy. Subscribers that historically expressed `subscribes: [{node: <sender>, type: event/<name>}]` shift to `subscribes: [{node: <sender>, type: terminal/*, when: "<name>" in payload.tags}]`. The CEL `when:` filter on `payload.tags` (bound to the terminal's tag set) replaces the type-path leaf. The CEL environment for `terminal/*` payloads binds `tags: list<string>` so subscribers can use the `in` predicate.

## Rationale

With tags collapsing the per-emission ledger into terminal-level metadata, the subscription surface follows. Type-path subscription can no longer express "this specific named event"; it expresses "this terminal kind" plus a CEL filter on the tag set.

## Alternatives

Synthesize `event/<name>` as a virtual leaf computed from tag presence — rejected because it adds parser complexity for a taxonomy that no longer matches the persistence.
