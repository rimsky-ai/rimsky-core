# Agentic telemetry for rimsky

**Date:** 2026-05-07
**Status:** Sketch — pre-spec design exploration
**Audience:** Future planner / implementer; rimsky maintainers

## Context

Today rimsky's claude-agent executor runs long Sonnet dispatches (≥30 min) inside docker-compose. Operators (human + LLM) have only two coarse signals:

1. **Polling** the events table or `/events` endpoint via HTTP GET.
2. **Tailing** the executor's pino logs via `docker compose logs -f`.

Neither is push-shaped. An LLM operator (e.g., a Claude Code session driving a smoke run) has to either burn cycles polling or babysit a fixed-cadence wakeup. When an interesting transition happens (subprocess exit, callback fired, frame stuck) the operator only learns about it on the next poll tick.

Two failure modes have driven this sketch:

- **"Subprocess exited 0 without report_complete."** Detectable only by parsing pino logs after the fact. We just shipped a resume-with-prompt-injection retry inside the executor itself, but a higher-level observer (rimsky, or an LLM monitor) has no way to *react* to this transition in real time.
- **Long, expensive smoke runs are the only way to validate orchestration changes.** Most of the bugs we've actually hit are integration-shape — silence_timeout, missing report_complete, callback-token substitution. Reproducing them in a 30-second test instead of a 30-minute test would change the iteration cost by two orders of magnitude.

Both are observability gaps, plus one tooling gap.

## Goals

1. **Push, not poll.** Give operators (human + LLM) a way to subscribe to rimsky state transitions and receive them as events without polling.
2. **First-class subprocess lifecycle.** The claude-agent subprocess's spawn/exit/retry transitions should appear in the rimsky events stream, not only in pino logs.
3. **Per-dispatch cost & shape.** Token counts, USD cost, max subagent depth, callback verb history — captured per dispatch and queryable.
4. **Cheap-test mode.** A way to exercise orchestration end-to-end (or a synthetic-blocker scenario) without spending real Sonnet tokens.

## Non-goals

- Distributed tracing (OpenTelemetry spans across producer/executor/supervisor). Future work; this sketch stays inside rimsky's existing event vocabulary.
- A dashboard UI. The event stream is the API; UI is downstream.
- Replacing pino logs. Logs stay as the unstructured deep-debug surface.

## Design

### 1. SSE event stream — `GET /events/stream`

Today: `GET /events?instance_id=X` returns a one-shot JSON snapshot.

Proposed: add a streaming sibling at `GET /events/stream?instance_id=X` (or `?frame_id=X`, `?node_id=X`). Server-Sent Events. Each new row in `rimsky_events` matching the filter is flushed as `data: {…}\n\n`. The handler holds a postgres `LISTEN`/`NOTIFY` subscription so we don't poll the table internally.

Why SSE over websocket: chi already speaks HTTP/1.1 streaming, no extra dependency, browser + curl + `EventSource` clients all work. We have no client-to-server message channel needed — pure subscribe.

LLM operator usage:

```
Monitor(
  command: "curl -N http://localhost:8080/events/stream?instance_id=$IID",
  persistent: true,
)
```

Each event arrives as a `<task-notification>` and wakes the loop immediately. No polling cadence to tune.

### 2. First-class CLI subprocess events

Today the claude-agent executor emits `cli.spawned` / `cli.exited` / `cli.silence_timeout` / `cli.resumed` as pino logs only. They never reach `rimsky_events`.

Proposed: extend the existing executor → control-api callback channel with a new verb (`emit_event` or `record_observation`) that the executor calls at subprocess transitions. Control-api validates and writes a row to `rimsky_events`.

Event kinds (initial):

| Kind                              | Fired when                                                         | Payload                                              |
|-----------------------------------|--------------------------------------------------------------------|------------------------------------------------------|
| `executor.subprocess_spawned`     | claude-agent forks the CLI subprocess                              | pid, model, session_id, allowed_tools                |
| `executor.subprocess_exited`      | subprocess exits (any code/signal)                                 | exit_code, signal, duration_ms, terminal_callback_fired |
| `executor.subprocess_resumed`     | retry path invokes `resume()`                                      | session_id, retry_reason, prompt_summary            |
| `executor.silence_timeout`        | silence watchdog fires                                             | last_stdout_age_ms, threshold_ms                     |
| `executor.cost_recorded`          | parsed `result` event from stream-json (final tokens + USD)        | input_tokens, output_tokens, cache_*, usd            |
| `executor.subagent_depth_observed`| max depth seen this dispatch (recorded once at terminal)           | max_depth, subagent_count                            |

These are recorded under the worker_request_id, so the existing `/events?worker_request_id=...` filter works for them automatically.

Trade-off: the executor now writes to two channels (rimsky callbacks + pino logs). That's intentional — pino stays for low-level debug; rimsky-events becomes the ergonomic operator surface. Don't try to unify. (Tracked-duplication policy in `cold-read-cheatsheet.md` applies — these are intentional copies for two different audiences.)

### 3. Token / cost / subagent-depth capture

The Claude Code CLI's `--output-format stream-json --verbose` already streams a final `result` event with input/output token counts, cache stats, and USD cost. The agent-run.ts loop already consumes this stream.

