// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// ClaimProducer interface — the rimsky-side contract every
// claim-producer implementation satisfies. Per spec
// docs/specs/2026-05-04-service-protocol-contract.md §2.
//
// Standard producer implementations live in standalone binaries under
// stores/ and rimsky talks to them via the gRPC client in
// runtime/peer/. Type assertions to a concrete producer
// from any rimsky package are forbidden — the ClaimProducer interface is
// the only contract.
//
// Lifecycle events live in a separate LifecycleSubscriber interface
// (lifecycle.go in this package; spec §3). A producer binary that wishes
// to react to control-plane events implements both interfaces and
// declares both protocols in rimsky.yml.
//
// Per the layer-crystallization design (2026-05-04), the canonical Go
// interface lives in github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer;
// foundation/locks.ClaimProducer is a Go type alias of that interface so
// rimsky-internal callers and external implementers share one nominal
// type. External authors should import protocols/claimproducer; rimsky-
// internal code may use either.

package locks

import (
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
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
//	rimsky_claim_handles (managed via foundation/persistence/<driver>/
//	claim_handles.go).
//
// @blessed-invariant 9b: ClaimProducers do not internally serialize on
// lock-shaped predicates. The reader-lease serialization pattern is not
// a valid implementation choice for staged_async; honest support
// requires snapshot delegation or native MVCC pass-through.
//
// @blessed-invariant 4b: single-writer-per-scope; overlap is producer-
// defined, byte-equal as the trivial default. The acquisition predicate
// rejects a new claim_handle row that conflicts with an existing held
// row for the same producer-claimed scope. Producers that advertise
// `SupportsScopesConflict: true` may define overlap via `ScopesConflict`
// (e.g. range-overlap, prefix-containment, MVCC snapshot overlap);
// producers that don't advertise default to byte-equal comparison of
// `rimsky_claim_handles.claim_scope_data`. Either way, two writers cannot
// simultaneously hold the same logical claim-scope.
//
// @concept: claim-producer
type ClaimProducer = claimproducer.ClaimProducer
