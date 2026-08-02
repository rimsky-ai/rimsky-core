---
story: claim-producer-conformance
---

# Author proves producer correct via the conformance runner

## Story

As a claim-producer author shipping a custom producer, I can run the conformance suite against my producer endpoint and have the suite drive my producer through the four terminal verbs (Open / Commit / Abandon / Release) including idempotency under retry, plus the serialization-9b probe (detect dishonest internal serialization on the staged-async write-semantics), reporting pass / fail per check, so that I prove my producer is correct before shipping it.
