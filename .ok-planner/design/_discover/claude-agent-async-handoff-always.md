---
topic: claude-agent-async-handoff-always
kind: choice
---

# `claude-agent` executor always responds `AsyncAccepted` immediately; agent runs in background; POSTs final outcome to callback URL

## Description

The TS reference executor `executors/claude-agent/` implements `NodeExecutor.Execute` with an unconditional async-handoff pattern. Every `Execute` call:

1. Streams back one `Heartbeat` event.
2. Streams back one `AsyncAccepted` event carrying a fresh `async_ack_id`.
3. Closes the gRPC stream.
4. Runs the agent in the background (CLI spawn + observability + MCP server).
5. POSTs the final outcome event (`Complete | Errored | Blocked | ParkRequested`) to `${callback_url}/v1/callback/{async_ack_id}` per the supervisor's chi route.

This is the always-async-handoff pattern. Per `executors/claude-agent/src/server.ts:18-24`: "gRPC NodeExecutor implementation. Always responds with the async-handoff pattern: one Heartbeat + AsyncAccepted, close stream, run agent in background, POST final outcome to callback_url."

The decision is structural: agentic runs can take from seconds to hours, and the gRPC stream would have to survive load balancer restarts, scaling events, and supervisor restarts. The async-handoff sidesteps all of these by ending the stream immediately and using HTTP+JSON for the final delivery.

The callback POST body is keyed `type` (not `kind`) — enforced by the supervisor's chi route. The TS test suite at `executors/claude-agent/src/server.test.ts` exercises the exact wire shape. CLAUDE.md "Non-obvious gotchas": "TS claude-agent async-callback path. The executor must POST to `${callback_url}/v1/callback/{async_ack_id}` with body keyed `type` (not `kind`) — enforced by the Go supervisor's chi route. End-to-end test in `executors/claude-agent/src/server.test.ts`."

The supervisor's callback registry (`foundation/integration/callback.go`) holds an in-memory `async_ack_id → AsyncContext` map. A supervisor restart loses the map. When this happens, the in-flight dispatch's claim ages past the heartbeat cutoff and the orphan reaper sweeps it.

`callback_url` is built from `callback.advertise_host` (YAML) / `RIMSKY_SUPERVISOR_CALLBACK_ADVERTISE_HOST` (env) — the peer-reachable hostname. CLAUDE.md "Non-obvious gotchas" calls out the two-hostname issue: the supervisor binds on `0.0.0.0` but the URL it advertises must resolve from the executor's network position.

Other executors (`executors/http-node/`, `executors/stub/`) do NOT use always-async; they respond synchronously with `Complete | Errored | Blocked` when work is fast. The async-handoff is specifically for long-running agentic work — `claude-agent` is the only bundled executor that uses it.

The agent run itself is orchestrated by `agent-run.ts` (which drives `cli-runner.ts`) plus the per-run MCP server (`internal-mcp-server.ts`) that exposes rimsky-side tools (attribute writeback, named event emit, park) for the Claude agent to call. The observability ledger (`observability.ts`) records per-step trace events queryable via `ExecutorObservability.GetTrace` or the Fastify HTTP bridge.

The token registry (`token-registry.ts`) tracks short-lived bearer tokens for MCP authentication; rate limiting (`rate-limit.ts`) gates token issuance.

## Code surface

- `executors/claude-agent/src/server.ts` — gRPC server with always-async-handoff.
- `executors/claude-agent/src/server.test.ts` — wire-shape regression.
- `executors/claude-agent/src/agent-run.ts` — background agent orchestration.
- `executors/claude-agent/src/cli-runner.ts` + `cli-env.ts` — Claude CLI spawn.
- `executors/claude-agent/src/internal-mcp-server.ts` — per-run MCP server.
- `executors/claude-agent/src/observability.ts` — trace ledger.
- `executors/claude-agent/src/http-bridge.ts` — Fastify HTTP+JSON bridge.
- `foundation/integration/callback.go` — supervisor's callback chi route + in-memory registry.

## Prose surface

- `CLAUDE.md` "Non-obvious gotchas" — async-callback path; advertise_host.
- `docs/concepts/executor.md` — `AsyncAccepted` documented as the long-run pattern.

## Adjacent topics

- `2026-05-10-typescript-executor-claude-agent` — broader TS executor structure.
- `2026-05-10-executor-streamed-execute` — protocol-level pattern.
- `2026-05-10-parked-state-and-resume` — claude-agent uses session_token for `--resume`.
- `conformance-probe-stub-mode-handshake` — stub mode for safe conformance.

## Observations

- The always-async pattern is a property of claude-agent specifically, not the executor protocol. An executor that returns `Complete` immediately is wire-protocol-valid; claude-agent's choice to use async-handoff for every run is a design decision, not a protocol requirement.
- The supervisor's in-memory registry being a single-process map is a known limitation. A multi-replica supervisor deployment with sticky session would need a registry-replication path; rimsky's current model (orphan-and-retry on supervisor restart) is acceptable for the agent use case where retries are common.
- The body-key `type` vs `kind` wire-shape footgun is regression-tested per CLAUDE.md. A protocol upgrade that allowed both keys would soften the gate but make the protocol more lenient than the chi route documents.
- Heartbeats are emitted by the agent background process during the run (not by the gRPC stream after it closes); the supervisor's heartbeat-refresh path is separate from the gRPC stream lifecycle.
