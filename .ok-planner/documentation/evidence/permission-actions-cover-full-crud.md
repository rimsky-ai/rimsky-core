---
trap: permission-actions-cover-full-crud
release: d977250c
---
# Evidence set — every noun in the action registry carries the full verb set it plausibly needs — an instance can be updated, an asset can be created, a message can be deleted.

Source of the prior: sibling-symmetry — `instance:create/read/terminate/…` with no `instance:update`; `asset:read/delete` with no `asset:create`; `message:send/read` with no delete

## What the audit ran and observed (assumption record)

Ran `experiments/assumption-permission-actions-cover-full-crud` (37 checks, pass)
against one `rimsky-all-in-one` container at this tree, submitting a
key-creation request for each candidate action and then probing the routes the
missing verbs would gate.

The three verbs the prior names do not exist. `instance:update`, `asset:create`
and `message:delete` are each rejected 400 `{"error": "unknown action: …"}`, and
so are sixteen siblings the same reasoning reaches for, including
`instance:delete`, `tag:update`, `template:update`, `node:delete`, `run:create`
and `auth:update`. The registry is a closed set: all 46 grantable actions are
accepted, and the three real-but-never-grantable ones refuse with their own
reason rather than "unknown action" — `health:probe` and `peer-auth:ca-root`
carry "the unauthenticated posture, so no permission is ever consulted for it",
`auth:whoami` "the identity-only posture".

The contradiction is real but shallow, and the record should say why. The missing
verbs are missing operations, not missing permissions: `PUT` and `PATCH` on an
instance, `POST` on an instance's assets, and `DELETE` on a message all answer
405 — no route exists for the operations those verbs would gate. And the update
verb the prior would look for does exist under another name:
`PUT /v1/tags/{tag}` updates a tag and is gated by `tag:set`, while `tag:update`
is unknown. The operator learns all of this at mint time, from a precise error,
rather than discovering it at request time.

The wildcard grammar is closed identically — `*`, `<noun>:*` and `*:<verb>` are
accepted; `*:*`, `inst*:read`, `instance:re*`, `instanceread` and
`instance:read:extra` each refused with a message naming the rule.

## Experiment record (experiment:assumption-permission-actions-cover-full-crud)

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

Runnables: `src:.ok-planner/experiments/assumption-permission-actions-cover-full-crud/` at the stamped commit.
