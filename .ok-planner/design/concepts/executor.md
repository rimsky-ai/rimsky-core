---
concept: executor
status: as-is
aliases: []
---

# Executor

## What it is

An executor is an out-of-process service that implements the gRPC executor's server-streaming execute method plus an optional executor-observability protocol. Production-side reference implementations (an HTTP-node executor, an LLM-agent executor, and two verifier executors) live on the consumption side, outside the platform. The stub test-double executor is the only in-rimsky implementation, used by conformance and the scenario harness. The executor receives one execute request, streams zero-or-more heartbeat / named-event messages, and exactly one stream-close event carrying one of four outcome variants (success, error, park, await-async-callback). The park outcome carries an inner park reason from the closed two-value set `AWAIT_CALLBACK | SNOOZE` (`concept:parked-state`).

## Purpose

Executors are where actual work happens. Out-of-process gives language-portability, lets work scale independently of supervisors, and lets long-running work hand off to an async-callback channel without holding a gRPC stream open for hours.

## Boundaries

Owns: the per-dispatch work, the stream-close outcome vocabulary, the observability protocol surface, the userdata interpretation. Does NOT own: dispatch routing (supervisor's job), attribute schema validation (rimsky validates at dispatch + commit), substitution (rimsky's job before dispatch), the supervisor-side stitching from terminal event to producer verb (see `terminal-resolution`), operator-decided retry/pass/give_up on the error outcome (see `error-policy`). Adjacent: `attribute`, `named-event`, `parked-state`, `error-policy`, `observability`, `terminal-resolution`, `service`.

The bundled SQL-based reference store registers this protocol alongside `concept:claim-producer`. The same binary plays both roles via separate gRPC service registrations on a single endpoint. Other SQL-substrate stores can use the same pattern.

## Invariants

- Exactly one stream-close event closes the stream; the executor MUST close the stream immediately after.
- The await-async-callback outcome switches to expecting an async-callback POST keyed on the assigned ack id; that POST body carries exactly one outcome key — `success`, `error`, or `park` — enforced by the supervisor's callback route, which rejects any other body shape.
- Heartbeat events refresh the node-run's last-heartbeat timestamp; cadence is executor-defined.
- The userdata schema reported via the observability capabilities call is the only place rimsky reads userdata-adjacent metadata (schema-only, not content).
- The declared-events list reported via observability is the source of truth for subscription template validation.
