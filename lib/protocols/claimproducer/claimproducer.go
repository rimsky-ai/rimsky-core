// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

package claimproducer

import "context"

type ClaimProducer interface {
	Name() string

	Capabilities(ctx context.Context) (Capabilities, error)

	Open(ctx context.Context, claimID ClaimID, spec ClaimSpec) (OpenOutcome, error)

	Commit(ctx context.Context, claimID ClaimID, scope []byte, address []byte, leaseToken string) (CommitResult, error)

	Abandon(ctx context.Context, claimID ClaimID, scope []byte, address []byte, leaseToken string) error

	Release(ctx context.Context, claimID ClaimID, scope []byte, address []byte, leaseToken string) error

	SplitScope(ctx context.Context, req SplitClaimScopeRequest) (SplitClaimScopeResponse, error)

	ScopesConflict(ctx context.Context, a, b []byte) (bool, error)
}
