// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package claimproducer

import "context"

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
	Name() string

	Capabilities(ctx context.Context) (Capabilities, error)

	Open(ctx context.Context, claimID ClaimID, spec ClaimSpec) (OpenOutcome, error)

	Commit(ctx context.Context, claimID ClaimID, scope []byte, address []byte) (CommitResult, error)

	Abandon(ctx context.Context, claimID ClaimID, scope []byte, address []byte) error

	Release(ctx context.Context, claimID ClaimID, scope []byte, address []byte) error

	SplitScope(ctx context.Context, req SplitClaimScopeRequest) (SplitClaimScopeResponse, error)

	ScopesConflict(ctx context.Context, a, b []byte) (bool, error)
}
