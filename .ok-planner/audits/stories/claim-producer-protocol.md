---
audit: claim-producer-protocol
artifact: story:claim-producer-protocol
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:28:18Z
---

# A custom producer implements Capabilities/Open/Commit/Abandon/Release with a chosen write-semantics

Supported. `claim_producer.proto` defines the capabilities-advertisement RPC plus the four verbs (Open/Commit/Abandon/Release), a `WriteSemantics` enum with all four values the story names (sync, staged_async, blocking_async, read_only), and per-Open realized-semantics reporting checked against the advertised envelope. Two shipped producers realize two different semantics in production (the filesystem producer as sync, the postgres producer defaulting to staged_async); the coexistence rule for all four semantics (`lib/foundation/locks/conflict.go::ModeCoexists`) is unit-tested across all four. The runtime's acquisition path (`lib/runtime/runner_acquire_claims.go` and related) calls Capabilities once at startup via the discovery-cache handshake, drives Open inside the acquisition transaction, and threads the returned address/scope into the claim handles that back executor dispatch; a durable per-producer outbox delivers terminal verbs (Commit/Abandon/Release) at-least-once. An out-of-process gRPC path and an in-process bundled-handler path both exist and are dispatched through the same protocol surface.
