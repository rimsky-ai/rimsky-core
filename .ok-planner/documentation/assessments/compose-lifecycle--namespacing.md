---
assessment: compose-lifecycle--namespacing
subject: story:compose-lifecycle
way: namespacing
release: d977250c
outcome: held
warrant: experiment:compose-lifecycle
---
# Keeping a manifest's resources distinguishable from hand-authored ones

After the apply, every resource the manifest created carried the manifest's own prefix in the tag, template, and instance listings, and the plan had named those identities before creating them. An operator sharing a deployment between a manifest and hand-authored work can therefore tell at a glance which resources the manifest owns — which is what makes the one-command teardown safe to run.

## Unverified remainder

None: the passing run demonstrates the way as promised.
