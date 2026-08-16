---
assumption: mcp-tools-cover-every-route
commit: PENDING
disposition: trap
synthesized: 2026-08-16T05:48:16Z
---

# the MCP tool catalog covers every operation the REST surface exposes, so nothing requires dropping to HTTP.

As agent driving rimsky over MCP, I would take it that the MCP tool catalog covers every operation the REST surface exposes, so nothing requires dropping to HTTP.

## Source

published-concept — `concept:control-api` ("multiple protocol skins on the same TCP port and the same operation set", catalog "computed from the canonical action registry")

## What a run would observe

map the 57 MCP tools against the 92 routes via the permission-action list and name operations with a route but no tool.

## Measured

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
