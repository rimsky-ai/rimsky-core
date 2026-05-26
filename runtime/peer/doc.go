// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// Package peer is the gRPC client that satisfies the rimsky-side
// foundation/locks.ClaimProducer and foundation/locks.LifecycleSubscriber
// interfaces by translating each verb / event to a wire RPC against a
// standard producer-service binary.
//
// The package was renamed from `runtime/remote` to `runtime/peer` per
// spec `2026-05-24-repo-reorganization-design` phase P2: the name
// "remote" implied an externally-facing surface, but the package is
// rimsky-internal infrastructure tightly coupled to `concept:supervisor`,
// `concept:terminal-resolution`, and `concept:discovery-cache`. "Peer"
// matches the `concept:service` vocabulary better.
//
// Per spec docs/specs/2026-05-04-service-protocol-contract.md §2 / §3.
// This is the only concrete ClaimProducer / LifecycleSubscriber
// implementation that ships in the rimsky module — every producer-service
// runs in its own process and rimsky reaches it via this client.
package peer
