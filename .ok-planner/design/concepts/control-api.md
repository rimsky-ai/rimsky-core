---
concept: control-api
status: as-is
aliases: []
---

# Control API

## What it is

The operator interface exposed by the control-api binary. Serves two protocol skins on the same TCP port and the same operation set:

- **HTTP+JSON** — routed at bare, unversioned paths covering template registration, instance lifecycle (create, pause, resume), per-instance breakpoint management (set, delete, resume), the auth surface, observability reads, and admin diagnostics and scheduled-node force-fire endpoints.
- **MCP** (Model Context Protocol) — JSON-RPC 2.0 over HTTP at a dedicated MCP endpoint, served by an in-process MCP package. Tools-only V1, plus read-only resource list and read added by `spec:2026-05-24-instance-debugger-design`. No resource-subscribe and no server-pushed notifications in V1 — those await a transport upgrade. The tool catalog is computed from the canonical action registry; the tool-list call filters by the requesting key's permission grant.

Both skins pass through the same auth + permission middleware. Fires lifecycle-subscriber events at state transitions (synchronously; see `concept:lifecycle-subscriber`).

## Purpose

The operator, the `rimsky` CLI thin client, and agentic clients (desktop AI assistants, custom MCP clients) all speak to this surface. HTTP+JSON is easier to script, expose through ingress, and inspect with curl during incidents than gRPC. MCP is the operator-facing surface for LLM-based agents that can self-discover the catalog and dispatch tool calls.

## Boundaries

Owns: the route mounts, the per-route handlers, the lifecycle-subscriber fan-out, observability handlers (each in a short transaction), the auth middleware + endpoint surface, the MCP envelope handler + catalog. Does NOT own: dispatch (supervisor's job), scheduling (scheduler's job), service protocols (those are gRPC). Adjacent: `rimsky` (CLI), `lifecycle-subscriber`, `observability`, `cascade-graph`, `instance`, `template`, `api-key`, `permission`.

## Invariants

- Bare paths only; v1 does not version the wire format. Rolling upgrades are operator-managed.
- Lifecycle events fire from control-api (not the supervisor) synchronously at state transitions. A slow subscriber holds up the response.
- The compose tag/instance-key prefix is reserved for the compose command but enforcement is client-side only; the server accepts any string.
- **Every route is auth-gated** except the health and readiness probes (infrastructure paths predating control-plane semantics). The action registry is the canonical route → action mapping; an unmapped route is a wiring bug.
- **MCP shares the auth gate.** Tool invocations re-enter the routing pipeline via the catalog's invoke path, so the same action-gating middleware runs. The audit row records the MCP protocol skin.

## MCP-as-skin

The MCP protocol skin is hosted in-process by a package under the control-api implementation. Tool invocations dispatch back into the router via an in-process handler (no self-loopback HTTP call). The pre-spec standalone MCP-server Go module has been retired; its tool-catalog scaffolding and JSON-RPC envelope handling folded into the in-process package.

Note: an LLM-agent executor embeds a separate per-run *internal* MCP server — same protocol, different role (per-dispatch executor-local tools vs operator control-plane). Do not confuse the two.

(Previously documented as a standalone concept `mcp-server`; folded here under `spec:2026-05-11-design-log-convergence`. The standalone MCP-server framing retired by `spec:2026-05-15-control-plane-mcp-and-auth-design`.)

## Aliases and historical names

None live.

## Open within this concept

- The compose prefix reservation is client-side only — see `tension:compose-prefix-client-side`.
- Bare-paths vs eventual v1 versioning commitment is acknowledged but unresolved — see `tension:control-api-version-prefix`.

## Notes

- [2026-05-15] `spec:2026-05-15-control-plane-mcp-and-auth-design` adds the auth surface, makes MCP a first-class protocol skin hosted in-process at a dedicated MCP endpoint, and retires the standalone MCP-server framing.
- 2026-05-24 — MCP capability extends from tools-only to tools + read-only resources per `spec:2026-05-24-instance-debugger-design`. Resource list and read added to the dispatch switch; push (resource-subscribe + resource-update notifications) deferred to a future transport-upgrade spec. New per-instance pause and resume routes added. New per-instance breakpoint routes added.
- 2026-05-25 — Codebase citations removed + cross-refs repaired for self-containment per spec:2026-05-25-concept-doc-self-containment.

