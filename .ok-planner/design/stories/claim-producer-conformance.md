---
story: claim-producer-conformance
status: as-is
---

# Author proves producer correct via conformance CLI

## Role

As a claim-producer author shipping a custom producer, I can run `rimsky conformance claim-producer --endpoint <my-producer>` and have the suite drive my producer through the four terminal verbs (Open / Commit / Abandon / Release) including idempotency under retry, plus the serialization-9b probe (detect dishonest internal serialization on `staged_async`), reporting pass / fail per check, so that I prove my producer is correct before shipping it.

## Capability

Conformance CLI for the claim-producer protocol: drives every terminal verb plus the serialization-9b probe; reports pass / fail per check with non-zero exit on failure.

## Business value

Custom producer authors prove correctness against rimsky's contract before shipping; the 9b probe catches dishonest internal serialization on `staged_async` that no in-process unit test could detect.

## Acceptance

The conformance CLI driven against an honest producer reports pass on each terminal verb and on the 9b probe; against a deliberately-broken producer, reports FAIL with non-zero exit and a message citing the specific check.

## Falsifier

The 9b probe passes a dishonest producer, OR a duplicate-terminal-call failure is reported as pass, OR the CLI exits zero on failure.

## Proof

Executable proof.

## Notes

2026-06-08 — Story landed via spec 2026-06-08-design-corpus-bootstrap.
