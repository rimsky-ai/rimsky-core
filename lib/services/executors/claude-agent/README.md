# rimsky-executor-claude-agent

Go implementation of a rimsky v1 Executor that runs agentic cells by spawning
the Claude Code CLI as a subprocess, giving it an internal MCP callback URL,
and relaying the subprocess's structured outcome back to the rimsky
supervisor.

## Design

Speaks rimsky's Executor protocol (`lib/protocols/proto/v1/executor.proto`)
via the generated Go stubs. Always uses the async-handoff pattern:

1. `Execute(ExecuteRequest)` is received over gRPC (or the HTTP-JSON bridge's
   `POST /execute`).
2. The executor replies immediately with `AwaitAsyncCallback{async_ack_id}`.
3. The agent runs in the background; the final outcome is POSTed to
   `callback_url` (HTTP+JSON, supervisor-supplied) — the gRPC path posts to
   `${callback_url}/v1/callback/{async_ack_id}`, the bridge path posts the
   body with `async_ack_id` inline.

The agent runtime:

- Starts a per-dispatch loopback HTTP/JSON-RPC callback endpoint (the
  "internal MCP" server) the Claude CLI subprocess invokes via its MCP-HTTP
  transport (`report_complete`, `report_blocked`, `report_error`,
  `report_park`, `dispatch_context_read`, `attributes_read`).
- Spawns the `claude` binary with the callback URL and a per-run token.
- Watches the CLI's `stream-json` output for progress signals and tears the
  subprocess down on two configurable deadlines (`cli.silence_timeout_ms`,
  `cli.tool_use_timeout_ms`), both disabled by default.
- Validates `cli.required_signoffs` (ed25519 over RFC 8785-canonicalized
  bound output) before committing a completion.

## Per-node configuration (`cli.*` attributes)

- `cli.mcp_servers` — inline MCP server declarations, one of three
  transports: `{transport: "http", name, url, headers?, allowed_tools?}`,
  `{transport: "stdio", name, command, args?, env?, allowed_tools?}`,
  `{transport: "module", name, module, allowed_tools?}` (`http-loopback` is
  accepted as an alias for `module`).
- `cli.expose_env` — env-var names to expose to that node's CLI child from
  the executor process's own environment.

## Operator configuration (env)

- `RIMSKY_CLAUDE_AGENT_MCP_ALLOWLIST` — comma-separated MCP server names any
  template may declare. Unset = open; set = exact boundary; a node declaring
  a server outside it fails the dispatch with an error naming the server,
  instance, and node.
- `RIMSKY_CLAUDE_AGENT_EXPOSE_ENV_ALLOWLIST` — comma-separated env-var names
  any template may expose. Same open/closed semantics.
- `ANTHROPIC_API_KEY` / `CLAUDE_CODE_OAUTH_TOKEN` — CLI auth (one required
  outside stub mode).
- `RIMSKY_EXECUTOR_HOST`, `RIMSKY_EXECUTOR_PORT_GRPC`,
  `RIMSKY_EXECUTOR_PORT_HTTP`, `RIMSKY_EXECUTOR_CALLBACK_HOST`,
  `RIMSKY_EXECUTOR_SILENCE_MS`, `RIMSKY_EXECUTOR_TOOL_USE_TIMEOUT_MS`,
  `RIMSKY_EXECUTOR_CLAUDE_BINARY`, `RIMSKY_EXECUTOR_DECLARED_TAGS`,
  `RIMSKY_EXECUTOR_STUB_MODE` — transport/runtime knobs, unchanged from the
  generic executor surface.

## Layout

- Handler package: `package claudeagent` (this directory) — importable by
  both the standalone binary and the all-in-one in-process registration.
- Standalone binary: `cmd/main.go` (gRPC + HTTP bridge + observability).
- Image: `Dockerfile` (Go binary + native Claude Code CLI, no Node runtime).
