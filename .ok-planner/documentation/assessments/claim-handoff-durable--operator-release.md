---
assessment: claim-handoff-durable--operator-release
subject: story:claim-handoff-durable
way: operator-release
release: d977250c
outcome: held
warrant: experiment:claim-handoff-durable
---
# A durable claim is given up by a deliberate operator act, and by nothing else

Nothing released the claim on its own — no dispatch ending, no elapsed time, no producer-side reaping. Terminating the instance with `catalog:cli-verbs/rimsky instance kill` released nothing either: no Release reached the producer, the claim handle stood, and a competitor was still refused. Deleting the terminated instance with `catalog:cli-verbs/rimsky instance delete` is the act that releases: the producer received Release, the claim handle went away, and a competing instance created afterwards claimed the scope and settled fresh. The release path an operator has is therefore the explicit deletion, applied to the terminated instance.

## Unverified remainder

The story's sentence names instance termination as a release trigger; termination alone does not release, so an operator who expects a killed instance to free the scope will find it still held until the instance is deleted.
