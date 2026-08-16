---
experiment: assumption-roles-are-server-side
commit: PENDING
---

# Does the server know the six bundled roles?

## What it ran against

One `rimsky-all-in-one` container from the tree's own image tag, bootstrapped
with `rimsky auth init`. It mints a key with `rimsky auth create-key --role
operator`, reads the key back over `GET /v1/auth/keys/{nameOrID}` and
`GET /v1/auth/keys`, probes four candidate role routes, drives the CLI's role
expansion against a dead endpoint, and compares a CLI-minted key against a key
minted over HTTP with the same grant and no role name.

## What was observed

The server stores no role. `GET /v1/auth/keys/op` answers 200 with exactly five
fields — `id`, `name`, `permissions`, `created_at`, `created_by_key_id` — none
named for a role and none carrying the string "operator". What the key carries
instead is the expanded grant: 16 entries, `instance:*`, `template:*`, `tag:*`,
`node:*` and the rest, with no name for the set. The key listing has the same
five fields. Note the expanded operator grant includes `backfill:*`, a wildcard
over a noun the action registry does not know.

No route lists the roles. `GET /v1/auth/roles`, `GET /v1/roles`,
`GET /v1/auth/keys/op/role` and `GET /v1/auth/role-templates` each answer 404.

The CLI expands the role before it contacts the server. `--role nonesuch` fails
client-side with `unknown bundled role "nonesuch" (available: admin,
agent-supervisor, debug-operator, operator, publisher-service, read-only)`, and
the same refusal arrives with `--endpoint` pointed at a dead port, while
`--role read-only` against that dead port gets as far as `connection refused`.

The role label the CLI prints is a client-side match on the grant. A key minted
over HTTP with `[{"action": "*:read"}]`, never naming a role, still reads back in
`rimsky auth list` as `role:read-only`. A `read-only` role patched with
`--add instance:create` reads back as `custom`. Server-side the CLI-role key and
the raw-grant key are identical: both carry `[{"action": "*:read"}]`.

EXPERIMENT PASS (18 checks)
