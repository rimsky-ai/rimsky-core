---
concept: executor
status: as-is
aliases: []
---

# Executor

## What it is

An executor implements the gRPC executor's server-streaming execute method plus an optional executor-observability protocol. Implementations come in two forms — in-process handlers registered with the dispatch pool, and out-of-process services (gRPC or HTTP-bridge) — and the protocol surface (execute, the four stream-close outcome variants, the observability handshake) is identical across both. The executor receives one execute request, streams zero-or-more heartbeat / named-event messages, and exactly one stream-close event carrying one of four outcome variants (success, error, park, await-async-callback). The park outcome carries an inner park reason from the closed two-value set `AWAIT_CALLBACK | SNOOZE` (per `concept:parked-state`). Production-side reference implementations (an HTTP-node executor, an LLM-agent executor, and two verifier executors) live on the consumption side, outside the platform. The stub test-double executor and the bundled in-process loop-counter handler are the in-rimsky implementations.

## Purpose

Executors are where actual work happens. Out-of-process gRPC executors give language-portability, scale-independence, and async-callback handoff for long-running work. In-process executors deliver utility-node primitives (counters, gates, simple computations) without the deploy / image / IPC overhead, sharing the same protocol surface so the dispatch path treats both forms uniformly.

## Scratch

Every executor receives a scratch field on its execute request carrying the dispatch row's currently persisted scratch bytes (empty on first dispatch). The executor may write scratch in two ways — mid-dispatch by POSTing to a scratch HTTP callback route (paralleling the executor protocol's existing attributes incremental-writeback HTTP callback), or at stream-close by attaching scratch bytes to the settling outcome (success, error, or park; the await-async-callback transient is excluded since it does not settle the dispatch). Both writes persist on the dispatch row. The bytes are opaque to rimsky — the inertness invariant (`concept:inertness` / `@blessed-invariant 21`) extends to scratch — and scratch carries forward to a successor dispatch of the same node only via the three recovery enqueue paths that stamp `prior_dispatch_id` on the new row (heartbeat-stale recovery, retry-after-error, recalculate, per `concept:node-run`). Normal cascade re-fires of the same node do not stamp `prior_dispatch_id` and so do not carry scratch; for that case same-node state rides the attribute carry-forward channel (per `concept:attribute`).

## Boundaries

Owns: the per-dispatch work, the stream-close outcome vocabulary, the observability protocol surface, the userdata interpretation; per-dispatch executor-attached opaque scratch bytes (the executor sets scratch mid-dispatch via the scratch callback route or at stream-close by attaching scratch bytes to the outcome). Does NOT own: dispatch routing (supervisor's job), attribute schema validation (rimsky validates at dispatch + commit), substitution (rimsky's job before dispatch), the supervisor-side stitching from terminal event to producer verb (see `terminal-resolution`), operator-decided retry/pass/give_up on the error outcome (see `error-policy`). Adjacent: `attribute`, `named-event`, `parked-state`, `error-policy`, `observability`, `terminal-resolution`, `service`.

The bundled SQL-based reference store registers this protocol alongside `concept:claim-producer`. The same binary plays both roles via separate gRPC service registrations on a single endpoint. Other SQL-substrate stores can use the same pattern.

## Invariants

- Exactly one stream-close event closes the stream; the executor MUST close the stream immediately after.
- The await-async-callback outcome switches to expecting an async-callback POST keyed on the assigned ack id; that POST body carries exactly one outcome key — `success`, `error`, or `park` — enforced by the supervisor's callback route, which rejects any other body shape.
- Heartbeat events refresh the node-run's last-heartbeat timestamp; cadence is executor-defined.
- The userdata schema reported via the observability capabilities call is the only place rimsky reads userdata-adjacent metadata (schema-only, not content).
- The declared-events list reported via observability is the source of truth for subscription template validation.
