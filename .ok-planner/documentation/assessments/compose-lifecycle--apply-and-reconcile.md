---
assessment: compose-lifecycle--apply-and-reconcile
subject: story:compose-lifecycle
way: apply-and-reconcile
release: d977250c
outcome: held
warrant: experiment:compose-lifecycle
---
# Applying the manifest, and applying it again without repeating the work

`catalog:cli-verbs/rimsky compose up` performed the eight steps the plan had listed, after which the tag, template, and instance listings carried the manifest's resources with the templates deployed. Reconciliation was settled by consequence: a second apply reported no changes rather than re-creating anything, so the verb drives the deployment towards the declaration instead of replaying a script. That is what lets an operator run the same command after an edit and get only the difference.

## Unverified remainder

The compose verbs send no credential, so this demonstration was taken on a deployment in the shipped default posture — the only posture in which they work. On a deployment with authentication enabled they fail unauthorized under every key-passing mechanism the CLI offers, while an ordinary verb with the same key succeeds.
