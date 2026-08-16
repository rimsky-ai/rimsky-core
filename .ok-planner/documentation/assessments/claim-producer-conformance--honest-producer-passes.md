---
assessment: claim-producer-conformance--honest-producer-passes
subject: story:claim-producer-conformance
way: honest-producer-passes
release: d977250c
outcome: held
warrant: experiment:claim-producer-conformance
---
# Proving a correct producer with nothing but the producer and the CLI

A claim producer written against the published protocol was started on loopback and `catalog:cli-verbs/rimsky conformance claim-producer` was pointed at its endpoint with `catalog:cli-flags/--endpoint`. The suite drew 16 checks and reported one passing row each, covering the four terminal verbs the story names, the three retried-terminal idempotency checks, and the serialization probe, and the command exited 0. No deployment and no container were involved: an author needs only their producer running and the shipped CLI, which is what makes this a step they can take before shipping rather than after.

## Unverified remainder

None: the passing run demonstrates the way as promised.
