// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package claimproducer

import "context"

// ClaimProducer is the Go interface for the ClaimProducer service
// protocol. See spec §2 for wire shapes and invariants.
//
// Four runtime verbs (Open / Commit / Abandon / Release) plus one
// startup-handshake verb (Capabilities). Each verb carries a
// rimsky-generated claim_id. The producer may key its internal state
// by claim_id, by address, by both, or by neither.
//
// @blessed-invariant 9a: Lock state lives only in the persistence layer.
// No ClaimProducer implementation persists lock state; producers may
// persist data state (items-table flips, staging metadata), but the
// question "is anyone holding lock X" is answered exclusively by
// rimsky_claim_handle on the rimsky side.
//
// @blessed-invariant 9b: ClaimProducer implementations MUST NOT
// internally serialize on lock-shaped predicates. The reader-lease
// serialization pattern is forbidden for staged_async; honest support
// requires snapshot delegation or native MVCC pass-through.
type ClaimProducer interface {
	// Name returns the operator-configured producer name (matches
	// claim_producers.<name> in rimsky.yml). Rimsky-side identifier;
	// not transported over the wire.
	Name() string

	// Capabilities reports the producer's advertised capability struct.
	// Called once per producer per rimsky process at startup; rimsky
	// caches the result and validates it against the operator's
	// declared envelope (must be subset).
	Capabilities(ctx context.Context) (Capabilities, error)

	// Open creates whatever producer-internal state the (intent,
	// realized write_semantics) combination requires (staging area,
	// snapshot, MVCC transaction, picked-item flip, or nothing) and
	// returns the producer-supplied address the executor will use on
	// the data path.
	//
	// Producers that may have nothing to give right now (e.g. an empty
	// items-table queue) return OpenOutcome{Available: false}. Rimsky
	// rolls back the dispatch tx and may retry on the next scheduler
	// tick. Producer-side faults flow as gRPC error status codes (or
	// wrapped Go errors), not as Available: false.
	Open(ctx context.Context, claimID ClaimID, spec ClaimSpec) (OpenOutcome, error)

	// Commit signals that the consumer of the claim succeeded. The
	// producer decides what to do with its own state per its own
	// configuration.
	Commit(ctx context.Context, claimID ClaimID, scope []byte, address []byte) error

	// Abandon signals that the consumer of the claim failed. The
	// producer decides what to do with its own state per its own
	// configuration. address may be empty when Open's response was lost.
	Abandon(ctx context.Context, claimID ClaimID, scope []byte, address []byte) error

	// Release tears down producer-internal read state created at Open.
	// Sync producers typically no-op.
	Release(ctx context.Context, claimID ClaimID, scope []byte, address []byte) error
}
