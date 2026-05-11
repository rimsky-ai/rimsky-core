---
concept: observability
status: as-is
aliases: []
references:
  - _discover/2026-05-10-observability-optional-protocols.md
  - _discover/observability-cascade-graph-endpoint.md
  - _discover/observability-handshake-discovery-cache.md
---

# Observability

## What it is

A multi-surface observability system: (a) two optional gRPC protocols per peer (`ExecutorObservability`, `StoreObservability`) with `Capabilities`/`GetTrace`/`StreamTrace`; (b) rimsky-side HTTP routes mounted on the control-api (`/observability/*`, `/events`, `/frames`, `/nodes/{instance}/{type}`, `/dispatches`, ...); (c) a startup handshake (`modeling/observability/handshake.go`) that probes each peer's observability endpoint in parallel and populates the in-memory `Discovery` cache.

## Purpose

Operators need to see what's running, what's wedged, and what each peer reports about itself. A protocol-side observability surface keeps the operator dashboard project-agnostic; a control-api side cascade-graph endpoint lets it query rimsky's own state.

## Boundaries

Owns: the optional peer protocols, the handshake/refresh loop, the `Discovery` cache, the HTTP routes, the `userdata_schema` validation site, the `declared_events` cross-check site. Does NOT own: per-event audit (see `event-log`), executor protocol (see `executor`), claim-producer protocol (see `claim-producer`). Adjacent: `executor`, `claim-producer`, `event-log`, `named-event`, and the cascade-graph endpoint (a sub-endpoint owned by this concept that surfaces rimsky's own runtime state to the operator dashboard).

## Invariants

- The handshake is best-effort: unreachable peers are recorded as `Unreachable` and never abort startup.
- Per-peer `userdata_schema` validates at template registration AND at dispatch post-merge/post-substitution.
- `declared_events` is the source of truth for `on_event` template-registration cross-check; runtime treats unknown event names as no-ops if the executor was unreachable at registration.
- All observability HTTP handlers run inside `inTx` (a short fresh transaction per handler).

## Aliases and historical names

None live.

## Open within this concept

- `userdata_schema` placement on the observability protocol (read by rimsky) sits in tension with `@blessed-invariant 11` opacity — schema-only is a sanctioned exception but not explicitly named in CLAUDE.md — see `tensions/userdata-schema-as-opacity-exception.md`.

