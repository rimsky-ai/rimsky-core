---
concept: service
status: as-is
aliases:
  - peer (legacy)
  - peer service (legacy)
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
- Conformance-validation entry points (the per-protocol conformance binaries; see `concept:conformance`).
- The multi-protocol composition pattern: a binary implementing N rimsky protocols uses N distinct handlers, one per protocol. Method-name collisions across protocols (e.g., a capabilities query on both the claim-producer and the executor-observability protocol) are resolved at the composition site, not by collapsing the protocols into one. Each handler implements one protocol; the binary registers each separately at the gRPC server.

## Invariants

- Services are declared in the unified config with an explicit protocol-membership list per service.
- Protocol membership is advertised at startup via the per-protocol capabilities query.
- Per-protocol conformance binaries validate compliance.
- Multi-protocol binaries use a distinct handler per protocol; there is no shared capabilities-provider abstraction across protocols (per `spec:2026-05-12-nomenclature-resolution` E.4 — the response shapes are protocol-specific and the downstream code is already protocol-specific).

## Adjacent

- `concept:executor`
- `concept:claim-producer`
- `concept:lifecycle-subscriber`
- `concept:blob-backend`
- `concept:publisher` (the umbrella concept added by the 2026-05-17 publisher / publisher-subscription unification; `concept:sensor` is one class of publisher)
- `concept:rimsky-yml`
- `concept:conformance`
- `concept:observability`
- `concept:discovery-cache`

## Notes

- Promoted as new umbrella concept per `spec:2026-05-12-nomenclature-resolution` (audit cross-layer #18). Replaces the colloquial "peer" framing, which implied peer-to-peer equivalence that doesn't match rimsky's orchestrator-to-orchestrated relationship.
- 2026-05-15: **`publisher` joins as a fifth service kind**. Publishers are first-class in-instance services declared in the unified config's publishers block; sensors are one class within that umbrella (post-2026-05-17 publisher / publisher-subscription unification). They monitor external state and push messages to rimsky's control-api. Same deployment model as executor / claim-producer / lifecycle-subscriber — out-of-process gRPC binaries with a capabilities startup handshake. Conformance is validated by the publisher conformance binary. See `concept:publisher`.
- 2026-05-25 — Codebase citations removed + cross-refs repaired for self-containment per spec:2026-05-25-concept-doc-self-containment.
- [2026-05-24] The host-agent-proxy is a multi-protocol service that bridges rimsky-side protocols to dev-machine binaries declared per-instance. It follows the existing multi-protocol composition pattern (one binary, N handler types) and inherits all of `concept:service`'s invariants. Per spec 2026-05-24-host-agent-and-proxy-design.
