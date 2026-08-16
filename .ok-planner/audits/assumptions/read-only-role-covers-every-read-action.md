---
assumption: read-only-role-covers-every-read-action
commit: d977250c
disposition: held
synthesized: 2026-08-16T05:48:16Z
---

# the `read-only` role grants every action ending in `read`, including `diagnostics:read`, `observability:read`, and `audit:read`.

As operator handing out a dashboard key, I would take it that the `read-only` role grants every action ending in `read`, including `diagnostics:read`, `observability:read`, and `audit:read`.

## Source

name-promise — the role name `read-only` against 12 `:read` actions in the registry

## What a run would observe

mint a `read-only` key and call each `:read`-gated route with it, recording the denials.

## Measured

Experiment `assumption-read-only-role-covers-every-read-action`, re-run at this
tree against one `rimsky-all-in-one` container. `rimsky auth create-key --role
read-only` expands to exactly one grant entry, `[{"action": "*:read"}]`, and that
entry grants all 18 actions ending in `:read`. Each was driven at a route it
gates and none was refused: `instance:read`, `breakpoint:read`, `template:read`,
`tag:read`, `node:read`, `run:read`, `message:read`, `event:read`, `audit:read`,
`lineage:read`, `parked-node:read`, `waitset:read`, `claim-holders:read`,
`asset:read`, `diagnostics:read`, `auth:read`, `observability:read`, `mcp:read`.
The three the prior names by hand — `diagnostics:read`, `observability:read`,
`audit:read` — are among them. The same key is refused six writes with 403.
Two adjacent observations that do not bear on the prior as written: `auth:read`
falls inside the role, so a dashboard key lists every api-key row; and the two
read-shaped actions whose verb is not `read` — `instance:list-frames` and
`instance:read-frame` — answer 403, because the wildcard matches the whole verb
rather than a prefix of it.
