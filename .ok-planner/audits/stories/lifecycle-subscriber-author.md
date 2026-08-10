---
audit: lifecycle-subscriber-author
artifact: story:lifecycle-subscriber-author
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T07:45:00Z
---

# All seven lifecycle callbacks fire at their transition, carrying their context

Supported. A subscriber written as its own module against the protocols module
was registered by declaring the lifecycle-subscriber protocol beside its
executor role, and all seven callbacks fired, in the order the transitions
happened. Six of them had already been delivered by the time the control-API
call returned — template registered, deployed, undeployed and deregistered,
instance created, and instance terminated on the delete that follows a
terminate — which is what synchronous means from the caller's side; the
run-scope terminal callback arrived from the runtime when the instance's frame
settled. Each carried the context the story names: the template hash on every
template callback and the spec on registration, the tags on deploy, and on
instance creation the instance id, template hash, instance key, params, the
service bindings supplied, the owner key that created it and its routing
identity. The run-scope callback carried the scope id, the instance and the
terminal reason; the termination callback carried the instance, its template and
when it terminated.