Proposed: parse the `result` event in agent-run.ts; emit `executor.cost_recorded` (verb above) just before reporting the terminal outcome to the supervisor.

Subagent depth: stream-json events carry a `parent_tool_use_id` for tool calls inside a subagent's context. Walk the chain at terminal time, record max depth, emit `executor.subagent_depth_observed`.

Both are in-process work in agent-run.ts; no protocol change beyond the new event verb.

### 4. Heartbeat enrichment

Today the supervisor heartbeat writes `last_heartbeat_at` and (post the recent fix) `active_node_count`. Proposed additions, all on `rimsky_supervisor`:

- `last_subprocess_stdout_at` — timestamp of last stream-json line received from any in-flight subprocess. Enables "is this dispatch genuinely working or is the model thinking forever?"
- `last_callback_kind` — last MCP callback verb received, per dispatch. Stored on `rimsky_worker_request` rather than the supervisor, but written from the same heartbeat path.

Both are optional; the heartbeat tick remains the same shape.

### 5. Synthetic-blocker test mode (the cheap-test gap)

Two new test tiers, both rimsky-internal:

**Tier A: Stub-mode end-to-end smoke.** We already have `RIMSKY_EXECUTOR_STUB_MODE=1` for unit tests. Proposed: a docker-compose profile (`deploy/docker-compose.stub.yml`) that brings up the full stack with the claude-agent in stub mode, runs a canned multi-template scenario, and asserts the events table reaches a specified terminal shape. No Anthropic calls. Validates wire integrations (cascade, frame resolution, lock acquisition, callback routing, terminal cleanup) in seconds.

**Tier B: Synthetic-blocker dispatch.** A real-CLI dispatch with a tiny prompt that deliberately triggers each known failure mode. Examples:

- `prompt = "Exit immediately without calling any tool."` → exercises the resume-retry path. Should emit `subprocess_exited` (no terminal_callback) → `subprocess_resumed` → `subprocess_exited` (no terminal_callback) → `executor.errored(subprocess_exit_before_complete)`.
- `prompt = "Sleep silently for 90 seconds then exit cleanly."` → exercises the silence_timeout path.
- `prompt = "Spawn 5 nested Task subagents and have the deepest one call report_complete."` → exercises subagent-depth observation.

These are useful both as CI assertions ("the resume retry actually fires") and as cost-bounded reproduction harnesses for new failure modes.

Mechanically: a small `cmd/rimsky-synthetic-blocker/` binary (or a `--scenario synthetic-blocker-N` flag on `rimsky-executor-conformance`) brings up the stack, dispatches the canned template, watches `/events/stream`, asserts the expected event sequence, and exits non-zero on deviation.

## Open questions

1. **`LISTEN`/`NOTIFY` vs. tailing the events table.** SSE handlers can either subscribe to a postgres NOTIFY channel published by the events writer, or poll the table on a tight cadence. NOTIFY is cleaner but adds a write-side concern; cadence-tail is simpler but worse for tail latency. SQLite has no NOTIFY — would need a polling fallback there anyway, so maybe just polling is fine.

2. **Where does the new event verb live in the protocol?** Adding it to the executor → callback channel is convenient but technically widens the protocol surface. Alternative: a side-channel HTTP endpoint on control-api that's just for telemetry-shaped events. Probably the side-channel is cleaner — telemetry isn't lifecycle.

3. **Do we promote pino's `cli.silence_timeout` log to a rimsky event AND keep the log line?** Tracked duplication (per cold-read) says yes — different audiences. Worth documenting at the call site.

4. **Subagent depth is best-effort.** The CLI doesn't always emit `parent_tool_use_id` in a way that survives Task→Task→Task nesting cleanly. Initial implementation might just count the number of `tool_use` events with a tool name of `Task` and call that "subagent_count" — depth might not be reliably reconstructable from stream-json alone.

5. **Where do synthetic-blocker assertions live?** `test/smoke/` already exists for testcontainer-based scenario tests. Synthetic-blocker probably belongs there, gated behind a build tag (`//go:build smoke_anthropic`) so it only runs when `CLAUDE_CODE_OAUTH_TOKEN` is in env.

## What this enables (concretely)

For an LLM operator running a smoke (today's use case): arm one Monitor on `/events/stream?instance_id=X`. Receive a push for every cascade transition, every subprocess spawn/exit, every cost-recorded, every retry. No polling cadence. Real-time intervention possible (e.g., cancel a runaway dispatch the moment cost exceeds threshold) without parsing logs.

For a CI pipeline: synthetic-blocker tier B asserts the resume-retry mechanism still fires. We never again ship a release where the agent silently exits 0 without report_complete and the failure goes unnoticed for 30 minutes.

For a human operator: the events table grows a per-dispatch cost ledger. "Which template is burning the most tokens" becomes a SQL query.

## Sketch boundaries

This sketch deliberately stops short of:

- Specifying the SSE wire shape in detail (event names, JSON envelope, heartbeat keepalive cadence).
- Picking the LISTEN/NOTIFY vs. polling fallback.
- Designing the synthetic-blocker scenario library beyond the three example prompts.
- Building any auth model for the stream endpoint (control-api auth applies, whatever that is today).

A proper spec would settle each. This sketch's job is to answer "is this worth spec-ing?" — and to record the failure modes and use cases that motivate the answer.
