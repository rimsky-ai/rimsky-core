---
story: claim-producer-conformance
status: as-is
---

# Author proves producer correct via the conformance runner

## Story

As a claim-producer author shipping a custom producer, I can run the conformance suite against my producer endpoint and have the suite drive my producer through the four terminal verbs (Open / Commit / Abandon / Release) including idempotency under retry, plus the serialization-9b probe (detect dishonest internal serialization on the staged-async write-semantics), reporting pass / fail per check, so that I prove my producer is correct before shipping it.

Conformance runner for the claim-producer protocol: drives every terminal verb plus the serialization-discipline probe; reports pass or fail per check with non-zero exit on failure.

Custom producer authors prove correctness against rimsky's contract before shipping; the 9b probe catches dishonest internal serialization on the staged-async write-semantics that no in-process unit test could detect.
