// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package claimproducer

import "context"

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
