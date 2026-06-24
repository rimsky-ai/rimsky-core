# @rimsky/executor-claude-agent

TypeScript reference implementation of a rimsky v1 Executor that runs
agentic cells by spawning the Claude CLI as a subprocess, giving it an internal
MCP callback URL, and relaying the subprocess's structured outcome back to the
rimsky supervisor.

## Design

Speaks rimsky's Executor protocol (see
`../../proto/v1/executor.proto`) loaded at runtime via `@grpc/proto-loader`.
Always uses the async-handoff pattern:

1. `Execute(ExecuteRequest)` is received over gRPC.
2. Executor emits one `Heartbeat` + `StreamClose{AwaitAsyncCallback}` and closes the stream.
3. Executor runs the agent in the background; posts final outcome to
   `callback_url` (HTTP+JSON, supervisor-supplied).

The agent runtime:

- Starts a loopback HTTP/JSON-RPC callback endpoint (the "internal MCP" server)
  the Claude CLI subprocess can invoke via its MCP-HTTP transport.
- Spawns the `claude` binary with the callback URL and a per-run token.
- Watches the CLI's `stream-json` output for progress signals and tears the
  subprocess down on two configurable deadlines, both disabled by default:
  - **Silence** — no stdout bytes within `cli.silence_timeout_ms` AND no
    tool_use is in flight. Emits `terminal/error/agent/timeout` with
    `silence_duration_ms`.
  - **Tool-in-flight** — the oldest open tool_use (started via an
    `assistant`/`tool_use` event but not yet closed by a matching
    `user`/`tool_result`) has been open longer than
    `cli.tool_use_timeout_ms`. Emits `terminal/error/agent/tool_use_timeout`
    with `tool_use_id`, `tool_name`, and `duration_ms`.
  - While a tool_use is open the silence detector is **paused** (a long-running
    `Bash` subprocess with zero CLI stdout is recognized as honest work, not
    a hang). Once `tool_result` closes the tool_use, silence detection
    resumes against the next stream event.
- Releases the callback token when the run ends.

## Stub mode

Setting `RIMSKY_EXECUTOR_STUB_MODE=1` short-circuits the agent runtime: no
Claude CLI spawn, no network calls, no internal MCP server. Returns a canned
`StreamClose{Success{attributes_delta: {stub: true}}}` after ~50ms. All tests run under stub mode.

## Attribute fields

Post-2026-05-21 userdata-collapse, the executor reads these keys out of
the unified `attributes` bag per dispatch. Each key surfaces through the
node's attribute schema (`attributes.schema.properties.<key>`) — either
as a `default:` value supplied at registration or a `source:` directive
resolved at dispatch. The executor's expected attribute schema is
declared in `src/expected-attributes-schema.ts`; see
`docs/executors/claude-agent/expected-attributes.md` for the full
catalog and migration table:

| key | type | purpose |
| --- | --- | --- |
| `model` | string | Claude model passed to the CLI's `--model` flag |
| `system_prompt` | string | system prompt, typically a `default:` value (or a `source:` directive resolved from an upstream attribute) |
| `user_prompt` | string | user prompt, typically a `source:` directive resolved at dispatch |
| `cwd_from_store` | string | name of a claim-producer entry from `ExecuteRequest.claim_producers` whose handle `address` (the filesystem claim-producer fills this with an absolute path) is used as the CLI's working directory. Validated as an existing directory before spawn; mismatches error as `invalid_cwd_from_store`. |
| `cwd` | string | raw working-directory override. Lower priority than `cwd_from_store`. Same existing-directory validation. |

`cwd_from_store` is the recommended way to bind a CLI run to a workspace
the supervisor has already serialized via a filesystem-store directory
claim — the store's lock primitive (two claims on the same path
conflict) ensures only one agent is in the directory at a time.

**Operator note:** for `cwd_from_store` to resolve, the executor pod
must mount the store-service's root volume **at the same absolute path
the store-service uses**. The address bytes returned by the filesystem
store are a path on the store-service's filesystem; if the executor pod
mounts the same volume elsewhere the path won't exist and the spawn
will error.

## Entry points

- `src/main.ts` — Node binary: `rimsky-executor-claude-agent`
- `src/server.ts` — gRPC server implementation
- `src/http-bridge.ts` — HTTP+JSON bridge (Fastify) for callers that prefer HTTP
- `src/agent-run.ts` — agent runtime (subprocess + MCP callback + silence watch)

## Environment

| Variable                         | Default       | Purpose                                                             |
| -------------------------------- | ------------- | ------------------------------------------------------------------- |
| `RIMSKY_EXECUTOR_PORT_GRPC`      | `9090`        | gRPC bind port                                                      |
| `RIMSKY_EXECUTOR_PORT_HTTP`      | `9190`        | HTTP bridge bind port                                                |
| `RIMSKY_EXECUTOR_HOST`           | `0.0.0.0`     | bind host                                                           |
| `RIMSKY_EXECUTOR_STUB_MODE`      | unset         | `1` to enable stub mode                                             |
| `RIMSKY_EXECUTOR_CLAUDE_BINARY`  | `claude`      | path to Claude CLI                                                   |
| `RIMSKY_EXECUTOR_SILENCE_MS`     | `0`           | deployment-wide stdout-silence-detection timeout (ms). `0` = disabled. Overridden per-node by `cli.silence_timeout_ms`. |
| `RIMSKY_EXECUTOR_TOOL_USE_TIMEOUT_MS` | `0`      | deployment-wide tool-in-flight timeout (ms). `0` = disabled. Overridden per-node by `cli.tool_use_timeout_ms`. |
| `RIMSKY_EXECUTOR_CALLBACK_HOST`  | `127.0.0.1`   | host for the internal MCP callback URL advertised to the subprocess |
| `ANTHROPIC_API_KEY`              | unset         | Anthropic API key (production). See auth precedence below.          |
| `CLAUDE_CODE_OAUTH_TOKEN`        | unset         | Claude Code OAuth token (dev). See auth precedence below.           |

### CLI auth precedence

At least one of `ANTHROPIC_API_KEY` or `CLAUDE_CODE_OAUTH_TOKEN` must be set
in non-stub mode; the executor exits with a fatal error at startup
otherwise. Resolution order:

1. **`ANTHROPIC_API_KEY` (production)** — wins if set. The executor writes
   the key to a 0600 temp file, points a temp `$HOME/.claude/settings.json`
   at an `apiKeyHelper` shell wrapper, and passes only `HOME` + `PATH` to
   the subprocess. The API key never enters the child env.
2. **`CLAUDE_CODE_OAUTH_TOKEN` (dev)** — fallback. Passed through as
   `CLAUDE_CODE_OAUTH_TOKEN` on the child env, with the executor's real
   `$HOME` retained.

The executor's own `process.env` is **not** inherited into the spawned
`claude` subprocess — only the auth env, `PATH`, and the per-run
`RIMSKY_CALLBACK_URL` / `RIMSKY_CALLBACK_TOKEN` reach it. This keeps
unrelated pod env (DB DSNs, internal callback secrets, etc.) out of the
CLI. Pattern ported from `skillprompting/brain/src/cli-env.ts`.

## Commands

```bash
npm install
npx tsc --noEmit
npm run build
npm test
```
