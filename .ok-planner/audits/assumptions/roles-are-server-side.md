---
assumption: roles-are-server-side
commit: PENDING
disposition: trap
synthesized: 2026-08-16T05:48:16Z
---

# the six bundled roles are entities the server knows about, so `GET /v1/auth/keys/{id}` reports which role a key holds and roles can be listed from the API.

As operator auditing access, I would take it that the six bundled roles are entities the server knows about, so `GET /v1/auth/keys/{id}` reports which role a key holds and roles can be listed from the API.

## Source

name-promise — six named `bundled-roles` presented as first-class surface elements

## What a run would observe

create a key with `--role operator`, then read the key back over HTTP and look for a role field in the response.

## Measured

Experiment `assumption-roles-are-server-side`, re-run at this tree against one
`rimsky-all-in-one` container. A key minted with `rimsky auth create-key --role
operator` reads back over `GET /v1/auth/keys/op` as 200 with five fields — `id`,
`name`, `permissions`, `created_at`, `created_by_key_id` — none named for a role
and none carrying the string "operator". What the key holds instead is the
expanded grant: 16 entries, `instance:*`, `template:*`, `tag:*`, `node:*` and the
rest. `GET /v1/auth/keys` carries the same five fields. No route lists the roles:
`GET /v1/auth/roles`, `GET /v1/roles`, `GET /v1/auth/keys/op/role` and
`GET /v1/auth/role-templates` each answer 404. The CLI expands the role name
before it contacts the server — `--role nonesuch` fails with `unknown bundled
role "nonesuch" (available: admin, agent-supervisor, debug-operator, operator,
publisher-service, read-only)` even with `--endpoint` pointed at a dead port,
while `--role read-only` against that same dead port gets as far as `connection
refused`. The `role:` column `rimsky auth list` prints is a client-side match on
the grant: a key minted over HTTP with `[{"action": "*:read"}]`, never naming a
role, still lists as `role:read-only`, and a `read-only` role patched with
`--add instance:create` lists as `custom`. An operator auditing access over the
API sees grants, never roles, and two keys created from different sources with
the same grant are indistinguishable.
