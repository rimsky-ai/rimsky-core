---
story: claim-producer-scopes-conflict
status: as-is
---

# Operator uses non-trivial overlap rules

## Role

As an operator running templates whose claims overlap non-trivially (e.g., prefix-containment), I can use a claim-producer that advertises the scopes-conflict capability and define its overlap rule there, with rimsky consulting that rule during claim acquisition (including the fan-out sub-claim path) — two writers whose scopes byte-equally don't overlap but semantically do cannot both hold claims, so that the no-overlapping-writers rule is enforced for the producer's own overlap definition.

## Capability

Producer-declared scopes-conflict capability: rimsky consults the producer's scopes-conflict predicate during claim acquisition (including the fan-out sub-claim path) so semantic overlap is enforced even when scopes are byte-distinct.

## Business value

Operators run templates whose claims overlap non-trivially; the safety invariant guarding "no two writers on overlapping scopes" extends to producers whose overlap definition is semantic, not byte-equal.

