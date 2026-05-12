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

The HTTP+JSON operator interface exposed by `cmd/rimsky-control-api` (binary). Implementation lives under `modeling/controlapi/`. Mounted by `go-chi/chi` at bare paths (no `/v1/` prefix): `/templates`, `/instances`, `/observability/*`, `/admin/diagnostics/*`, `/admin/scheduled-nodes/{id}/force-fire`, etc. Fires `LifecycleSubscriber` events at state transitions (synchronously).

## Purpose

The operator and the `rimsky-cli` thin client both speak to this surface. HTTP+JSON is easier to script, expose through ingress, and inspect with curl during incidents than gRPC.

## Boundaries

Owns: the chi route mounts, the per-route handlers, the lifecycle-subscriber fan-out, observability handlers (`inTx`-wrapped). Does NOT own: dispatch (supervisor's job), scheduling (scheduler's job), peer protocols (those are gRPC). Adjacent: `rimsky-cli`, `lifecycle-subscriber`, `observability`, `cascade-graph`, `instance`, `template`.

## Invariants

- Bare paths only; v1 does not version the wire format. Rolling upgrades are operator-managed.
- Lifecycle events fire from control-api (not the supervisor) synchronously at state transitions. A slow subscriber holds up the response.
- The `compose:<project>:<...>` tag/instance-key prefix is reserved for `rimsky-cli compose` but enforcement is client-side only; the server accepts any string.

## Agentic MCP shim

The standalone Go module under `mcp-servers/control-api/` wraps the HTTP control-api surface as MCP (Model Context Protocol) tools. Implements `initialize` / `tools/list` / `tools/call` over `POST /mcp` using stdlib `encoding/json` + `go-chi/chi` (no third-party MCP SDK). Catalog covers templates, tags, instances, nodes, diagnostics. Strict pass-through: no validation, no caching, no synthesis. Forwards `Authorization: Bearer <CONTROL_API_TOKEN>` to the underlying control-api. Independent Go module with no runtime dependency on modeling/foundation; catalog is hand-curated in `tools.go`.

Note: `executors/claude-agent/` embeds a separate per-run *internal* MCP server (`internal-mcp-server.ts`) — same protocol, different role (per-dispatch executor-local tools vs operator control-plane). The dual-MCP-role observation is part of this subsection; do not confuse the two.

(Previously documented as a standalone concept `mcp-server`; folded here under `2026-05-11-design-log-convergence`.)

## Aliases and historical names

None live.

## Open within this concept

- `compose:` prefix reservation is client-side only — see `tensions/compose-prefix-client-side.md`.
- Bare-paths-no-/v1/ vs eventual v1 commitment is acknowledged but unresolved — see `tensions/control-api-version-prefix.md`.

