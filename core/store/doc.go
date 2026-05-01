// Package store defines the rimsky-side store contract. Per spec
// docs/specs/2026-04-27-stores-redesign-v3-design.md as amended by
// docs/specs/2026-04-30-stores-protocol-cleanup-design.md (store-
// internal-vocabulary excision).
//
// In v3 the standard store implementations live in standalone binaries
// under stores/ and rimsky talks to them via the gRPC client in
// core/store/remote/. This package owns:
//
//   - The Store interface (interface.go) — four runtime verbs plus
//     Capabilities, every verb keyed on a rimsky-generated claim_id.
//   - Value types (types.go) — ClaimID, ClaimSpec, ClaimResult,
//     OpenOutcome, NamedLockSpec, Capabilities, WriteSemantics.
//   - Registry (registry.go) — a simple name→Store map populated
//     externally by the rimsky cmd binaries at startup.
//   - rimsky_lock_holders postgres helpers (lockholders.go) — the
//     bookkeeping table that backs invariant 9a.
//   - Pure rimsky-side comparators (conflict.go) — ModeCoexists for
//     the C3.1 mode-coexistence matrix; RegionsByteEqual for v3's
//     byte-equal region conflict (per spec §7.7).
//
// Concrete implementations:
//
//   - core/store/remote/    — the gRPC client; the only concrete Store
//     in the rimsky module.
//   - core/store/storetest/ — an in-Go fake for unit tests where the
//     wire isn't relevant.
//   - stores/<kind>/        — standalone store-service binaries
//     (filesystem, postgres, stub) that implement the wire protocol.
//
// # Two primitives
//
//   - Claim — store-bound. ClaimSpec carries (StoreName, Selector,
//     Intent, Alias). The store parses Selector and decides what it
//     means (regional access vs. an items-table queue convention).
//
//   - Named lock — store-independent. NamedLockSpec carries (Name) only.
//     Limit lives in operator config.
//
// Both are persisted as rows in rimsky_lock_holders but the two specs
// are distinct types with no common interface.
//
// # Four protocol verbs (spec §4.1)
//
//   - Open(ctx, claim_id, ClaimSpec) → OpenOutcome
//   - Commit(ctx, claim_id, region, address)
//   - Abandon(ctx, claim_id, region, address)
//   - Release(ctx, claim_id, region, address)
//
// Plus Capabilities(ctx) for the startup handshake. Store
// disposition (what Commit / Abandon mean for the store's own
// state) is governed by per-store config; rimsky carries only
// the success/failure binary.
//
// # write_semantics (spec §4.8)
//
// Per-store-service: direct | staged_blocking | staged_async. Baked
// into the store-service's own config; rimsky validates strict equality
// against the operator-declared block in stores.yml.
package store
