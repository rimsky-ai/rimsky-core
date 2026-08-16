---
assessment: data-processing-author--conformance
subject: story:data-processing-author
way: conformance
release: d977250c
outcome: held
warrant: experiment:data-processing-author
---
# Proving the typed-data mix-in as written

A claim producer carrying the typed-data mix-in was written as its own module depending on the published protocols alone, and it advertises the data-processing protocol alongside the claim-producer protocol. `catalog:cli-verbs/rimsky conformance data-processing` passed all ten of its checks against that producer — capabilities, begin-then-commit, begin idempotency, the three abandon checks, the three listing smokes, and concurrent writes — and `catalog:cli-verbs/rimsky conformance claim-producer` passed its own suite alongside. An author therefore proves both halves of what they implemented with shipped verbs, before wiring the producer to any deployment. Seventeen checks ran across the story's three ways and none failed.

## Unverified remainder

None: the passing run demonstrates the way as promised across all ten checks the data-processing suite runs.
