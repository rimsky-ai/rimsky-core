---
experiment: assumption-permission-scope-on-every-action
commit: d977250c
---

# Does the scope selector work on any action?

## What it ran against

One `rimsky-all-in-one` container from the tree's own image tag, seeded with two
templates, two tags and one instance. It tries a scope-bearing grant on every one
of the 46 grantable actions, tries six scope dimensions on an action that accepts
one, tries to pin six instance-shaped actions to a specific instance, and then
exercises the scopes that are accepted against in-scope and out-of-scope targets.

## What was observed

Scope works on seven actions out of 46, and they are all template- and
tag-shaped: `instance:create`, `tag:delete`, `tag:set`, `template:deploy`,
`template:deregister`, `template:register`, `template:undeploy`.

The other thirty-nine refuse a scope at key creation, loudly and precisely:
`grant entry 0: action "instance:read" does not support scope`. Seventeen were
checked by name, spanning instances, nodes, messages, assets, breakpoints,
events, audit, templates, tags, auth and observability.

There is exactly one scope dimension, and it is the template tag. On
`tag:delete`, `template_id`, `instance_id`, `instance_key`, `tag`, `node_type`
and an invented key are each rejected with
`unknown scope dimension "…" for action "tag:delete"`; only `template_tag` is
accepted. Even `instance:create`, which does take a scope, refuses
`instance_id` — it is scoped by the tag of the template it instantiates.

So an operator cannot pin a key to one instance at all: `instance:read`,
`instance:terminate`, `instance:pause`, `instance:kill`, `message:send` and
`node:reset` each refuse both `instance_id` and `instance_key`, twelve
combinations, all 400 at mint time.

Where scope is accepted it behaves exactly as least-privilege. A key granted
`*:read` plus `tag:delete` scoped to `mine:v1` is refused 403 on
`DELETE /v1/tags/theirs:v1` and succeeds on `DELETE /v1/tags/mine:v1`. A key
scoped to deploy `mine:v1` deploys the template carrying that tag at 200 and is
refused 403 deploying an untagged template — the scope reaches a template through
its tags rather than around them.

EXPERIMENT PASS (44 checks)
