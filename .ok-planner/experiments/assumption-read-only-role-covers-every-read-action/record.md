---
experiment: assumption-read-only-role-covers-every-read-action
commit: d977250c
---

# Does the read-only role grant every action ending in :read?

## What it ran against

One `rimsky-all-in-one` container from the tree's own image tag, bootstrapped
with `rimsky auth init` and seeded with a template, a tag and an instance. It
mints a key with `rimsky auth create-key --role read-only`, reads the grant back
over `GET /v1/auth/keys/dashboard`, then drives that key at one route per action
for all 18 actions ending in `:read`, at six writes, and at the two read-shaped
actions whose verb is not `read`.

## What was observed

The role expands to exactly one grant entry, `[{"action": "*:read"}]`, and that
entry covers all 18 actions ending in `:read`. Every one answered without an
authorization refusal: `instance:read`, `breakpoint:read`, `template:read`,
`tag:read`, `node:read`, `run:read`, `message:read`, `event:read`, `audit:read`,
`lineage:read`, `parked-node:read`, `waitset:read`, `claim-holders:read`,
`asset:read`, `diagnostics:read`, `auth:read`, `observability:read`, `mcp:read`.
The three the prior names — `diagnostics:read`, `observability:read`,
`audit:read` — are among them. Non-2xx answers came from the absent identifiers
and empty query the probe deliberately supplied (404 on `run:read` and
`lineage:read`, 400 on `waitset:read`), never from the gate.

`auth:read` is inside the role: a read-only key lists every api-key row through
`GET /v1/auth/keys`, names, ids, grants and all.

The same key is refused every write: `template:register`, `instance:create`,
`instance:kill`, `node:reset`, `auth:create` and `tag:create` each answered 403.

Two read-shaped actions sit outside the role, because the wildcard matches the
whole verb and their verb is not `read`: `instance:list-frames`
(`GET /v1/instances/{id}/frames`) and `instance:read-frame`
(`GET /v1/instances/{id}/frames/{frame_id}`) each answered 403 with
`permission denied`. The two ungated routes, `GET /v1/auth/whoami` and
`GET /v1/health`, answered 200.

EXPERIMENT PASS (30 checks)
