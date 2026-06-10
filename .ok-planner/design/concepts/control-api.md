---
concept: control-api
status: as-is
aliases: []
---

# Control API

## What it is

The operator interface exposed by the control-api binary. Serves two protocol skins on the same TCP port and the same operation set:

- **HTTP+JSON** — routed at bare, unversioned paths covering template registration, instance lifecycle (create, pause, resume), per-instance breakpoint management (set, delete, resume), the auth surface, observability reads, and admin diagnostics endpoints.
- **MCP** (Model Context Protocol) — JSON-RPC 2.0 over HTTP at a dedicated MCP endpoint, served by an in-process MCP package. The MCP surface exposes tools plus read-only resource list and read. Resource-subscribe and server-pushed notifications are not part of this concept. The tool catalog is computed from the canonical action registry; the tool-list call filters by the requesting key's permission grant.

Both skins pass through the same auth + permission middleware. Fires lifecycle-subscriber events at state transitions (synchronously; see `concept:lifecycle-subscriber`).

## Purpose

The operator, the `rimsky` CLI thin client, and agentic clients (desktop AI assistants, custom MCP clients) all speak to this surface. HTTP+JSON is easier to script, expose through ingress, and inspect with curl during incidents than gRPC. MCP is the operator-facing surface for LLM-based agents that can self-discover the catalog and dispatch tool calls.

## Boundaries

Owns: the route mounts, the per-route handlers, the lifecycle-subscriber fan-out, observability handlers (each in a short transaction), the auth middleware + endpoint surface, the MCP envelope handler + catalog. Does NOT own: dispatch (supervisor's job), scheduling (scheduler's job), service protocols (those are gRPC). Adjacent: `rimsky` (CLI), `lifecycle-subscriber`, `observability`, `cascade-graph`, `instance`, `template`, `api-key`, `permission`.

## Invariants

- Bare paths only; v1 does not version the wire format. Rolling upgrades are operator-managed.
- Lifecycle events fire from control-api (not the supervisor) synchronously at state transitions. A slow subscriber holds up the response.
- The compose tag/instance-key prefix is server-enforced: tag-create and instance-create reject the reserved prefix from non-compose origins.
- **Every route is auth-gated** except the health and readiness probes (infrastructure paths predating control-plane semantics). The action registry is the canonical route → action mapping; an unmapped route is a wiring bug.
- **MCP shares the auth gate.** Tool invocations re-enter the routing pipeline via the catalog's invoke path, so the same action-gating middleware runs. The audit row records the MCP protocol skin.

## MCP-as-skin

The MCP protocol skin is hosted in-process by a package under the control-api implementation. Tool invocations dispatch back into the router via an in-process handler (no self-loopback HTTP call). The tool-catalog scaffolding and JSON-RPC envelope handling live in the in-process MCP package.

Note: an LLM-agent executor embeds a separate per-run *internal* MCP server — same protocol, different role (per-dispatch executor-local tools vs operator control-plane). Do not confuse the two.
