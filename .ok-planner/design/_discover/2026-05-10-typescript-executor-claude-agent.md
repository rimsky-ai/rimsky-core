---
topic: typescript-executor-claude-agent
kind: choice
---

# Reference Claude-running executor lives in TypeScript (tsc-built, Vitest-tested, gRPC + Fastify HTTP bridge + embedded MCP)

## Description

The bundled executors include `http-node` (Go), `stub` (Go), and `claude-agent` (TypeScript). The TypeScript binary is the only language boundary in the bundled set and exercises the cross-language portability of the protocol surface.

`executors/claude-agent/` is a standalone Node.js project with its own `package.json`, `tsconfig.json`, and `dist/`. The architecture spans several modules under `executors/claude-agent/src/`:

- **`server.ts`** — gRPC `NodeExecutor.Execute` impl. Always responds with `Heartbeat + AsyncAccepted`, closes the stream, and runs the agent in the background. The final outcome is POSTed to the supervisor's callback URL — `${callback_url}/v1/callback/{async_ack_id}` per the supervisor's chi route. Body keyed `type` (not `kind`) — enforced; the test suite at `server.test.ts` exercises the exact wire shape.
- **`agent-run.ts`** — drives the Claude CLI via `cli-runner.ts`. The actual agent run; orchestrates token-budgeting, observability events, and the terminal outcome.
- **`cli-runner.ts` + `cli-env.ts`** — `claude` CLI process invocation. `cliAuth` carries the OAuth refresh / API-key config. Stream-JSON output.
- **`internal-mcp-server.ts` + `internal-mcp-tools.ts`** — per-run MCP server exposing rimsky-side tools (attribute writeback, named-event emit, park) to the agent. Embedded via `@modelcontextprotocol/sdk`.
- **`http-bridge.ts`** — Fastify HTTP+JSON bridge for observability and trace-fetching by the dashboard.
- **`attributes-tools.ts`** — the writeback-attributes MCP tool implementation.
- **`observability.ts`** — local trace ledger; supports `GetTrace` + `StreamTrace` via the `ExecutorObservability` protocol.
- **`userdata-schema.ts`** — declared events + JSON schema reported back to rimsky via observability `Capabilities()`.

Protocol bindings are loaded **at runtime** from `protocols/proto/v1/*.proto` via `@grpc/proto-loader` (`proto-loader.ts`). This avoids checking in TypeScript-generated proto bindings and keeps the executor binary-compat with rimsky's protocol module by reading the same `.proto` files.

Build: `cd executors/claude-agent && npm install && npm test && npm run build`. Test framework: vitest (`*.test.ts` co-located with sources). Build target: `dist/main.js` packaged as a Node binary; `deploy/Dockerfile.claude-agent` carries a Node runtime.

The MCP dependency is the load-bearing pin to TypeScript: `@modelcontextprotocol/sdk` is Anthropic's MCP SDK, best-supported in TypeScript. The executor specifically exposes a per-run MCP server for the Claude agent to call back into rimsky-side tools — rebuilding the MCP machinery in Go would be substantial work without ecosystem benefit.

The async-handoff-always pattern is structural: every `Execute` call responds with `AsyncAccepted` immediately and runs the agent in the background. A long agent run doesn't tie up a gRPC stream through load-balancer / restart events. The supervisor's in-memory callback registry holds the `async_ack_id → AsyncContext` map; if the supervisor restarts, the registry is lost and the dispatch falls to the orphan reaper.

Conformance: `cmd/rimsky-executor-conformance` treats claude-agent as just another gRPC endpoint. The `--require-stub-mode` flag uses `cmd/rimsky-conformance-probe` to verify the executor is running in stub mode before issuing real API calls.

## Code surface

- `executors/claude-agent/src/` — entire TS codebase (~20 source files, ~5,000 lines).
- `executors/claude-agent/package.json` — deps (`@grpc/grpc-js`, `@grpc/proto-loader`, `fastify`, `ajv`, `pino`, `@modelcontextprotocol/sdk`).
- `executors/claude-agent/src/server.ts` — gRPC server.
- `executors/claude-agent/src/server.test.ts` — async-callback wire-shape regression.
- `executors/claude-agent/src/internal-mcp-server.ts` — embedded MCP.
- `executors/claude-agent/src/http-bridge.ts` — Fastify HTTP+JSON bridge.
- `deploy/Dockerfile.claude-agent` — container.
- `protocols/proto/v1/executor.proto` — wire spec loaded at runtime.

## Prose surface

- `CLAUDE.md` "Build & test" — TS executor build commands.
- `CLAUDE.md` "Non-obvious gotchas" — TS claude-agent async-callback path (body key `type`).
- `docs/concepts/executor.md` — `executors/claude-agent` cited as reference TS impl.

## Adjacent topics

- `2026-05-10-executor-streamed-execute` — the gRPC stream pattern claude-agent implements.
- `2026-05-10-parked-state-and-resume` — claude-agent uses session_token for `--resume`.
- `2026-05-10-observability-optional-protocols` — claude-agent implements ExecutorObservability with declared_events + userdata_schema.
- `claude-agent-async-handoff-always` — the always-async pattern is its own design choice.

## Observations

- The package.json description says "claude-agent" but the Docker tag is `rimsky/executor-claude-agent`. The name carries through tests and config consistently.
- The proto loader uses `@grpc/proto-loader`'s dynamic-load mode (vs codegen). This means every executor startup reads the proto file from disk; the path is resolved relative to the binary or via the bundled copy in `dist/`. A proto file rename would require updating the loader path.
- The CLI runner spawns the `claude` binary as a child process; the executor expects `claude` to be on PATH or `CLAUDE_CLI_PATH` to be set. The conformance probe (`rimsky-conformance-probe`) reads a stub-mode env var to confirm the run is stubbed.
- The HTTP bridge (`http-bridge.ts`) is a per-executor observability surface; rimsky's `RunHandshake` probes it via gRPC for `GetCapabilities`, then the dashboard uses the bridge URL directly. So the executor speaks both protocols (gRPC for handshake + dispatch; HTTP+JSON for direct dashboard fetch).
- `pino` is the TS logger. The Go side uses stdlib `slog`. The two have similar JSON output shapes but field naming may differ; cross-process trace correlation relies on `dispatch_id` and `instance_id` carried verbatim.
