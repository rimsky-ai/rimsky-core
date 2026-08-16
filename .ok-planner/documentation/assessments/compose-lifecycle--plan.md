---
assessment: compose-lifecycle--plan
subject: story:compose-lifecycle
way: plan
release: d977250c
outcome: held
warrant: experiment:compose-lifecycle
---
# Seeing what a manifest would do before it does it

The audit drove one manifest declaring two templates, their tags, and two instances against a running deployment of `catalog:images/rimsky-all-in-one`. `catalog:cli-verbs/rimsky compose plan` listed all eight steps before any of the resources existed, and named the namespaced identities it would create. An operator therefore reads the whole change as a list before committing to it, which is what makes a multi-resource manifest safe to apply to a deployment that already has work on it. Eighteen checks ran across the five capabilities and none failed.

## Unverified remainder

None: the passing run demonstrates the way as promised.
