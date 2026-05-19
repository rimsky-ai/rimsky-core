---
concept: control-api
status: as-is
aliases: []
references:
  - _discover/2026-05-10-rimsky-cli-thin-client.md
  - _discover/control-api-mcp-server.md
  - _discover/observability-cascade-graph-endpoint.md
  - _discover/2026-05-10-lifecycle-subscriber-opt-in.md
---

# Control API

## What it is

The operator interface exposed by `cmd/rimsky-control-api` (binary). Implementation lives under `control/controlapi/`. Serves two protocol skins on the same TCP port and the same operation set:

- **HTTP+JSON** — `go-chi/chi`-routed at bare paths (no `/v1/` prefix): `/templates`, `/instances`, `/auth/*`, `/observability/*`, `/admin/diagnostics/*`, `/admin/scheduled-nodes/{id}/force-fire`, etc.
- **MCP** (Model Context Protocol) — JSON-RPC 2.0 over HTTP at `POST /mcp`, served by `control/controlapi/mcp/`. Tools-only V1 (no resources, prompts, subscriptions). The tool catalog is computed from the canonical action registry at `code:control/controlapi/actions.go::v1Actions`; `tools/list` filters by the requesting key's permission grant.

Both skins pass through the same auth + permission middleware. Fires `LifecycleSubscriber` events at state transitions (synchronously).

## Purpose

The operator, the `rimsky` CLI thin client, and agentic clients (Claude Desktop, custom MCP clients) all speak to this surface. HTTP+JSON is easier to script, expose through ingress, and inspect with curl during incidents than gRPC. MCP is the operator-facing surface for LLM-based agents that can self-discover the catalog and dispatch tool calls.

## Boundaries

Owns: the chi route mounts, the per-route handlers, the lifecycle-subscriber fan-out, observability handlers (`inTx`-wrapped), the auth middleware + endpoint surface, the MCP envelope handler + catalog. Does NOT own: dispatch (supervisor's job), scheduling (scheduler's job), service protocols (those are gRPC). Adjacent: `rimsky` (CLI), `lifecycle-subscriber`, `observability`, `cascade-graph`, `instance`, `template`, `api-key`, `permission`.

## Invariants

- Bare paths only; v1 does not version the wire format. Rolling upgrades are operator-managed.
- Lifecycle events fire from control-api (not the supervisor) synchronously at state transitions. A slow subscriber holds up the response.
- The `compose:<project>:<...>` tag/instance-key prefix is reserved for `rimsky compose` but enforcement is client-side only; the server accepts any string.
- **Every route is auth-gated** except `/health` and `/ready` (infrastructure paths predating control-plane semantics). The action registry at `code:control/controlapi/actions.go::v1Actions` is the canonical route → action mapping; an unmapped route is a wiring bug.
- **MCP shares the auth gate.** Tool invocations re-enter the chi pipeline via `code:control/controlapi/mcp/catalog.go::Catalog.Invoke`, so the same `gateByAction` runs. The audit row records `protocol_skin: "mcp"`.

## MCP-as-skin

The MCP protocol skin is hosted in-process by `control/controlapi/mcp/` (a package under `control/controlapi/`). Tool invocations dispatch back into the chi router via an in-process `http.Handler` (no self-loopback HTTP call). The pre-spec standalone Go module under `mcp-servers/control-api/` has been retired; its tool-catalog scaffolding and JSON-RPC envelope handling folded into the in-process package.

Note: `executors/claude-agent/` embeds a separate per-run *internal* MCP server (`internal-mcp-server.ts`) — same protocol, different role (per-dispatch executor-local tools vs operator control-plane). Do not confuse the two.

(Previously documented as a standalone concept `mcp-server`; folded here under `2026-05-11-design-log-convergence`. The standalone `mcp-servers/control-api/` framing retired by `2026-05-15-control-plane-mcp-and-auth-design.md`.)

## Aliases and historical names

None live.

## Open within this concept

- `compose:` prefix reservation is client-side only — see `tensions/compose-prefix-client-side.md`.
- Bare-paths-no-/v1/ vs eventual v1 commitment is acknowledged but unresolved — see `tensions/control-api-version-prefix.md`.

## Notes

- [2026-05-15] Spec `.ok-planner/specs/2026-05-15-control-plane-mcp-and-auth-design.md` adds the auth surface, makes MCP a first-class protocol skin hosted in-process at `POST /mcp`, and retires the standalone `mcp-servers/control-api/` framing.

