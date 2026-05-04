package claimproducer

import (
	"context"

	"github.com/google/uuid"
)

// ClaimProducer is the Go interface for the ClaimProducer service
// protocol. See spec §2 for wire shapes and invariants.
//
// @blessed-invariant 9b: ClaimProducer implementations MUST NOT
// internally serialize on lock-shaped predicates. The reader-lease
// serialization pattern is forbidden for staged_async; honest support
// requires snapshot delegation or native MVCC pass-through.
type ClaimProducer interface {
	Open(ctx context.Context, req OpenRequest) (ClaimResult, error)
	Commit(ctx context.Context, claimID uuid.UUID) error
	Abandon(ctx context.Context, claimID uuid.UUID) error
	Release(ctx context.Context, claimID uuid.UUID) error
	Capabilities(ctx context.Context) (CapabilitiesResult, error)
}
