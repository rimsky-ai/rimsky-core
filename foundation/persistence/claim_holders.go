// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package persistence

import (
	"context"
	"time"

	"github.com/fallguy/rimsky/modeling/shared"
)

// ClaimHolderState enumerates the per-claim-holder lifecycle states.
type ClaimHolderState string

const (
	ClaimHolderStateActive    ClaimHolderState = "active"
	ClaimHolderStateCompleted ClaimHolderState = "completed"
	ClaimHolderStateFailed    ClaimHolderState = "failed"
)

// ClaimHolderRow mirrors a row of rimsky_claim_holders.
type ClaimHolderRow struct {
	ID           shared.UUID      `json:"id"`
	LockHolderID shared.UUID      `json:"claim_handle_id"`
	HolderNodeID shared.UUID      `json:"holder_node_id"`
	State        ClaimHolderState `json:"state"`
	CompletedAt  *time.Time       `json:"completed_at,omitempty"`
}

// ClaimHolderInsertInput is the per-row input for Insert.
type ClaimHolderInsertInput struct {
	ID           shared.UUID
	LockHolderID shared.UUID
	HolderNodeID shared.UUID
	FrameID      *shared.UUID
}

// ClaimHoldersStore is the rimsky_claim_holders accessor.
type ClaimHoldersStore interface {
	Insert(ctx context.Context, in ClaimHolderInsertInput, tx Tx) error
	Get(ctx context.Context, id shared.UUID, tx Tx) (*ClaimHolderRow, error)
	ListByLockHolderID(ctx context.Context, lockHolderID shared.UUID, tx Tx) ([]ClaimHolderRow, error)
	ListByHolderNode(ctx context.Context, holderNodeID shared.UUID, tx Tx) ([]ClaimHolderRow, error)
	ListActiveByLockHolderID(ctx context.Context, lockHolderID shared.UUID, tx Tx) ([]ClaimHolderRow, error)
	Complete(ctx context.Context, id shared.UUID, state ClaimHolderState, tx Tx) error
	CompleteByLockHolderAndNode(ctx context.Context, lockHolderID, holderNodeID shared.UUID, state ClaimHolderState, tx Tx) error
}
