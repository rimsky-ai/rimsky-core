// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package persistence

import (
	"context"
	"errors"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

var ErrClaimHolderNotActive = errors.New("persistence: claim-holder row not found or not active")

type ClaimHolderState string

const (
	ClaimHolderStateActive    ClaimHolderState = "active"
	ClaimHolderStateCompleted ClaimHolderState = "completed"
	ClaimHolderStateFailed    ClaimHolderState = "failed"
)

type ClaimHolderRow struct {
	ID              shared.UUID      `json:"id"`
	ClaimHandleID   shared.UUID      `json:"claim_handle_id"`
	HolderNodeRunID shared.UUID      `json:"holder_run_id"`
	State           ClaimHolderState `json:"state"`
	CompletedAt     *time.Time       `json:"completed_at,omitempty"`
}

type ClaimHolderInsertInput struct {
	ID              shared.UUID
	ClaimHandleID   shared.UUID
	HolderNodeRunID shared.UUID
}

type ClaimHolderTable interface {
	Insert(ctx context.Context, in ClaimHolderInsertInput, tx Tx) error
	Get(ctx context.Context, id shared.UUID, tx Tx) (*ClaimHolderRow, error)
	ListByClaimHandleID(ctx context.Context, claimHandleID shared.UUID, tx Tx) ([]ClaimHolderRow, error)
	ListByHolderRun(ctx context.Context, holderNodeRunID shared.UUID, tx Tx) ([]ClaimHolderRow, error)
	Complete(ctx context.Context, id shared.UUID, state ClaimHolderState, tx Tx) error
	CompleteByClaimHandleAndRun(ctx context.Context, claimHandleID, holderNodeRunID shared.UUID, state ClaimHolderState, tx Tx) error
	FailAllActiveByClaimHandle(ctx context.Context, claimHandleID shared.UUID, supervisorID string, tx Tx) error
}
