---
topic: executor-streamed-execute
kind: choice
---

# `NodeExecutor.Execute` is server-streamed gRPC; long runs hand off to HTTP+JSON async callback

## Description

A node's execution can take milliseconds (deterministic transforms via `executors/stub`) to hours (an agentic claude-agent run with `--resume` semantics). The dispatch protocol needs to support both, plus heartbeats during long runs, plus a clean handoff to async-callback mode when keeping a gRPC stream open is impractical.

`NodeExecutor.Execute` is a server-streamed gRPC RPC (`protocols/proto/v1/executor.proto:17-25`):

- One `ExecuteRequest` from the supervisor (carries `dispatch_id`, `node_id`, `instance_id`, `node_type`, `userdata`, `attributes`, `attributes_schema`, `stores`, `callback_url`, `cancel_token`, `run_attempt`, optional `resume_context`).
- Zero or more `Heartbeat` / `NamedEvent` events streamed back from the executor.
- Exactly one terminal event: `Complete | Blocked | Errored | AsyncAccepted | ParkRequested`.
- Stream closes.

Terminal semantics differ:

- **`Complete`** — success. Carries `changed: bool` and the writeback delta for attributes. Validated against the schema at commit (`@blessed-invariant 12`).
- **`Blocked`** — "I produced output but explicitly chose not to claim success" (e.g. low-confidence routing). Distinct from `Errored`. Routed via `on_executor_blocked` handler.
- **`Errored`** — true failure. Carries an `error_class` string; the supervisor's policy chain looks up the action.
- **`AsyncAccepted`** — long-running handoff. Carries an `async_ack_id`. The supervisor switches to expecting a POST to `${callback_url}/v1/callback/{async_ack_id}` with the final event. The HTTP body is `AsyncCallbackBody` (executor.proto:192-208); the body key is `type` (not `kind`) per the chi route at `foundation/integration/callback.go`.
- **`ParkRequested`** — non-terminal-but-paused. Carries opaque `payload`, optional `session_token`, optional `resume_at`. Transitions the node to `parked` state; the claim is retained; the resume re-dispatches with `ResumeContext` populated.

The async-callback path is what lets an executor outlive the gRPC stream — important in a multi-replica deployment where load balancers, restarts, and scaling events cannot be relied on to keep the stream open for hours. `executors/claude-agent/` always uses this path: every dispatch responds with `AsyncAccepted` immediately and runs the actual agent in the background, then POSTs the final outcome.

The HTTP+JSON-bridge (separate from the async-callback path) is also documented for languages without convenient gRPC tooling: `docs/concepts/executor.md` and `executors/http-node/bridge.go`. The bridge maps gRPC request/response onto HTTP+JSON so a curl-friendly executor can implement the protocol without gRPC bindings.

CLAUDE.md "Non-obvious gotchas" calls out two specifics:

- **Two distinct callback hostnames.** The supervisor binds its callback HTTP listener on `0.0.0.0`, but the URL it advertises must be peer-reachable. `callback.advertise_host` (YAML) / `RIMSKY_SUPERVISOR_CALLBACK_ADVERTISE_HOST` (env) is the peer-reachable hostname.
- **Body key `type`, not `kind`.** Enforced by the chi route; the TS claude-agent test suite at `executors/claude-agent/src/server.test.ts` exercises the exact wire shape.

The supervisor's callback registry is in-memory (`foundation/integration/callback.go`): `async_ack_id → AsyncContext`. A supervisor restart loses the map, and the dispatch falls to the orphan reaper. This is acceptable because async runs hold heartbeating claims — when the supervisor restarts, the claims age past the orphan cutoff and the reaper sweeps them.

## Code surface

- `protocols/proto/v1/executor.proto` — entire file (`NodeExecutor` service, `ExecuteRequest`, `ExecuteEvent`, terminal variants, `AsyncCallbackBody`, `ResumeContext`).
- `foundation/integration/callback.go` — async-callback chi route + in-memory registry.
- `foundation/integration/runner_dispatch.go` — dispatch site that issues `Execute`.
- `foundation/integration/runner_terminal*.go` — terminal-event handlers.
- `executors/http-node/server.go` / `bridge.go` — reference Go executor implementing both gRPC and HTTP+JSON bridge.
- `executors/claude-agent/src/server.ts` — TS executor doing AsyncAccepted-always pattern.
- `executors/claude-agent/src/server.test.ts` — wire-shape regression tests.

## Prose surface

- `docs/concepts/executor.md` — concept-doc treatment; `NodeExecutor`, async-callback path, HTTP+JSON bridge.
- `docs/protocols/executor.md` — implementer's guide.
- `CLAUDE.md` "Non-obvious gotchas" — `advertise_host`, body-key-`type`.
- `.ok-planner/specs/2026-05-04-service-protocol-contract.md` — protocol contract.

## Adjacent topics

- `2026-05-10-parked-state-and-resume` — `ParkRequested` semantics.
- `2026-05-10-typescript-executor-claude-agent` — the TS reference impl.
- `claude-agent-async-handoff-always` — TS executor specifics.

## Observations

- The five terminal events have different parallel semantics; `Complete` and `Errored` are "always terminal," `AsyncAccepted` is "stream-terminal but logical-non-terminal," and `ParkRequested` is "terminal-but-resumable." A reader counting "terminal events" gets different numbers depending on what counts as terminal.
- The body-key `type` vs `kind` distinction is a known footgun documented in CLAUDE.md, executor.md, and `executors/claude-agent/src/server.test.ts`. The chi route's error message could be more explicit.
- The HTTP+JSON bridge (`executors/http-node/bridge.go`) is described as available for non-Go peers but `executors/claude-agent/` uses gRPC + async-callback (not the bridge). The bridge is for executors that genuinely cannot speak gRPC.
- `Heartbeat` events are zero-or-more and non-terminal; their cadence and content are executor-defined. They primarily refresh the worker-request heartbeat column (`@blessed-invariant 6` cutoff).
