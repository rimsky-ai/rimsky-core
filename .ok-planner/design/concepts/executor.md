---
concept: executor
status: as-is
aliases:
  - peer executor
  - node-executor
references:
  - _discover/2026-05-10-executor-streamed-execute.md
  - _discover/2026-05-10-typescript-executor-claude-agent.md
  - _discover/claude-agent-async-handoff-always.md
  - _discover/2026-05-10-observability-optional-protocols.md
  - _discover/2026-05-10-parked-state-and-resume.md
---

# Executor

## What it is

An executor is an out-of-process peer service that implements the gRPC `NodeExecutor.Execute` server-streaming RPC plus optional `ExecutorObservability`. Bundled reference impls: `executors/http-node` (Go), `executors/stub` (Go), `executors/claude-agent` (TypeScript). Receives one `ExecuteRequest`, streams zero-or-more `Heartbeat`/`NamedEvent`, and exactly one terminal: `Complete | Blocked | Errored | AsyncAccepted | ParkRequested`.

## Purpose

Executors are where actual work happens. Out-of-process gives language-portability, lets work scale independently of supervisors, and lets long-running work hand off to an async-callback channel without holding a gRPC stream open for hours.

## Boundaries

Owns: the per-dispatch work, the terminal-event vocabulary, the observability protocol surface, the userdata interpretation. Does NOT own: dispatch routing (supervisor's job), attribute schema validation (rimsky validates at dispatch + commit), substitution (rimsky's job before dispatch), the supervisor-side stitching from terminal event to producer verb (see `terminal-resolution`). Adjacent: `userdata`, `attribute`, `named-event`, `parked-state`, `lifecycle-handler`, `observability`, `terminal-resolution`.

## Invariants

- The five terminal-event variants close the stream exactly once.
- `AsyncAccepted` switches to expecting POST to `${callback_url}/v1/callback/{async_ack_id}` with body keyed `type` (not `kind`) — chi route enforced.
- `Heartbeat` events refresh `rimsky_worker_request.last_heartbeat_at`; cadence is executor-defined.
- `userdata_schema` reported via `ExecutorObservability.Capabilities` is the only place rimsky reads userdata-adjacent metadata (schema-only, not content).
- `declared_events` reported via observability is the source of truth for `on_event` template validation.

## Aliases and historical names

The Go interface name is `NodeExecutor`; the operator/binary vocabulary just uses `executor`.

## Open within this concept

- `AsyncAccepted` is "stream-terminal but logically-non-terminal"; `ParkRequested` is "terminal-but-resumable". Different parallel semantics under one "terminal" word — see `tensions/terminal-event-overloaded.md`.
- Body key `type` vs `kind` async-callback footgun — see `tensions/async-callback-body-key.md`.
- Two distinct callback hostnames (bind vs advertise) — see `tensions/callback-hostname-split.md`.

