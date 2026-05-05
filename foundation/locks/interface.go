// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// ClaimProducer interface — the rimsky-side contract every
// claim-producer implementation satisfies. Per spec
// docs/specs/2026-05-04-service-protocol-contract.md §2.
//
// Standard producer implementations live in standalone binaries under
// stores/ and rimsky talks to them via the gRPC client in
// foundation/integration/remote/. Type assertions to a concrete producer
// from any rimsky package are forbidden — the ClaimProducer interface is
// the only contract.
//
// Lifecycle events live in a separate LifecycleSubscriber interface
// (lifecycle.go in this package; spec §3). A producer binary that wishes
// to react to control-plane events implements both interfaces and
// declares both protocols in rimsky.yml.
//
// Per the layer-crystallization design (2026-05-04), the canonical Go
// interface lives in github.com/fallguy/rimsky/protocols/claimproducer;
// foundation/locks.ClaimProducer is a Go type alias of that interface so
// rimsky-internal callers and external implementers share one nominal
// type. External authors should import protocols/claimproducer; rimsky-
// internal code may use either.

package locks

import (
	"github.com/fallguy/rimsky/protocols/claimproducer"
)

// ClaimProducer is the universal interface every claim-producer
// implementation satisfies.
//
// Four runtime verbs (Open / Commit / Abandon / Release) plus one
// startup-handshake verb (Capabilities). Each verb carries a
// rimsky-generated claim_id. The producer may key its internal state by
// claim_id, by address, by both, or by neither.
//
// Claim disposition (what Commit / Abandon mean for the producer's own
// state — publish staging, delete an items-table row, release-to-back,
// etc.) is governed entirely by per-producer config. Rimsky carries only
// the success/failure binary: success → Commit; failure → Abandon.
//
// @blessed-invariant 9a: Lock state lives only in the persistence layer.
//
//	No ClaimProducer implementation persists lock state. Producers may
//	persist *data* state (e.g. items-table flips, staging metadata), but
//	the question "is anyone holding lock X" is answered exclusively by
//	rimsky_claim_handle (managed via foundation/persistence/<driver>/
//	lock_holders.go).
//
// @blessed-invariant 9b: ClaimProducers do not internally serialize on
// lock-shaped predicates. The reader-lease serialization pattern is not
// a valid implementation choice for staged_async; honest support
// requires snapshot delegation or native MVCC pass-through.
type ClaimProducer = claimproducer.ClaimProducer

// Store is a deprecated alias for ClaimProducer kept for transitional
// compatibility during the layer-crystallization rollout. New code MUST
// use ClaimProducer directly.
//
// Deprecated: use ClaimProducer.
type Store = ClaimProducer
