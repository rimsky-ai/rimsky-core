---
assessment: claim-scope-substitution--canonical-resolves
subject: story:claim-scope-substitution
way: canonical-resolves
release: d977250c
outcome: held
warrant: experiment:claim-scope-substitution
---
# The canonical spelling resolves to the live claim's scope

The audit drove a deployment of `catalog:images/rimsky-all-in-one` with `catalog:bundled-services/claim-producer-filesystem` over a mounted workspace, on a node that acquires a claim and passes through whatever the substitution resolved to. Six checks ran and none failed. The canonical alias-keyed claim-scope spelling resolved to the claim's scope bytes exactly as the claim-handle ledger recorded them for that live claim. Written with a deliberately non-canonical selector, the directive still resolved to the producer's canonical scope rather than to the template's selector text, so the value follows the claim rather than the words the author typed.

## Unverified remainder

None: the passing run demonstrates the way as promised.
