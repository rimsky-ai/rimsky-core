// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// Package remote is the gRPC client that satisfies the rimsky-side
// foundation/locks.ClaimProducer and foundation/locks.LifecycleSubscriber
// interfaces by translating each verb / event to a wire RPC against a
// standard producer-service binary.
//
// Per spec docs/specs/2026-05-04-service-protocol-contract.md §2 / §3.
// This is the only concrete ClaimProducer / LifecycleSubscriber
// implementation that ships in the rimsky module — every producer-service
// runs in its own process and rimsky reaches it via this client.
package remote
