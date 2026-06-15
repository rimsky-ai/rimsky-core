---
story: claim-producer-conformance
status: as-is
---

# Author proves producer correct via the conformance runner

## Role

As a claim-producer author shipping a custom producer, I can run the conformance suite against my producer endpoint and have the suite drive my producer through the four terminal verbs (Open / Commit / Abandon / Release) including idempotency under retry, plus the serialization-9b probe (detect dishonest internal serialization on the staged-async write-semantics), reporting pass / fail per check, so that I prove my producer is correct before shipping it.

## Capability

Conformance runner for the claim-producer protocol: drives every terminal verb plus the serialization-discipline probe; reports pass or fail per check with non-zero exit on failure.

## Business value

Custom producer authors prove correctness against rimsky's contract before shipping; the 9b probe catches dishonest internal serialization on the staged-async write-semantics that no in-process unit test could detect.

## Acceptance

The conformance runner driven against an honest producer reports pass on each terminal verb and on the serialization-discipline probe; against a deliberately-broken producer, reports FAIL with non-zero exit and a message citing the specific check.

## Falsifier

The serialization-discipline probe passes a dishonest producer, OR a duplicate-terminal-call failure is reported as pass, OR the runner exits zero on failure.

## Proof

Executable proof.
