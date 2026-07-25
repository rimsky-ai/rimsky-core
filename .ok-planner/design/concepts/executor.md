---
concept: executor
status: as-is
aliases: []
---

# Executor

## What it is

An executor implements the executor protocol's unary execute operation plus an optional observability protocol. Implementations come in two forms — in-process handlers registered with an in-process handler registry, and out-of-process services — and the outcome vocabulary (execute, the outcome variants) is identical across both, but the observability handshake is not: out-of-process services advertise capabilities through the gRPC observability handshake, while in-process handlers advertise capabilities directly into the discovery cache at registration and never receive a handshake. The executor receives one execute request and returns exactly one outcome from the closed family of settling outcomes plus an async-callback deferral. The deferral defers the verdict to a later callback against the supervisor whose body settles the dispatch with one of the settling outcomes. Park outcomes carry exactly a required resume-at, scratch, and tags (per `concept:parked-state`).

## Purpose

Executors are where actual work happens. Out-of-process executors give language-portability, scale-independence, and async-callback handoff for long-running work. In-process executors deliver utility-node primitives (counters, gates, simple computations) without the deploy / image / IPC overhead, sharing the same protocol surface so the dispatch path treats both forms uniformly.

## Scratch

Executors carry opaque per-dispatch scratch bytes via the protocol. Each dispatch surfaces the previously persisted scratch (empty on first dispatch); the executor writes scratch by attaching it to the settling outcome — there is no mid-dispatch scratch channel. The bytes are opaque to rimsky — the inertness discipline extends to scratch (see `concept:inertness`) — and scratch carries forward to a successor dispatch of the same node whenever that dispatch supersedes a prior one, including in-place retry, stale recovery, and cascade-driven recalculate; a fresh dispatch with no predecessor starts with empty scratch. Recovery-path semantics live with `concept:node-run`.

## Boundaries

Owns: the per-dispatch work, the outcome vocabulary, per-dispatch executor-attached opaque scratch bytes; the orthogonal dispatch deadlines (one bounding the sync RPC, one bounding the quiet period, one bounding total runtime); the dedicated keepalive and incremental attribute-writeback channels; the persistent async-callback registry. Does NOT own: dispatch routing (supervisor's job), attribute schema validation (rimsky validates at dispatch + commit), substitution (rimsky's job before dispatch), the supervisor-side stitching from terminal event to producer verb (see `terminal-resolution`), operator-decided retry/pass/give_up on the error outcome (see `error-policy`), the observability protocol surface — handshake, capability advertisement mechanics, refresh-loop policy (see `observability`; this doc describes only what the executor advertises through it). Adjacent: `attribute`, `terminal-tag`, `parked-state`, `error-policy`, `observability`, `terminal-resolution`, `service`, `peer-auth` (authenticates the dispatch and its async-callback return leg).

## Invariants

- Every dispatch returns exactly one outcome (no stream).
- Every settling terminal carries tags uniformly; an attributes delta is carried only by the run-terminating verdicts (Success and Error) — Park's shape carries scratch and tags, not an attributes delta.
- The resume-context channel does not exist; executors carry resume state across a park-and-resume cycle via scratch attached to the Park outcome, not via attribute carry-forward.
- The async-callback deferral registers a callback identifier and the eventual settling terminal arrives via a later callback addressed to that identifier. The identifier (`async_ack_id`) is purely a correlation key — which run a callback settles — never an authenticator: authentication of the return leg is the mTLS peer identity under `peer_auth: mtls` and the trusted-subnet assumption under `none` (see `concept:peer-auth`). The executor-chosen `async_ack_id` is never treated as a credential — an executor-chosen id is not a credential the trusted side issued. The incremental per-run callbacks (keepalive and attribute writeback) additionally require the dispatch's cancel token presented as a bearer credential — a supervisor-issued per-dispatch secret binding the caller to the run it claims to report on — layered under whatever transport-level peer-auth posture is configured.
- The async-callback registry persists across supervisor restarts. Because the registry's durability is only useful if the callback eventually lands, an executor that fails to deliver its async-callback POST must retry with backoff rather than dropping the outcome after a single failed attempt.
- A per-node sync RPC deadline bounds sync dispatches.
- Per-node quiet-period and runtime deadlines bound async dispatches.
- Executors advertise their declared tag set as part of their observability capabilities; registration rejects subscriptions that reference tags outside this advertisement, and the terminal handler enforces emitted tag sets against it at settle, rejecting an undeclared tag as a protocol violation (see `concept:terminal-tag`).
