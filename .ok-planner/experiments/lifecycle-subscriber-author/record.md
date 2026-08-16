---
experiment: lifecycle-subscriber-author
commit: d977250c
---

# Seven callbacks, each already delivered when the call returns

## What it ran against

`peer/` is a third-party lifecycle subscriber: its own Go module whose only
rimsky requirement is the protocols module, built by the run the way the
permissive-peer-build experiment's executor is, and carrying that executor as
its second role because a lifecycle subscriber is registered as a protocol
alongside another peer role and only peers a template names receive the
callbacks. It records every callback with the context rimsky handed it.
`run.sh` wires it into a `rimsky-all-in-one` stack from the tree's own image tag
by declaring `protocols: ["executor", "lifecycle_subscriber"]`, mints an owner
key so the created instance has an owner, and walks one instance through every
transition.

## What was observed

Thirty-seven checks, none failing. Nothing fired before anything happened. Then
each control-API call had already delivered its callback by the time it returned
— checked without waiting, which is what synchronous means from the caller's
side — for template registered, template deployed, instance created, instance
terminated (delivered on the instance delete that follows a terminate), template
undeployed, and template deregistered: six of the seven. The run-scope terminal
callback has no caller call to be synchronous with and arrived from the runtime
when the instance's frame settled, which the run waited for.

Each carried what the story names: the template hash on all four template
callbacks and the spec on registration, the deployment's tags on deploy, and on
instance creation the instance id, template hash, instance key, params, the
service bindings the caller supplied, the owner key that created the instance,
and the routing identity it was created under. The run-scope callback carried
the run-scope id, the instance, and the terminal reason (`frame_settled`); the
termination callback carried the instance, its template and the time it
terminated.

All seven distinct callbacks fired, in the order the transitions happened.
