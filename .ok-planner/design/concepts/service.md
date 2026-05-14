---
concept: service
status: as-is
aliases:
  - peer (legacy)
  - peer service (legacy)
references:
  - _discover/2026-05-10-multi-protocol-services.md
---

# Service

## Definition

An out-of-process gRPC binary that implements one or more rimsky service protocols and is orchestrated by rimsky.

## Purpose

Extensibility (third-party implementations are first-class) and modularity (reference implementations are decoupled from rimsky core). A service is the orchestrated-resource side of rimsky's runtime; rimsky itself runs the supervisor / scheduler / control-api binaries that orchestrate services.

## Boundaries

The specific service protocols are sibling concepts: `concept:executor`, `concept:claim-producer`, `concept:lifecycle-subscriber`, `concept:blob-backend`. Orchestration mechanics (dispatch, acquisition, supervisor coordination, terminal resolution) live in their own concepts: `concept:supervisor`, `concept:terminal-resolution`, `concept:auto-terminal`, `concept:orphan-reaper`.

`concept:service` owns:

- How a binary declares its protocol membership in `cfg:rimsky.yml` (the `protocols:` list per service entry).
- The `Capabilities` startup handshake (one RPC per protocol; see `concept:observability` for the discovery-cache that consumes them).
- Conformance-validation entry points (`code:cmd/rimsky-executor-conformance`, `code:cmd/rimsky-claim-producer-conformance`, `code:cmd/rimsky-blob-backend-conformance`).
- The multi-protocol composition pattern: a binary implementing N rimsky protocols uses N handler types, one per protocol interface. Method-name collisions across protocols (e.g., both `ClaimProducer.Capabilities()` and `ExecutorObservability.Capabilities()`) are resolved at the composition site, not by interface unification. Each handler implements one interface; the binary registers each separately at the gRPC server.

## Invariants

- Services are declared in `cfg:rimsky.yml` with an explicit `protocols: [...]` list per service.
- Protocol membership is advertised at startup via the per-protocol `Capabilities` RPC.
- Per-protocol conformance binaries validate compliance.
- Multi-protocol binaries use distinct Go handler types per protocol; no shared `CapabilitiesProvider` Go interface (per `spec:2026-05-12-nomenclature-resolution` E.4 — the response types are protocol-specific and the downstream code is already protocol-specific).

## Adjacent

- `concept:executor`
- `concept:claim-producer`
- `concept:lifecycle-subscriber`
- `concept:blob-backend`
- `concept:rimsky-yml`
- `concept:conformance`
- `concept:observability`
- `concept:discovery-cache`

## Notes

- Promoted as new umbrella concept per `spec:2026-05-12-nomenclature-resolution` (audit cross-layer #18). Replaces the colloquial "peer" framing, which implied peer-to-peer equivalence that doesn't match rimsky's orchestrator-to-orchestrated relationship.
