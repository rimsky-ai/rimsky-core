---
topic: rimsky-cli-thin-client
kind: boundary
---

# `rimsky-cli` is a thin HTTP+JSON client over the control-api; v1 does not version the wire format

## Description

A CLI surface for an orchestration platform commonly bundles either heavy logic (parsing manifests, validating before sending) or thin pass-through (POST/GET wrapping). The choice constrains both the operator's mental model and the upgrade story. Rimsky chose thin pass-through.

`cmd/rimsky-cli/main.go` is small; `modeling/cli/` carries the request-building helpers; `modeling/controlapi/` is the server side. There is no proto definition for the control-api — it's HTTP+JSON with routes and shapes defined by the chi router. Every CLI operation is one or more HTTP calls; the CLI does not embed control-api business logic.

URLs are **bare-pathed** (no `/v1/` prefix). The chi routes are mounted at `/templates`, `/instances`, `/observability/*`, `/admin/diagnostics/*`, etc. — no version segment. Pinning a wire format would be a v1 commitment that the routing layer has deliberately not made; CLAUDE.md "Non-obvious gotchas" calls this out: "rimsky-cli is a thin client; v1 does not version the control-api. Bare paths (no /v1/ prefix); rolling upgrades are operator-managed."

The CLI's `compose` subcommand is the only convention-bearing surface. CLAUDE.md: "Compose owns project-prefixed names. Tags `compose:<project>:<...>` and instance keys `compose:<project>:<...>` are reserved for `rimsky-cli compose`. The CLI rejects manual registration with this prefix client-side."

The prefix reservation is **client-side**. A future enforcement at the API would be an additive change: today the server accepts any tag string, and the CLI's restriction is local. A motivated operator could bypass the CLI and POST a `compose:` tag directly to the API; the server would accept it. This is the same pattern as the rest of the CLI: validation is convenience, not authority.

Alternative considered: gRPC for control-api. Not chosen — control-api is the operator interface, where HTTP+JSON is easier to script, easier to expose through ingress / API gateways, and easier to script with curl during incident response. The peer protocols (executor, claim-producer, lifecycle-subscriber) are gRPC because they're peer-to-peer; the operator interface intentionally chose the more accessible transport.

Alternative considered: heavy CLI with embedded validation. Not chosen — every validation has to be replicated server-side anyway (a client cannot trust client validation); embedding it in the CLI doubles maintenance.

CLAUDE.md "Non-obvious gotchas" describes a real consequence: "A CLI shipped from an older rimsky release may issue requests an older control-api rejects with 404; operators are expected to keep them on close versions." There is no client-side version negotiation; the CLI assumes the routes it knows are present.

## Code surface

- `cmd/rimsky-cli/main.go` — thin entry.
- `modeling/cli/` — request builders (URLs + JSON bodies).
- `modeling/controlapi/` — chi-routed server side.
- `modeling/controlapi/router.go` (or equivalent) — mount points without version prefix.
- `Dockerfile.cli` — release container.

## Prose surface

- `CLAUDE.md` "Non-obvious gotchas" — thin client, bare paths, compose prefix.
- `docs/concepts/template.md`, `docs/concepts/instance.md` — CLI verbs cited alongside HTTP routes.
- `docs/humans/dashboard.md` — operator-facing CLI usage.
- `quickstart/` — examples.

## Adjacent topics

- `2026-05-10-lifecycle-subscriber-opt-in` — peer protocols use gRPC; CLI uses HTTP+JSON deliberately.
- `2026-05-10-content-addressed-templates` — `compose:` tag prefix interacts with template tagging.
- `rimsky-cli-compose-prefix-reservation` — operator-facing detail (could be a separate entry).

## Observations

- "Bare paths" mean `POST /templates` not `POST /v1/templates`. This is a v1-commitment-deferral: a future v1 release can introduce `/v1/...` and a CLI that knows about both can route accordingly. Today's CLI cannot do that.
- The CLI rewrite-in-another-language story is intact because there's no proto: anyone who can POST JSON to chi routes can implement the operator interface. The `mcp-servers/control-api/` directory carries a separate MCP server that wraps the same HTTP routes for AI-agent consumption.
- The `compose:` prefix reservation lives only in the CLI source. If a future tool that's not `rimsky-cli` wants the same convention, it must replicate the reservation. There is no server-side helper that exposes "the canonical compose-prefix matcher."
- `Dockerfile.cli` packages just the CLI binary for distribution; the operator uses the same image across different rimsky deployments by adjusting the `RIMSKY_CONTROL_API` env var.
