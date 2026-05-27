// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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
