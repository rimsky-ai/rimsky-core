---
assessment: client-context--register
subject: story:client-context
way: register
release: d977250c
outcome: held
warrant: experiment:client-context
---
# Registering several deployments by name

Two independent deployments of `catalog:images/rimsky-all-in-one` were booted, each seeded with a distinct template while still addressed explicitly, and each registered by name with `catalog:cli-verbs/rimsky ctx add`. After those two calls, no later command in the run named an endpoint. The CLI ran against an empty home directory with every rimsky environment variable unset, so nothing outside the registrations could have supplied a connection detail. Sixteen checks ran and none failed.

## Unverified remainder

None: the passing run demonstrates the way as promised.
