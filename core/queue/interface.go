// Package queue defines the DispatchQueue interface — the claimable-work
// primitive between scheduler and supervisors. The Postgres implementation
// lives in core/queue/postgres.
package queue

import (
	"context"
	"time"

	"github.com/fallguy/rimsky/core/shared"
)

// DispatchRequest is the payload for Enqueue.
type DispatchRequest struct {
	NodeID          shared.UUID
	ExecutorName    string    // empty for pure-cascade (which never enqueue)
	ConcurrencyTags []string
	EnqueuedAt      time.Time // may be future-dated for backoff
}

// ClaimOwnership is the return shape of GetClaimedBy. Kind is:
//   "not_found" — dispatch row does not exist
//   "unclaimed" — row exists, claimed_by IS NULL
//   "claimed_by" — row exists, claimed_by = SupervisorID
type ClaimOwnership struct {
	Kind         string
	SupervisorID string
}

// DispatchQueue is the claimable-work primitive. The Postgres implementation
// at core/queue/postgres/queue.go carries the load-bearing @blessed-invariant
// on Claim (tag-limit counts from dispatch rows, not node state).
type DispatchQueue interface {
	Enqueue(ctx context.Context, req DispatchRequest) error

	// Claim atomically selects one ready dispatch row matching the
	// supervisor's accept list (executor names) and respecting tag limits,
	// marks it claimed_by=supervisorID, and returns it. Returns nil, nil
	// when no matching row is available.
	Claim(ctx context.Context, supervisorID string, accepts []string, limits map[string]int) (*shared.DispatchRow, error)

	// Complete deletes a dispatch row. If expectedClaimedBy is non-empty,
	// the delete is guarded (no-op on mismatch).
	Complete(ctx context.Context, dispatchID shared.UUID, expectedClaimedBy string) error

	// Fail deletes a dispatch row with an optional failure reason.
	// Implementations may log the reason but the row is always removed.
	Fail(ctx context.Context, dispatchID shared.UUID, reason string, expectedClaimedBy string) error

	// RemoveForNode deletes any pending dispatch row for a given node.
	// Used when a node is invalidated while queued (claim becomes moot).
	RemoveForNode(ctx context.Context, nodeID shared.UUID, expectedClaimedBy string) error

	// ListOrphanedClaims returns dispatch rows whose claimed_at is older than
	// cutoff AND whose node is still in state=stale (i.e. the supervisor
	// died between Claim and the state→running transition).
	ListOrphanedClaims(ctx context.Context, cutoff time.Time) ([]shared.DispatchRow, error)

	// ReleaseClaim sets claimed_by=NULL, claimed_at=NULL on a dispatch row.
	// If expectedClaimedBy is non-empty, the release is claimant-guarded
	// (no-op on mismatch — protects a fresh supervisor's live claim from a
	// stale sweep).
	ReleaseClaim(ctx context.Context, dispatchID shared.UUID, expectedClaimedBy string) error

	// GetClaimedBy returns current ownership of a dispatch row. Used by the
	// supervisor's verify-before-run invariant (§6.2 step 4 / §17).
	GetClaimedBy(ctx context.Context, dispatchID shared.UUID) (ClaimOwnership, error)
}
