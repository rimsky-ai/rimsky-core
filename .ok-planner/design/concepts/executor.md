---
concept: executor
status: as-is
aliases: []
---

# Executor

## What it is

An executor implements the executor protocol's unary execute operation plus an optional observability protocol. Implementations come in two forms — in-process handlers registered with the dispatch pool, and out-of-process services — and the protocol surface (execute, the outcome variants, the observability handshake) is identical across both. The executor receives one execute request and returns exactly one outcome from the closed family of settling outcomes plus an async-callback deferral. The deferral defers the verdict to a later callback against the supervisor whose body settles the dispatch with one of the settling outcomes. Park outcomes carry an inner park reason (per `concept:parked-state`).

## Purpose

Executors are where actual work happens. Out-of-process executors give language-portability, scale-independence, and async-callback handoff for long-running work. In-process executors deliver utility-node primitives (counters, gates, simple computations) without the deploy / image / IPC overhead, sharing the same protocol surface so the dispatch path treats both forms uniformly.

## Scratch

Executors carry opaque per-dispatch scratch bytes via the protocol. Each dispatch surfaces the previously persisted scratch (empty on first dispatch); the executor may write scratch mid-dispatch via a scratch callback channel or by attaching it to the settling outcome. Both writes persist against the dispatch. The bytes are opaque to rimsky — the inertness discipline extends to scratch (see `concept:inertness`) — and scratch carries forward to a successor dispatch of the same node only via recovery enqueue paths, not via normal cascade re-fires. For normal re-fires, same-node state rides the attribute carry-forward channel (per `concept:attribute`); recovery-path semantics live with `concept:node-run`.

## Boundaries

Owns: the per-dispatch work, the outcome vocabulary, the observability protocol surface, per-dispatch executor-attached opaque scratch bytes; the orthogonal dispatch deadlines (one bounding the sync RPC, one bounding the quiet period, one bounding total runtime); the dedicated keepalive channel; the persistent async-callback registry. Does NOT own: dispatch routing (supervisor's job), attribute schema validation (rimsky validates at dispatch + commit), substitution (rimsky's job before dispatch), the supervisor-side stitching from terminal event to producer verb (see `terminal-resolution`), operator-decided retry/pass/give_up on the error outcome (see `error-policy`). Adjacent: `attribute`, `terminal-tag`, `parked-state`, `error-policy`, `observability`, `terminal-resolution`, `service`, `peer-auth` (authenticates the dispatch and its async-callback return leg).

## Invariants

- Every dispatch returns exactly one outcome (no stream).
- Every settling terminal carries an attributes delta and tags uniformly.
- The resume-context channel does not exist; executors carry resume state via attribute carry-forward.
- The async-callback deferral registers a callback identifier and the eventual settling terminal arrives via a later callback addressed to that identifier. The identifier (`async_ack_id`) is purely a correlation key — which run a callback settles — never an authenticator: authentication of the return leg is the mTLS peer identity under `peer_auth: mtls` and the trusted-subnet assumption under `none` (see `concept:peer-auth`). The former per-call `supervisor_id:node_run_id` run-token, and treating the executor-chosen `async_ack_id` as a credential, are both gone — per-call auth on the return leg with nothing authenticating the outbound leg was security theater, and an executor-chosen id is not a credential the trusted side issued.
- The async-callback registry persists across supervisor restarts.
- An executor-template-level sync RPC deadline bounds sync dispatches.
- Per-node quiet-period and runtime deadlines bound async dispatches.
- Executors advertise their declared tag set as part of their observability capabilities; templates validate emitted tag sets against this advertisement at registration.
