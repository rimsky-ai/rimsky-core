---
experiment: assumption-cli-thin-client-route-parity
commit: PENDING
---

# Route parity between the CLI and the control API

## What it ran against

A recording reverse proxy standing in front of one `rimsky-all-in-one`
container from this tree's image set, with the CLI pointed at the proxy. Every
request the CLI makes is recorded and folded onto the route template it
matched, so a route counts as reached only when some verb was observed asking
for it — nothing is inferred from source.

The run drives 55 CLI verbs against a seeded world (template, two instances, a
node, tags, api-keys, a compose manifest), including the streaming verbs
`logs`, `watch`, and `messages tail`, which are cut off once they have shown
which route they poll. The declared population is the 68 control-API routes
from this run's public surface — the 92 listed minus the six served by the
supervisor callback listener and the sensor-webhook ingress, and counting the
19 observability routes as the one `GET /v1/observability/*` prefix the router
mounts. Names an operator would reach for that turn out not to be verbs are
probed separately.

## What was observed

36 of the 68 routes were reached. 32 were reached by nothing:

The debugger surface is entirely absent — breakpoints list, create, delete,
resume, and `POST /v1/instances/{id}/debug/override` have no verb, and
`rimsky breakpoint` is not a command. So are pause and resume: `POST
/v1/instances/{idOrKey}/pause` and `/resume` exist, and `rimsky instance
pause` answers `unknown subcommand "pause"`. So is the whole lineage query
surface — six `by-producer` / `by-source` / claim / run routes, where the CLI
offers only `lineage prune`, the one write. So is `GET /v1/audit` (`rimsky
audit` is not a command), `GET /v1/auth/whoami` (`rimsky whoami` is not a
command), `GET /v1/runs/{run_id}`, the three admin diagnostics beyond parked
nodes, `POST /v1/instances/{id}/messages` (the CLI can tail and show messages
but not send one), the three MCP transport routes, `POST /v1/enroll`, `GET
/v1/ca-root`, and every observability route.

Two of the 32 are reachable only in a world this run did not build: `GET
/v1/instances/{id}/frames` is named by `watch` on its idle-check path, which
this run's instance never entered, and `GET
/v1/instances/{id}/assets/{alias}/materialization-history` has a client method
in the CLI (`GetAssetMaterializationHistory`) that no verb calls.

The other direction holds better than the prior's phrasing suggests but still
fails it: 8 verbs reach no route at all — `ctx list|add|use|current|rm` (local
config file), `agent status|stop` (a local daemon), and `version`. Two more,
`template lint` and `compose plan`, do dial, so the CLI is a pass-through
wherever it talks at all. 2 checks, 0 pass, 2 fail.
