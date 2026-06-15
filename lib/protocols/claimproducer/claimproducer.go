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
// rimsky_claim_handles on the rimsky side.
//
// @blessed-invariant 9b: ClaimProducer implementations MUST NOT
// internally serialize on lock-shaped predicates. The reader-lease
// serialization pattern is forbidden for staged_async; honest support
// requires snapshot delegation or native MVCC pass-through.
type ClaimProducer interface {
	// @agent-contract: Name returns the operator-configured producer name
	// (matches claim_producers.<name> in rimsky.yml). Rimsky-side identifier;
	// not transported over the wire.
	Name() string

	// @agent-contract: Capabilities reports the producer's advertised
	// capability struct. Called once per producer per rimsky process at
	// startup; rimsky caches the result and validates it against the
	// operator's declared envelope (must be subset).
	Capabilities(ctx context.Context) (Capabilities, error)

	// @agent-contract: Open creates whatever producer-internal state the
	// (intent, realized write_semantics) combination requires (staging area,
	// snapshot, MVCC transaction, picked-item flip, or nothing) and returns
	// the producer-supplied address the executor will use on the data path.
	// Producers that may have nothing to give right now (e.g. an empty
	// items-table queue) return OpenOutcome{Available: false}. Rimsky rolls
	// back the dispatch tx and may retry on the next scheduler tick.
	// Producer-side faults flow as gRPC error status codes (or wrapped Go
	// errors), not as Available: false.
	Open(ctx context.Context, claimID ClaimID, spec ClaimSpec) (OpenOutcome, error)

	// @agent-contract: Commit signals that the consumer of the claim
	// succeeded. The producer decides what to do with its own state per its
	// own configuration. The returned CommitResult mirrors the base-protocol
	// CommitResponse wire message: producers MAY stamp a canonical
	// version_id (persisted on the claim-handle row) and opaque
	// producer_metadata bytes (surfaced in the fan-out parent's writeback at
	// parent terminal). Both fields are optional; a producer with nothing to
	// report returns the zero CommitResult.
	Commit(ctx context.Context, claimID ClaimID, scope []byte, address []byte) (CommitResult, error)

	// @agent-contract: Abandon signals that the consumer of the claim
	// failed. The producer decides what to do with its own state per its own
	// configuration. address may be empty when Open's response was lost.
	Abandon(ctx context.Context, claimID ClaimID, scope []byte, address []byte) error

	// @agent-contract: Release tears down producer-internal read state
	// created at Open. Sync producers typically no-op.
	Release(ctx context.Context, claimID ClaimID, scope []byte, address []byte) error

	// @agent-contract: SplitScope is optional. Producers that advertise
	// Capabilities.SupportsSplitScope partition a parent claim-scope into
	// disjoint sub-claim-scopes for fan-out dispatch. The parent
	// claim_handle MUST already be Open'd; rimsky calls SplitScope inside
	// the same acquisition transaction that opened the parent.
	// partition_request is producer-interpreted bytes (e.g., a date range, a
	// list of regions, a hash bucket count). The producer returns a list of
	// SubClaimScopeDescriptors. Per spec §Fan-out. Producers that do NOT
	// support partitioning return ErrSplitScopeUnsupported. Rimsky validates
	// Capabilities at canonicalization and only calls SplitScope for
	// producers that advertise it.
	SplitScope(ctx context.Context, req SplitClaimScopeRequest) (SplitClaimScopeResponse, error)

	// @agent-contract: ScopesConflict is optional. Producers that advertise
	// Capabilities.SupportsScopesConflict implement producer-specific scope
	// conflict semantics. When unsupported (the default), rimsky uses
	// byte-equal scope conflict per @blessed-invariant 4b.
	ScopesConflict(ctx context.Context, a, b []byte) (bool, error)
}
