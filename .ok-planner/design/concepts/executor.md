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

An executor is an out-of-process service that implements the gRPC `proto:executor.proto::Executor.Execute` server-streaming RPC plus optional `proto:executor_observability.proto::ExecutorObservability`. Bundled reference impls: `executors/http-node` (Go), `executors/stub` (Go), `executors/claude-agent` (TypeScript). Receives one `ExecuteRequest`, streams zero-or-more `Heartbeat`/`NamedEvent`, and exactly one `StreamClose` event carrying an outcome `oneof` (`Success | Error | Park | AwaitAsyncCallback`). `Park` carries an inner `ParkReason ∈ {AWAIT_CALLBACK, SNOOZE}`; the two-value taxonomy is closed (`concept:parked-state`).

## Purpose

Executors are where actual work happens. Out-of-process gives language-portability, lets work scale independently of supervisors, and lets long-running work hand off to an async-callback channel without holding a gRPC stream open for hours.

## Boundaries

Owns: the per-dispatch work, the stream-close outcome vocabulary, the observability protocol surface, the userdata interpretation. Does NOT own: dispatch routing (supervisor's job), attribute schema validation (rimsky validates at dispatch + commit), substitution (rimsky's job before dispatch), the supervisor-side stitching from terminal event to producer verb (see `terminal-resolution`), operator-decided retry/pass/give_up on Error (see `error-policy`). Adjacent: `attribute`, `named-event`, `parked-state`, `error-policy`, `observability`, `terminal-resolution`, `service`.

The bundled SQL-based store `stores/postgres/` registers this protocol alongside `concept:claim-producer`. The same binary plays both roles via separate gRPC service registrations on a single endpoint. Future SQL-substrate stores may adopt the same pattern.

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

- 2026-05-14: `Park.reason` typed as `ParkReason` enum on the wire; new `reason_note` field carries human annotation. The Notes section already references the prior Snooze→Park rename; this entry sits alongside it. Per spec Piece 2 `.ok-planner/specs/2026-05-14-subscription-cascade-and-quality-of-life-design.md`.
- 2026-05-19 — `stores/postgres/` extends to the executor role per spec 2026-05-19-multi-instance-template-ergonomics-design.
- 2026-05-23 — Per spec `.ok-planner/specs/2026-05-23-signal-taxonomy-and-policy-decoupling-design.md`: executor terminal vocabulary is the 4-variant `StreamClose.outcome` (`Success | Error | Park | AwaitAsyncCallback`); operator-decided retry is via the operator's `error_types:` chain on `Error`, not an executor wire surface. Executors handle internal retry silently or via `Park{reason: SNOOZE}`. The pre-existing Notes entry mis-listing `Snooze` as the third oneof variant has been corrected above to `Park`.
