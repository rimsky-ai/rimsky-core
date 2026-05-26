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

An executor is an out-of-process service that implements the gRPC executor's server-streaming execute method plus an optional executor-observability protocol. Production-side reference implementations (an HTTP-node executor, an LLM-agent executor, and two verifier executors) live on the consumption side, outside the platform. A stub test-double executor stays in-rimsky for conformance + scenario harness use. The executor receives one execute request, streams zero-or-more heartbeat / named-event messages, and exactly one stream-close event carrying one of four outcome variants (success, error, park, await-async-callback). The park outcome carries an inner park reason from the closed two-value set `AWAIT_CALLBACK | SNOOZE` (`concept:parked-state`).

## Purpose

Executors are where actual work happens. Out-of-process gives language-portability, lets work scale independently of supervisors, and lets long-running work hand off to an async-callback channel without holding a gRPC stream open for hours.

## Boundaries

Owns: the per-dispatch work, the stream-close outcome vocabulary, the observability protocol surface, the userdata interpretation. Does NOT own: dispatch routing (supervisor's job), attribute schema validation (rimsky validates at dispatch + commit), substitution (rimsky's job before dispatch), the supervisor-side stitching from terminal event to producer verb (see `terminal-resolution`), operator-decided retry/pass/give_up on the error outcome (see `error-policy`). Adjacent: `attribute`, `named-event`, `parked-state`, `error-policy`, `observability`, `terminal-resolution`, `service`.

The bundled SQL-based reference store registers this protocol alongside `concept:claim-producer`. The same binary plays both roles via separate gRPC service registrations on a single endpoint. Future SQL-substrate stores may adopt the same pattern.

## Invariants

- Exactly one stream-close event closes the stream; the executor MUST close the stream immediately after.
- The await-async-callback outcome switches to expecting an async-callback POST keyed on the assigned ack id, with the body keyed `type` (not `kind`) — enforced by the supervisor's callback route.
- Heartbeat events refresh the node-run's last-heartbeat timestamp; cadence is executor-defined.
- The userdata schema reported via the observability capabilities call is the only place rimsky reads userdata-adjacent metadata (schema-only, not content).
- The declared-events list reported via observability is the source of truth for subscription template validation.

## Aliases and historical names

Pre-`spec:2026-05-12-nomenclature-resolution` Group E.1, the proto service carried a node- prefix on the executor name; the rename drops it. The operator/binary vocabulary always used "executor" so no operator-visible churn. The pre-E.2 wire shape exposed per-terminal messages (complete, blocked, errored, async-accepted, park-requested); post-E.2 the wire shape is a single stream-close event carrying one of the outcome variants (success, error, park, await-async-callback) with the historical blocked terminal collapsed into an error outcome bearing the `executor_blocked` error class.

## Open within this concept

- Two distinct callback hostnames (bind vs advertise) — see `tension:callback-hostname-split`.

## Notes

- Proto service renamed to drop its node- prefix per `spec:2026-05-12-nomenclature-resolution` Group E.1. Wire-event shape rewritten to channel-mechanics (a single stream-close event + an outcome variant) per Group E.2; the blocked terminal collapsed into an error outcome bearing an error-class field; the park-requested terminal renamed to the park outcome (the state-machine value `'parked'` is unchanged). The capabilities query was renamed for uniformity per Group E.11. Resolves `tension:_resolved/terminal-event-overloaded` and `tension:_resolved/async-callback-body-key`.

- 2026-05-14: the park outcome's reason is typed as the closed park-reason enum on the wire; a new reason-note field carries human annotation. The Notes section already references the prior snooze→park rename; this entry sits alongside it. Per `spec:2026-05-14-subscription-cascade-and-quality-of-life` Piece 2.
- 2026-05-19 — the bundled SQL-based reference store extends to the executor role per `spec:2026-05-19-multi-instance-template-ergonomics`.
- 2026-05-23 — Per `spec:2026-05-23-signal-taxonomy-and-policy-decoupling`: executor terminal vocabulary is the 4-variant outcome (success, error, park, await-async-callback); operator-decided retry is via the operator's `error_types:` chain over the error outcome, not an executor wire surface. Executors handle internal retry silently or via a park outcome with reason `SNOOZE`. The pre-existing Notes entry mis-listing snooze as the third outcome variant has been corrected above to park.
- 2026-05-25 — Codebase citations removed + cross-refs repaired for self-containment per spec:2026-05-25-concept-doc-self-containment.
- 2026-05-24: production-side bundled executor reference implementations (the LLM-agent executor, the HTTP-node executor, the two verifiers) moved to the consumption side, outside the platform. The stub executor stays in rimsky as test infrastructure. The cross-reference to the bundled SQL store was retargeted alongside. See `spec:2026-05-24-repo-reorganization` phase P3.
