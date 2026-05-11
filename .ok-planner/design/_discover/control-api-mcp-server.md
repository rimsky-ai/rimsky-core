---
topic: control-api-mcp-server
kind: choice
---

# `mcp-servers/control-api/` exposes rimsky's HTTP control-api as MCP tools for agent consumption

## Description

Rimsky's primary operator-facing surface is the HTTP+JSON control-api (`2026-05-10-rimsky-cli-thin-client`). For AI-agent consumers (Claude, etc.), the same surface is wrapped as an MCP (Model Context Protocol) server at `mcp-servers/control-api/`. The MCP server exposes rimsky's operations as named tools an agent can call.

This is a separate binary from the runtime processes; it's not a peer protocol implementation, it's a control-plane façade. An agent that wants to register a template, create an instance, invalidate a node, or inspect a parked frame can do so via the MCP tool surface without speaking HTTP directly.

**Implementation: Go, not TypeScript.** Package `controlapimcp` is a third Go module (`github.com/fallguy/rimsky/mcp-servers/control-api`, own `go.mod`) implementing JSON-RPC 2.0 over HTTP on a single `POST /mcp` endpoint using `go-chi/chi` and stdlib `encoding/json`. The package doc on `server.go` explicitly motivates the choice: "Per plan K2: implements `initialize`, `tools/list`, `tools/call` over POST /mcp using stdlib encoding/json + go-chi/chi. No third-party MCP SDK; the wire surface is small enough that a direct implementation is cleaner."

Dispatch lives in `Server.handleMCP` (`server.go:105`), which switches on `rpc.Method` across three RPC methods: `initialize`, `tools/list`, `tools/call`. Unknown methods return JSON-RPC error code `-32601`. The tool catalog is registered at `NewServer` time by `registerCoreTools` (`tools.go:27`); each tool is a closure that calls `callJSON` (`tools.go:236`) — a minimal HTTP round-trip helper that forwards an optional `Authorization: Bearer <CONTROL_API_TOKEN>` header to the underlying control-api.

The current catalog (`tools.go`) covers templates (`template_list`, `template_get`, `template_register`, `template_deploy`, `template_undeploy`, `template_deregister`), tags (`tag_list`, `tag_set`, `tag_delete`), instances (`instance_list`, `instance_get`, `instance_create` — supports `userdata_overrides`, `instance_terminate`), nodes (`node_get`, `node_invalidate`, `force_fire_scheduled`), and diagnostics (`held_frames_list`, `parked_nodes_list`). Each tool's `InputSchema` is minimal JSON Schema; the call wraps the response as MCP tool-result format (`{ content: [{ type: "text", text: <json> }] }`).

Auth is bounded: the shim does not enforce its own auth; it forwards `CONTROL_API_TOKEN` (env name `EnvControlAPIToken`, `config.go:22`) to the control-api and relies on operators isolating the shim port. The binary entrypoint at `cmd/rimsky-mcp-control-api/main.go` reads `CONTROL_API_URL`, `CONTROL_API_TOKEN`, `BIND_ADDR`, `PORT` from env (defaults `http://127.0.0.1:8080`, empty, `0.0.0.0`, `8081`).

The platform-extensions design (`.ok-planner/specs/2026-05-08-platform-extensions-for-agent-consumers-design.md`) added several features specifically for agent consumers: named events, per-instance userdata overrides, parked nodes with session-resume, blob spill backends. The MCP server makes those features accessible to an agent without it having to learn rimsky's HTTP API.

Operationally, the MCP server is opt-in. An operator who doesn't use AI-agent consumption doesn't run it.

## Code surface

- `mcp-servers/control-api/server.go` — package doc, JSON-RPC envelope types, `Server`, `NewServer`, `Routes`, `handleMCP`, `handleInitialize`, `handleToolsList`, `handleToolsCall`, `RegisterTool`.
- `mcp-servers/control-api/tools.go` — `registerCoreTools` (the full tool catalog) and `callJSON` (HTTP round-trip helper, bearer-token forwarding).
- `mcp-servers/control-api/config.go` — env-var name constants (`CONTROL_API_URL`, `CONTROL_API_TOKEN`, `BIND_ADDR`, `PORT`).
- `mcp-servers/control-api/server_test.go` — wire-shape tests.
- `mcp-servers/control-api/cmd/rimsky-mcp-control-api/main.go` — binary entrypoint.
- `mcp-servers/control-api/go.mod` — own module; depends only on `github.com/go-chi/chi/v5`.

## Prose surface

- `.ok-planner/specs/2026-05-08-platform-extensions-for-agent-consumers-design.md` — design that motivated the MCP server.
- Package doc on `mcp-servers/control-api/server.go` — explicit rationale for the no-SDK direct implementation.

## Adjacent topics

- `2026-05-10-typescript-executor-claude-agent` — sibling TS binary that embeds an MCP server inside the executor (a different MCP role: per-run executor-local tools, not the operator surface).
- `2026-05-10-rimsky-cli-thin-client` — operator interface in HTTP+JSON form.
- `2026-05-10-unified-rimsky-yml-config` — broader config landscape (the MCP shim itself reads env vars, not `rimsky.yml`).

## Observations

- The shim is its own Go module (third in the workspace, alongside `foundation/` and `protocols/`) — keeping it out of the runtime modules so embedders can pick it up independently without dragging modeling-layer dependencies.
- The "no third-party MCP SDK" choice keeps the dependency surface tiny (stdlib + `go-chi/chi` only). The tradeoff: the shim has to be updated by hand if the MCP wire spec evolves.
- The shim is a strict pass-through: no validation, no caching, no synthesis. Every tool call → one HTTP round-trip; the control-api is the source of truth for both behavior and errors.
- The catalog is currently hand-curated in `tools.go`; if the control-api grows new endpoints, the catalog has to be manually extended. A future audit could decide whether to generate tools from an OpenAPI spec or keep hand-curation for tighter agent-facing descriptions.
- Naming: `mcp-servers/` (plural) suggests room for more MCP servers (per-feature surfaces). Currently only `control-api` is present.
