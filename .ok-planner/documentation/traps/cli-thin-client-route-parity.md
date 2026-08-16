---
trap: cli-thin-client-route-parity
release: d977250c
demonstration: experiment:assumption-cli-thin-client-route-parity
---
## Assumption

As operator choosing between CLI and HTTP, I would take it that because the CLI is a thin pass-through client, every CLI verb corresponds to a control-API route and every route is reachable from some CLI verb.

published-concept — `concept:rimsky` ("a thin HTTP+JSON client over the control-api", "no client-side business logic")

## Actual behavior

the experiment — built for
this run — stood a recording reverse proxy in front of a live
`rimsky-all-in-one` from this tree's image set, pointed the CLI at it, and
drove 55 verbs against a seeded world. A route counts as reached only where a
verb was observed asking for it.

36 of the 68 declared control-API routes were reached; 32 were reached by
nothing. Whole surfaces are missing from the CLI: the debugger (breakpoints
list / create / delete / resume and `POST /v1/instances/{id}/debug/override` —
`rimsky breakpoint` is not a command), pause and resume (`rimsky instance
pause` answers `unknown subcommand "pause"` while `POST
/v1/instances/{idOrKey}/pause` is live), the six lineage query routes (the CLI
ships only `lineage prune`, the one write), every observability route, the
three MCP transport routes, the three admin diagnostics beyond parked nodes,
`GET /v1/audit`, `GET /v1/auth/whoami`, `GET /v1/runs/{run_id}`, `POST
/v1/enroll`, `GET /v1/ca-root`, and `POST /v1/instances/{id}/messages` — the
CLI can tail and show messages but cannot send one. Two of the 32 are near
misses rather than absences: `GET /v1/instances/{id}/frames` is named by
`watch` on an idle-check path this run's instance never entered, and the asset
materialization-history route has a client method in the CLI that no verb
calls.

The other direction fails more mildly. 8 verbs reach no route at all — the
five `ctx` verbs (a local config file), `agent status` and `agent stop` (a
local daemon), and `version`. Everything else that dials is a pass-through,
including `template lint`, which posts to `/v1/templates/validate` rather than
validating client-side, so the "no client-side business logic" half of the
prior is what actually holds. The parity half does not: an operator choosing
between the CLI and HTTP is choosing between roughly half the platform and all
of it. 2 checks, 0 pass, 2 fail.
