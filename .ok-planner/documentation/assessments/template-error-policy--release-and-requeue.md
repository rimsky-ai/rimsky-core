---
assessment: template-error-policy--release-and-requeue
subject: story:template-error-policy
way: release-and-requeue
release: d977250c
outcome: held
warrant: experiment:template-error-policy
---
# Declaring that a failure should send the work back for another attempt

Under the release-and-requeue action each failure emitted its own signal and the run was dispatched again, settling neither fresh nor failed — it went back for another attempt, which is what the action names. The audit enumerated the whole declared vocabulary: all four routing actions the story names were driven and honoured, and none was left unaccounted for.

## Unverified remainder

The action was observed going back for another attempt against a deterministic failure. The demonstration does not establish an end state for a workload where the failure eventually clears.
