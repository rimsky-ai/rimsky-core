---
decision: byte-equal-conflict-default
---

# Byte equality is the default claim-scope conflict predicate

## Choice

Rimsky compares claim scopes by byte equality to decide whether two claims conflict. A producer that advertises the scopes-conflict capability supplies its own overlap predicate, and rimsky asks that predicate instead (see `concept:claim-scope`).

## Rationale

Rimsky knows nothing about a producer's selector language. One producer's selectors are globs, another's are row ranges, another's name a private namespace. Byte equality is the one predicate rimsky evaluates without producer-specific code, so it works uniformly across producers rimsky has never seen.

The default also puts canonicalization where the knowledge is. A producer that wants two different selectors to conflict either canonicalizes them to the same bytes at open or advertises its own overlap predicate and answers the question directly. Rimsky persists the bytes the producer returned without normalizing them, so the audit trail shows exactly what the producer committed to representing.

## Alternatives

- Require every producer to expose overlap semantics rimsky interprets — rejected: rimsky would model selector languages it cannot know, and a simple producer would carry a predicate it does not need.
- Normalize claim-scope bytes at the rimsky persistence boundary — rejected: the persisted scope then differs from what the producer returned, so the audit trail stops answering what was acquired.
