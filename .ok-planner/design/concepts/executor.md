---
concept: executor
status: as-is
aliases: []
references:
  - _discover/2026-05-10-executor-streamed-execute.md
  - _discover/2026-05-10-typescript-executor-claude-agent.md
  - _discover/claude-agent-async-handoff-always.md
  - _discover/2026-05-10-observability-optional-protocols.md
  - _discover/2026-05-10-parked-state-and-resume.md
---

# Executor

## What it is

An executor is an out-of-process service that implements the gRPC `proto:executor.proto::Executor.Execute` server-streaming RPC plus optional `proto:executor_observability.proto::ExecutorObservability`. Bundled reference impls: `executors/http-node` (Go), `executors/stub` (Go), `executors/claude-agent` (TypeScript). Receives one `ExecuteRequest`, streams zero-or-more `Heartbeat`/`NamedEvent`, and exactly one `StreamClose` event carrying an outcome `oneof` (`Success | Error | Snooze | AwaitAsyncCallback`).

## Purpose

Executors are where actual work happens. Out-of-process gives language-portability, lets work scale independently of supervisors, and lets long-running work hand off to an async-callback channel without holding a gRPC stream open for hours.

## Boundaries

Owns: the per-dispatch work, the stream-close outcome vocabulary, the observability protocol surface, the userdata interpretation. Does NOT own: dispatch routing (supervisor's job), attribute schema validation (rimsky validates at dispatch + commit), substitution (rimsky's job before dispatch), the supervisor-side stitching from terminal event to producer verb (see `terminal-resolution`). Adjacent: `userdata`, `attribute`, `named-event`, `parked-state`, `lifecycle-handler`, `observability`, `terminal-resolution`, `service`.

## Invariants

- Exactly one `StreamClose` closes the stream; the executor MUST close the stream immediately after.
- `AwaitAsyncCallback` switches to expecting POST to `${callback_url}/v1/callback/{async_ack_id}` with body keyed `type` (not `kind`) — chi route enforced.
- `Heartbeat` events refresh `col:rimsky_node_runs.last_heartbeat_at`; cadence is executor-defined.
- `userdata_schema` reported via `ExecutorObservability.Capabilities` is the only place rimsky reads userdata-adjacent metadata (schema-only, not content).
- `declared_events` reported via observability is the source of truth for `on_event` template validation.

## Aliases and historical names

Pre-`spec:2026-05-12-nomenclature-resolution` Group E.1, the proto service name was `NodeExecutor`; the rename drops the `Node` prefix. The operator/binary vocabulary always used "executor" so no operator-visible churn. The pre-E.2 wire shape exposed per-terminal messages (`Complete | Blocked | Errored | AsyncAccepted | ParkRequested`); post-E.2 the wire shape is `StreamClose{outcome: Success | Error | Snooze | AwaitAsyncCallback}` with the historical `Blocked` collapsed into `Error{error_class: "executor_blocked"}`.

## Open within this concept

- Two distinct callback hostnames (bind vs advertise) — see `tensions/callback-hostname-split.md`.

## Notes

- Proto service renamed `NodeExecutor` → `Executor` per `spec:2026-05-12-nomenclature-resolution` Group E.1. Wire-event shape rewritten to channel-mechanics (`StreamClose` + outcome oneof) per Group E.2; `Blocked` collapsed into `Error{error_class}`; `ParkRequested` renamed `Snooze` (the state-machine value `'parked'` is unchanged). Capabilities RPC renamed `GetCapabilities` → `Capabilities` per Group E.11. Resolves `tension:_resolved/terminal-event-overloaded` and `tension:_resolved/async-callback-body-key`.

