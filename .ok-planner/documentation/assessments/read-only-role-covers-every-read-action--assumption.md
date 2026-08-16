---
assessment: read-only-role-covers-every-read-action--assumption
subject: assumption:read-only-role-covers-every-read-action
way: assumption
release: d977250c
outcome: held
warrant: experiment:assumption-read-only-role-covers-every-read-action
---
# the `read-only` role grants every action ending in `read`, including `diagnostics:read`, `observability:read`, and `audit:read`.

As operator handing out a dashboard key, I would take it that the `read-only` role grants every action ending in `read`, including `diagnostics:read`, `observability:read`, and `audit:read`.

name-promise — the role name `read-only` against 12 `:read` actions in the registry

## What the audit ran and observed

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

## Unverified remainder

None: the passing run demonstrates the prior as stated.
