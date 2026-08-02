---
concept: service
---

# Service

## What it is

A rimsky-orchestrated implementation of one or more of rimsky's service protocols, running either as an out-of-process binary or as an in-process handler within the rimsky all-in-one process. Both forms are dispatched via the same protocol surface.

## Purpose

Extensibility (third-party implementations are first-class) and modularity (reference implementations are decoupled from rimsky core). A service is the orchestrated-resource side of rimsky's runtime; rimsky itself runs the supervisor / scheduler / control-api binaries that orchestrate services.

## Boundaries

The specific service protocols are sibling concepts: `concept:executor`, `concept:claim-producer`, `concept:lifecycle-subscriber`, `concept:publisher`. Orchestration mechanics (dispatch, acquisition, supervisor coordination, terminal resolution) live in their own concepts: `concept:supervisor`, `concept:terminal-resolution`, `concept:auto-terminal`, `concept:orphan-reaper`.

`concept:service` owns:

- How a service declares its protocol membership. For an out-of-process binary that is the unified config (the per-service-entry protocol-membership list; see `concept:rimsky-yml`); for a bundled in-process handler it is the sibling path of programmatic registration through the bundled registration entrypoint (see `decision:bundled-registry-entrypoint`).
- The capabilities startup handshake (one handshake call per protocol; see `concept:observability` for the handshake mechanics and `concept:discovery-cache` for the cache that consumes them).
- Conformance-validation entry points (the per-protocol conformance subcommands shipped in the single binary, not standalone per-protocol binaries; see `concept:conformance`).
- The multi-protocol composition pattern: a binary implementing N rimsky protocols uses N distinct handlers, one per protocol. Method-name collisions across protocols (e.g., a capabilities query on both the claim-producer and the executor-observability protocol) are resolved at the composition site, not by collapsing the protocols into one. Each handler implements one protocol; the binary registers each separately with its serving stack.

## Invariants

- Out-of-process services are declared in the unified config with an explicit protocol-membership list per service. In-process bundled services register their protocol membership programmatically via the bundled registration entrypoint — an equivalent declaration on a different surface.
- Protocol membership is advertised for out-of-process services at startup via the per-protocol capabilities query; in-process handlers advertise their protocol membership and capabilities (schema, tags, declared error classes) through the same registration entrypoint, which knows each handler's capabilities by construction.
- Conformance is validated by the per-protocol conformance subcommands shipped in the single binary (see `concept:conformance`). For executor and claim-producer, conformance runs against the standalone gRPC surface each bundled service exposes; the in-process handler and the gRPC-wrapped handler share the same protocol semantics by construction (same handler package, same code paths), so a passing conformance run on the standalone image guarantees the in-process handler's semantics too. Lifecycle-subscriber has no dedicated conformance subcommand. (Blob-backend also ships a conformance suite, but it is a persistence-layer abstraction, not a service protocol — see `concept:blob-backend`.)
- Multi-protocol binaries use a distinct handler per protocol; there is no shared capabilities-provider abstraction across protocols (response shapes are protocol-specific and the downstream code is already protocol-specific).
- An operator-deployed standing service participates in the internal service↔service trust boundary: under `peer_auth: none` its dials are plaintext on a trusted subnet, and under `peer_auth: mtls` it enrolls with a `service:enroll`-bearing api-key to obtain a short-lived certificate and both peers of every dial mutually authenticate (see `concept:peer-auth`). This is orthogonal to the per-peer `tls` config key that verifies a single peer's server certificate against system roots (see `decision:peer-tls-enforcement`).
- Every bundled out-of-process service binary resolves its serving port through one shared precedence: an agent-assigned port environment variable first, falling back to the service's own port variable (or its unified-config value) when unset, falling back to a built-in default when neither is set. The agent-assigned port variable is the port-discovery contract a `concept:host-agent` uses to late-bind a bundled binary as a local, per-invocation service; a binary that ignores it cannot be late-bound — the agent's readiness poll against the port it chose simply never observes a listener.

## Adjacent

- `concept:executor`
- `concept:claim-producer`
- `concept:lifecycle-subscriber`
- `concept:publisher` (the umbrella concept; `concept:sensor` is one class of publisher)
- `concept:rimsky-yml`
- `concept:conformance`
- `concept:observability`
- `concept:discovery-cache`
- `concept:peer-auth`
