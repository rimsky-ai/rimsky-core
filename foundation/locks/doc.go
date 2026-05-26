// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// Package locks defines the rimsky-side claim-producer contract. Per spec
// docs/history/2026-04-27-stores-redesign-v3-design.md as amended by
// docs/history/2026-04-30-stores-protocol-cleanup-design.md (store-
// internal-vocabulary excision) and the layer-crystallization Phase 2
// rename, the Go interface is `ClaimProducer`.
//
// Standard claim-producer implementations live in standalone binaries
// under stores/ and rimsky talks to them via the gRPC client in
// runtime/peer/. This package owns:
//
//   - The ClaimProducer interface (interface.go) — four runtime verbs plus
//     Capabilities, every verb keyed on a rimsky-generated claim_id.
//   - Value types (types.go) — ClaimID, ClaimSpec, ClaimResult,
//     OpenOutcome, NamedLockSpec, Capabilities, WriteSemantics.
//   - Registry (registry.go) — a simple name→ClaimProducer map populated
//     externally by the rimsky cmd binaries at startup.
//   - rimsky_claim_handles postgres helpers (lockholders.go) — the
//     bookkeeping table that backs invariant 9a.
//   - Pure rimsky-side comparators (conflict.go) — ModeCoexists for
//     the C3.1 mode-coexistence matrix; ClaimScopesByteEqual for v3's
//     byte-equal scope conflict (per spec §7.7).
//
// Concrete implementations:
//
//   - runtime/peer/ — the gRPC client; the only
//     concrete ClaimProducer in the rimsky module.
//   - foundation/locks/storetest/    — an in-Go fake for unit tests
//     where the wire isn't relevant.
//   - stores/<kind>/                 — standalone claim-producer
//     binaries (filesystem, postgres, stub) that implement the wire
//     protocol.
//
// # Two primitives
//
//   - Claim — producer-bound. ClaimSpec carries (ProducerName, Selector,
//     Intent, Alias). The producer parses Selector and decides what it
//     means (scoped access vs. an items-table queue convention).
//
//   - Named lock — producer-independent. NamedLockSpec carries (Name) only.
//     Limit lives in operator config.
//
// Both are persisted as rows in rimsky_claim_handles but the two specs
// are distinct types with no common interface.
//
// # Four protocol verbs (spec §4.1)
//
//   - Open(ctx, claim_id, ClaimSpec) → OpenOutcome
//   - Commit(ctx, claim_id, scope, address)
//   - Abandon(ctx, claim_id, scope, address)
//   - Release(ctx, claim_id, scope, address)
//
// Plus Capabilities(ctx) for the startup handshake. Producer
// disposition (what Commit / Abandon mean for the producer's own
// state) is governed by per-producer config; rimsky carries only
// the success/failure binary.
//
// # write_semantics (spec §4.8)
//
// Per-producer enum: sync | staged_async | blocking_async | read_only.
// Producers advertise an `allowed` SET via `Capabilities`; the
// operator-declared `write_semantics_allowed` envelope in rimsky.yml
// MUST be a subset. Realized semantics are returned per claim on `Open`.
package locks
