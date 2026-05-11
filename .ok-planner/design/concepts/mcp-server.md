---
concept: mcp-server
status: as-is
aliases:
  - control-api MCP shim
references:
  - _discover/control-api-mcp-server.md
  - _discover/2026-05-10-typescript-executor-claude-agent.md
---

# MCP server (control-api shim)

## What it is

`mcp-servers/control-api/` is a standalone Go module that wraps rimsky's HTTP control-api as MCP (Model Context Protocol) tools. Implements `initialize`, `tools/list`, `tools/call` over `POST /mcp` using stdlib `encoding/json` + `go-chi/chi`. No third-party MCP SDK. Catalog covers templates, tags, instances, nodes, diagnostics. Forwards `Authorization: Bearer <CONTROL_API_TOKEN>` to the underlying control-api.

## Purpose

AI-agent consumers (Claude etc.) speak MCP, not raw HTTP. The shim translates rimsky's operator surface into tools an agent can call.

## Boundaries

Owns: the JSON-RPC envelope, the tool catalog, the bearer-token forwarding. Does NOT own: control-api business logic (every call is one HTTP round-trip), agent-side reasoning. Adjacent: `control-api`, `rimsky-cli`, `executor` (claude-agent embeds a different MCP server for per-run tools — same protocol, different role).

## Invariants

- Own Go module (no runtime dependency on modeling/foundation).
- Strict pass-through: no validation, no caching, no synthesis.
- Catalog is hand-curated in `tools.go`; not auto-generated.

## Aliases and historical names

None. Note that `executors/claude-agent/` embeds a per-run *internal* MCP server (`internal-mcp-server.ts`) that is a different surface from this one (per-dispatch executor-local tools vs operator control-plane).

## Open within this concept

- Two MCP server roles coexist (per-run executor-local in claude-agent; operator control-plane in mcp-servers/control-api) — distinct, not in conflict. No tension noted.

