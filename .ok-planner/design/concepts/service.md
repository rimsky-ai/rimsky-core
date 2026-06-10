---
concept: service
status: as-is
aliases: []
---

# Service

## Definition

An out-of-process gRPC binary that implements one or more rimsky service protocols and is orchestrated by rimsky.

## Purpose

Extensibility (third-party implementations are first-class) and modularity (reference implementations are decoupled from rimsky core). A service is the orchestrated-resource side of rimsky's runtime; rimsky itself runs the supervisor / scheduler / control-api binaries that orchestrate services.

## Boundaries

The specific service protocols are sibling concepts: `concept:executor`, `concept:claim-producer`, `concept:lifecycle-subscriber`, `concept:blob-backend`. Orchestration mechanics (dispatch, acquisition, supervisor coordination, terminal resolution) live in their own concepts: `concept:supervisor`, `concept:terminal-resolution`, `concept:auto-terminal`, `concept:orphan-reaper`.

`concept:service` owns:

- How a binary declares its protocol membership in the unified config (the per-service-entry protocol-membership list; see `concept:rimsky-yml`).
- The capabilities startup handshake (one handshake call per protocol; see `concept:observability` for the discovery-cache that consumes them).
- Conformance-validation entry points (the `rimsky conformance <protocol>` subcommand family in the single binary, not standalone per-protocol binaries; see `concept:conformance`).
- The multi-protocol composition pattern: a binary implementing N rimsky protocols uses N distinct handlers, one per protocol. Method-name collisions across protocols (e.g., a capabilities query on both the claim-producer and the executor-observability protocol) are resolved at the composition site, not by collapsing the protocols into one. Each handler implements one protocol; the binary registers each separately at the gRPC server.

## Invariants

- Services are declared in the unified config with an explicit protocol-membership list per service.
- Protocol membership is advertised at startup via the per-protocol capabilities query.
- Conformance is validated by the per-protocol `rimsky conformance <protocol>` subcommands shipped in the single binary (see `concept:conformance`).
- Multi-protocol binaries use a distinct handler per protocol; there is no shared capabilities-provider abstraction across protocols (response shapes are protocol-specific and the downstream code is already protocol-specific).

## Adjacent

- `concept:executor`
- `concept:claim-producer`
- `concept:lifecycle-subscriber`
- `concept:blob-backend`
- `concept:publisher` (the umbrella concept; `concept:sensor` is one class of publisher)
- `concept:rimsky-yml`
- `concept:conformance`
- `concept:observability`
- `concept:discovery-cache`
