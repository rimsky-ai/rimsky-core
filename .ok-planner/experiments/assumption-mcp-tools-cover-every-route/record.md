---
experiment: assumption-mcp-tools-cover-every-route
commit: d977250c
---

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
