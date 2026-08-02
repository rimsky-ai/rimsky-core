---
story: claim-producer-scopes-conflict
---

# Operator uses non-trivial overlap rules

## Story

As an operator running templates whose claims overlap non-trivially (e.g., prefix-containment), I can use a claim-producer that advertises the scopes-conflict capability and define its overlap rule there, with rimsky consulting that rule during claim acquisition (including the fan-out sub-claim path) — two writers whose scopes byte-equally don't overlap but semantically do cannot both hold claims, so that the no-overlapping-writers rule is enforced for the producer's own overlap definition.
