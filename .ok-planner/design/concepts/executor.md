---
concept: executor
status: as-is
aliases: []
---

# Executor

## What it is

An executor implements the gRPC executor's unary Execute method plus an optional executor-observability protocol. Implementations come in two forms — in-process handlers registered with the dispatch pool, and out-of-process services (gRPC or HTTP-bridge) — and the protocol surface (execute, the four outcome variants, the observability handshake) is identical across both. The executor receives one execute request and returns one Outcome — exactly one of `Success | Error | Park | AwaitAsyncCallback`. The Park outcome carries an inner park reason from the closed two-value set `AWAIT_CALLBACK | SNOOZE` (per `concept:parked-state`). The AwaitAsyncCallback outcome defers the verdict to a later HTTP POST against the supervisor's callback URL carrying an `AsyncCallbackBody` whose own outcome is one of the three settling terminals. Production-side reference implementations live on the consumption side, outside the platform. The stub test-double executor and the bundled in-process loop-counter handler are the in-rimsky implementations.

## Purpose

Executors are where actual work happens. Out-of-process gRPC executors give language-portability, scale-independence, and async-callback handoff for long-running work. In-process executors deliver utility-node primitives (counters, gates, simple computations) without the deploy / image / IPC overhead, sharing the same protocol surface so the dispatch path treats both forms uniformly.

## Scratch

Every executor receives a scratch field on its execute request carrying the dispatch row's currently persisted scratch bytes (empty on first dispatch). The executor may write scratch in two ways — mid-dispatch by POSTing to a scratch HTTP callback route (paralleling the executor protocol's existing attributes incremental-writeback HTTP callback), or by attaching scratch bytes to the settling outcome (success, error, or park; the await-async-callback outcome carries no scratch since it does not settle the dispatch). Both writes persist on the dispatch row. The bytes are opaque to rimsky — the inertness invariant (`concept:inertness` / `@blessed-invariant 21`) extends to scratch — and scratch carries forward to a successor dispatch of the same node only via the three recovery enqueue paths that stamp `prior_dispatch_id` on the new row (stale-recovery, retry-after-error, recalculate, per `concept:node-run`). Normal cascade re-fires of the same node do not stamp `prior_dispatch_id` and so do not carry scratch; for that case same-node state rides the attribute carry-forward channel (per `concept:attribute`).

## Boundaries

Owns: the per-dispatch work, the four-variant outcome vocabulary, the observability protocol surface, per-dispatch executor-attached opaque scratch bytes (the executor sets scratch mid-dispatch via the scratch callback route or by attaching scratch bytes to the settling outcome); the three orthogonal dispatch deadlines (`sync_rpc_deadline`, `max_quiet_period`, `max_runtime`); the dedicated keepalive endpoint; the persistent async-callback registry. Does NOT own: dispatch routing (supervisor's job), attribute schema validation (rimsky validates at dispatch + commit), substitution (rimsky's job before dispatch), the supervisor-side stitching from terminal event to producer verb (see `terminal-resolution`), operator-decided retry/pass/give_up on the error outcome (see `error-policy`). Adjacent: `attribute`, `terminal-tag`, `parked-state`, `error-policy`, `observability`, `terminal-resolution`, `service`.

The bundled SQL-based reference store registers this protocol alongside `concept:claim-producer`. The same binary plays both roles via separate gRPC service registrations on a single endpoint. Other SQL-substrate stores can use the same pattern.

## Invariants

- Every dispatch returns exactly one Outcome (no stream).
- Every settling terminal (Success / Error / Park) carries `attributes_delta` and `tags` uniformly.
- The resume-context channel does not exist; executors carry resume state via attribute carry-forward.
- The AwaitAsyncCallback outcome registers an `async_ack_id` and the eventual settling terminal arrives via HTTP POST to `/v1/callback/{async_ack_id}`.
- The async-callback registry persists across supervisor restarts.
- The executor-template-level `sync_rpc_deadline` (default 30s, 0 = disabled) bounds sync dispatches.
- Per-node `max_quiet_period` and `max_runtime` (default 0 = disabled) bound async dispatches.
- Executors advertise `declared_tags` in `ObservabilityCapabilities`; templates validate emitted tag sets against this advertisement at registration.
