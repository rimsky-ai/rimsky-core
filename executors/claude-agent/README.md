# @rimsky/executor-claude-agent

TypeScript reference implementation of a rimsky v1 NodeExecutor that runs
agentic cells by spawning the Claude CLI as a subprocess, giving it an internal
MCP callback URL, and relaying the subprocess's structured outcome back to the
rimsky supervisor.

## Design

Speaks rimsky's NodeExecutor protocol (see
`../../proto/v1/node_executor.proto`) loaded at runtime via `@grpc/proto-loader`.
Always uses the async-handoff pattern:

1. `Execute(ExecuteRequest)` is received over gRPC.
2. Executor emits one `Heartbeat` + `AsyncAccepted` and closes the stream.
3. Executor runs the agent in the background; posts final outcome to
   `callback_url` (HTTP+JSON, supervisor-supplied).

The agent runtime:

- Starts a loopback HTTP/JSON-RPC callback endpoint (the "internal MCP" server)
  the Claude CLI subprocess can invoke via its MCP-HTTP transport.
- Spawns the `claude` binary with the callback URL and a per-run token.
- Watches for silence (no stdout within `silenceTimeoutMs`) and tears the
  subprocess down as `silence_timeout` → Errored.
- Releases the callback token when the run ends.

## Stub mode

Setting `RIMSKY_EXECUTOR_STUB_MODE=1` short-circuits the agent runtime: no
Claude CLI spawn, no network calls, no internal MCP server. Returns a canned
`Complete { stub: true }` after ~50ms. All tests run under stub mode.

## Entry points

- `src/main.ts` — Node binary: `rimsky-executor-claude-agent`
- `src/server.ts` — gRPC server implementation
- `src/http-bridge.ts` — HTTP+JSON bridge (Fastify) for callers that prefer HTTP
- `src/agent-run.ts` — agent runtime (subprocess + MCP callback + silence watch)

## Environment

| Variable                         | Default       | Purpose                                                             |
| -------------------------------- | ------------- | ------------------------------------------------------------------- |
| `RIMSKY_EXECUTOR_PORT_GRPC`      | `7071`        | gRPC bind port                                                      |
| `RIMSKY_EXECUTOR_PORT_HTTP`      | `7072`        | HTTP bridge bind port                                                |
| `RIMSKY_EXECUTOR_HOST`           | `0.0.0.0`     | bind host                                                           |
| `RIMSKY_EXECUTOR_STUB_MODE`      | unset         | `1` to enable stub mode                                             |
| `RIMSKY_EXECUTOR_CLAUDE_BINARY`  | `claude`      | path to Claude CLI                                                   |
| `RIMSKY_EXECUTOR_SILENCE_MS`     | `120000`      | silence-detection timeout                                            |
| `RIMSKY_EXECUTOR_CALLBACK_HOST`  | `127.0.0.1`   | host for the internal MCP callback URL advertised to the subprocess |

## Commands

```bash
npm install
npx tsc --noEmit
npm run build
npm test
```
