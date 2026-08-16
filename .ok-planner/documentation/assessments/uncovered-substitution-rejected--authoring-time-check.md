---
assessment: uncovered-substitution-rejected--authoring-time-check
subject: story:uncovered-substitution-rejected
way: authoring-time-check
release: d977250c
outcome: held
warrant: experiment:uncovered-substitution-rejected
---
# The same finding is available before any registration is attempted

The audit put the same template through `catalog:http-routes/POST /v1/templates/validate` and got the same finding, with a not-ok verdict, before any registration was attempted. An author can therefore fix the wiring while writing the template rather than by trying to register it, and the two answers agree — the check is one check reached from two places, not two checks that could drift.

## Unverified remainder

None: the passing run demonstrates the way as promised.
