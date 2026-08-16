---
experiment: assumption-permission-actions-cover-full-crud
commit: d977250c
---

# Does every noun in the action registry carry a full verb set?

## What it ran against

One `rimsky-all-in-one` container from the tree's own image tag. It submits a
key-creation request for each of the three actions the prior names, sixteen more
the same reasoning reaches for, all 46 grantable registry actions, the three
non-grantable ones, and nine wildcard shapes — then probes the routes those
missing verbs would gate.

## What was observed

None of the three verbs the prior names exists. `instance:update`, `asset:create`
and `message:delete` are each rejected 400 `{"error": "unknown action: …"}` at key
creation. So are sixteen siblings: `instance:delete`, `asset:update`,
`message:update`, `node:update`, `node:delete`, `tag:update`, `template:update`,
`template:delete`, `run:create`, `run:delete`, `event:write`, `event:delete`,
`audit:write`, `breakpoint:update`, `lineage:write` and `auth:update`.

The registry is closed and complete for what exists: all 46 grantable actions are
accepted. The three that are real but never grantable refuse with their own
reason rather than "unknown action" — `health:probe` and `peer-auth:ca-root`
"carries the unauthenticated posture, so no permission is ever consulted for it",
`auth:whoami` "carries the identity-only posture".

The missing verbs are missing operations, not missing permissions.
`PUT /v1/instances/{id}`, `PATCH /v1/instances/{id}`,
`POST /v1/instances/{id}/assets` and `DELETE /v1/messages/{id}` all answer 405:
the routes those verbs would gate do not exist. And the update verb the prior
would look for does exist under a different name — `PUT /v1/tags/{tag}` updates a
tag, gated by `tag:set`; `tag:update` is unknown.

The wildcard grammar is closed the same way: `*`, `<noun>:*` and `*:<verb>` are
accepted; `*:*`, `inst*:read`, `instance:re*`, `instanceread` and
`instance:read:extra` are each rejected with a message naming the rule.

EXPERIMENT PASS (37 checks)
