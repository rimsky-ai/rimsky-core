---
assessment: grant-scope-enforcement--scoped-grant
subject: story:grant-scope-enforcement
way: scoped-grant
release: d977250c
outcome: held
warrant: experiment:grant-scope-enforcement
---
# Scoping a delegated key's grant to one template tag

An admin key from `catalog:cli-verbs/rimsky auth init` delegated to a per-tenant key created with `catalog:cli-verbs/rimsky auth create-key` and `catalog:cli-flags/--role-file`, whose grant scoped seven actions to one template tag and granted unscoped reads; the grant read back off the key with all seven entries carrying that scope. Each scopeable action was then driven twice from the tenant key — once at the in-scope tag, once at a second tag the admin owned — across the resource's whole lifecycle: `catalog:permission-actions/template:register`, `catalog:permission-actions/template:deploy`, `catalog:permission-actions/tag:set`, `catalog:permission-actions/tag:delete`, `catalog:permission-actions/instance:create` by tag, instance creation by the template's own content hash, `catalog:permission-actions/template:undeploy` and `catalog:permission-actions/template:deregister`. Every in-scope call succeeded and every out-of-scope call was refused, the hash-addressed creation included, so naming the resource by id rather than by tag does not evade the scope. The second template survived every out-of-scope attempt and still read for the admin. The tenant key stayed a working key for what it was granted while being refused what it was not, so the delegation is narrow rather than broken.

## Unverified remainder

None: the passing run demonstrates the way as promised, across the full lifecycle of the scoped resource.
