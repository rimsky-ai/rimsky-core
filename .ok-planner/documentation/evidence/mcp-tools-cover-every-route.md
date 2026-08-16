---
trap: mcp-tools-cover-every-route
release: d977250c
---
# Evidence set — the MCP tool catalog covers every operation the REST surface exposes, so nothing requires dropping to HTTP.

Source of the prior: published-concept — `concept:control-api` ("multiple protocol skins on the same TCP port and the same operation set", catalog "computed from the canonical action registry")

## What the audit ran and observed (assumption record)

Ran `experiments/assumption-mcp-tools-cover-every-route` (16 checks, pass) against
one `rimsky-all-in-one` container at this tree: opened an MCP session, called all
57 advertised tools, drove 61 HTTP routes across every family, and compared the
permission actions each skin reached using the deployment's own audit log, which
stamps every request with its action and `protocol_skin`.

The coverage claim is very nearly right, and `concept:control-api` is borne out
where it matters: the 57 tools reached 43 distinct permission actions, the 61
routes reached 44, and the only action HTTP reaches that MCP does not is
`mcp:read` — the MCP transport's own gate. MCP reaches nothing HTTP cannot.
`observability_get` covers the whole `/v1/observability/*` family through one
`path_suffix` argument.

Two operations still require dropping to HTTP. `GET /v1/health` and
`GET /v1/auth/whoami` both answer 200 over HTTP and neither has a tool: no tool
is named for health, whoami or the CA root, and neither `health:probe` nor
`auth:whoami` appears in the MCP action set. Both are ungated, which is why they
fall outside a catalog computed from the action registry — and both are exactly
what an agent reaches for first: is this deployment up, and who am I.
`observability_get("system/health")` is a different resource behind a different
permission, not a substitute.

Separately, the two teardown tools are named the inverse of their routes.
`instance_terminate` is described as "Delete an already-terminal instance and its
rows; refused with 409 while the instance is still running"; `instance_kill` as
"Force a running instance terminal". The audit log shows the same inversion on
the wire: `POST /v1/instances/{id}/terminate` is gated by `instance:kill` and
`DELETE /v1/instances/{id}` by `instance:terminate`. An agent that has read the
REST surface and calls `instance_terminate` on a running instance gets a 409
about deletion rather than the termination it asked for.

## Experiment record (experiment:assumption-mcp-tools-cover-every-route)

# Does the MCP catalog cover the REST surface?

## What it ran against

One `rimsky-all-in-one` container from the tree's own image tag. It opens an MCP
session, lists the catalog, calls every advertised tool once, drives 61 HTTP
routes covering every route family, and then reads the deployment's own audit log
— which records a permission action and a `protocol_skin` for each request — to
compare which actions each skin reached.

## What was observed

The catalog advertises 57 tools. Calling all 57 reached 43 distinct permission
actions; driving the 61 routes reached 44. The only action HTTP reaches that MCP
does not is `mcp:read`, the MCP transport's own gate — self-referential. MCP
reaches nothing HTTP cannot. So the gated operation set really is covered, and
`observability_get` covers the whole `/v1/observability/*` family through one
tool taking a `path_suffix`.

Two ungated routes have no tool. `GET /v1/health` and `GET /v1/auth/whoami` both
answer 200 over HTTP and neither is reachable from the catalog: no tool is named
for health, whoami or the CA root, and neither `health:probe` nor `auth:whoami`
appears in the MCP action set. The nearest tool,
`observability_get("system/health")`, reads a different resource behind a
different permission.

The two tools that cover instance teardown are named the other way round from
their routes. `instance_terminate` is described as "Delete an already-terminal
instance and its rows; refused with 409 while the instance is still running", and
`instance_kill` as "Force a running instance terminal". The audit log confirms
the inversion at the route level: `POST /v1/instances/{id}/terminate` is gated by
`instance:kill`, and `DELETE /v1/instances/{id}` is gated by
`instance:terminate`.

EXPERIMENT PASS (16 checks)

Runnables: `src:.ok-planner/experiments/assumption-mcp-tools-cover-every-route/` at the stamped commit.
