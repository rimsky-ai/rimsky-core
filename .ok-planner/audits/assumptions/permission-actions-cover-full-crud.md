---
assumption: permission-actions-cover-full-crud
commit: PENDING
disposition: trap
synthesized: 2026-08-16T05:48:16Z
---

# every noun in the action registry carries the full verb set it plausibly needs — an instance can be updated, an asset can be created, a message can be deleted.

As operator minting a scoped key, I would take it that every noun in the action registry carries the full verb set it plausibly needs — an instance can be updated, an asset can be created, a message can be deleted.

## Source

sibling-symmetry — `instance:create/read/terminate/…` with no `instance:update`; `asset:read/delete` with no `asset:create`; `message:send/read` with no delete

## What a run would observe

submit a key-creation request naming `instance:update` and `asset:create` and see whether the registry rejects them.

## Measured

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
