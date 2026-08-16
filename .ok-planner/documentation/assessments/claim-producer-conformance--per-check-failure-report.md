---
assessment: claim-producer-conformance--per-check-failure-report
subject: story:claim-producer-conformance
way: per-check-failure-report
release: d977250c
outcome: held
warrant: experiment:claim-producer-conformance
---
# A failing producer is told which checks failed, not just that it failed

The same suite was pointed at a producer deliberately rejecting a retried terminal verb. Exactly the three retry checks failed, each named, while their first-call counterparts still reported passing — so the report separates "your Commit works" from "your Commit is not idempotent under retry". The run printed how many of the 16 checks failed and the command exited 1, giving the author both a machine-readable verdict and a per-check list to work from.

## Unverified remainder

None: the passing run demonstrates the way as promised.
