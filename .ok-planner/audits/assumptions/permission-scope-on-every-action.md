---
assumption: permission-scope-on-every-action
commit: d977250c
disposition: trap
synthesized: 2026-08-16T05:48:16Z
---

# the optional scope selector works on any action, so a key can be pinned to one instance, one template, or one tag equally.

As operator delegating narrowly, I would take it that the optional scope selector works on any action, so a key can be pinned to one instance, one template, or one tag equally.

## Source

published-concept — `concept:permission` ("an optional scope (a resource selector evaluated alongside the action match)", "per-action resource scoping")

## What a run would observe

mint keys scoped to a specific instance, template, and tag and test each against in-scope and out-of-scope targets.

## Measured

Ran `experiments/assumption-permission-scope-on-every-action` (44 checks, pass)
against one `rimsky-all-in-one` container at this tree, trying a scope-bearing
grant on every one of the 46 grantable actions and six scope dimensions on an
action that takes one.

Scope works on seven actions out of 46, and every one of them is template- or
tag-shaped: `instance:create`, `tag:delete`, `tag:set`, `template:deploy`,
`template:deregister`, `template:register`, `template:undeploy`. The other
thirty-nine refuse a scope at key creation with
`grant entry 0: action "instance:read" does not support scope`.

There is exactly one scope dimension and it is the template tag. On `tag:delete`,
`template_id`, `instance_id`, `instance_key`, `tag`, `node_type` and an invented
key are each rejected `unknown scope dimension "…" for action "tag:delete"`; only
`template_tag` is accepted. Even `instance:create`, which does take a scope,
refuses `instance_id` — it is scoped by the tag of the template it instantiates,
not by the instance it creates.

So the operator delegating narrowly cannot pin a key to one instance at all:
`instance:read`, `instance:terminate`, `instance:pause`, `instance:kill`,
`message:send` and `node:reset` each refuse both `instance_id` and
`instance_key`, twelve combinations, all 400. A key pinned to one template tag is
the only narrowing rimsky offers, and `concept:permission`'s "per-action resource
scoping" reads far wider than seven actions and one dimension.

The failure mode is the good one: the refusal is at mint time with a message
naming the action, the dimension and the valid dimensions, so nobody ships a key
they believe is scoped and is not. And where scope is accepted it is exactly
least-privilege — a key scoped to `mine:v1` is refused 403 deleting `theirs:v1`
and succeeds on `mine:v1`, and a deploy scope reaches a template through its tags
rather than around them.
