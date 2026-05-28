// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package persistence

import (
	"context"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

// ClaimHolderState enumerates the per-claim-holder lifecycle states.
type ClaimHolderState string

const (
	ClaimHolderStateActive    ClaimHolderState = "active"
	ClaimHolderStateCompleted ClaimHolderState = "completed"
	ClaimHolderStateFailed    ClaimHolderState = "failed"
)

// ClaimHolderRow mirrors a row of rimsky_claim_holders.
//
// Post-stage-5 of the run-row lifecycle cutover the holder is keyed by
// HolderRunID (a `rimsky_node_runs.id`), not by node id — co-holdership
// (`holds:` template directive) and inheritor membership both bind to a
// specific run, so per-run identity is what auto-terminal walks.
type ClaimHolderRow struct {
	ID            shared.UUID      `json:"id"`
	ClaimHandleID shared.UUID      `json:"claim_handle_id"`
	HolderRunID   shared.UUID      `json:"holder_run_id"`
	State         ClaimHolderState `json:"state"`
	CompletedAt   *time.Time       `json:"completed_at,omitempty"`
}

// ClaimHolderInsertInput is the per-row input for Insert.
type ClaimHolderInsertInput struct {
	ID            shared.UUID
	ClaimHandleID shared.UUID
	HolderRunID   shared.UUID
	FrameID       *shared.UUID
}

// ClaimHolderTable is the rimsky_claim_holders accessor.
type ClaimHolderTable interface {
	Insert(ctx context.Context, in ClaimHolderInsertInput, tx Tx) error
	Get(ctx context.Context, id shared.UUID, tx Tx) (*ClaimHolderRow, error)
	ListByClaimHandleID(ctx context.Context, claimHandleID shared.UUID, tx Tx) ([]ClaimHolderRow, error)
	ListByHolderRun(ctx context.Context, holderRunID shared.UUID, tx Tx) ([]ClaimHolderRow, error)
	ListActiveByClaimHandleID(ctx context.Context, claimHandleID shared.UUID, tx Tx) ([]ClaimHolderRow, error)
	Complete(ctx context.Context, id shared.UUID, state ClaimHolderState, tx Tx) error
	CompleteByClaimHandleAndRun(ctx context.Context, claimHandleID, holderRunID shared.UUID, state ClaimHolderState, tx Tx) error
	// FailAllActiveByClaimHandle marks every still-'active' row for the
	// given claim_handle as 'failed'. Used by the held-claim
	// acquirer-failure path (operator-declared `error_types:` chain
	// resolving give_up for an executor Error or for synthetic
	// `acquire/unavailable`) so auto-terminal can fire immediately
	// rather than waiting for inheritors that will never reach a
	// terminal — the acquirer's failure means the held subgraph
	// aborts.
	//
	// Claimant-guarded per blessed-invariant 4: the UPDATE applies only
	// when rimsky_claim_handles.holder_supervisor_id matches supervisorID.
	// Defense-in-depth — today's call site is the legitimate owner by
	// construction (it just acquired the handle), but the guard prevents
	// a future refactor from acting on rows whose ownership has moved.
	FailAllActiveByClaimHandle(ctx context.Context, claimHandleID shared.UUID, supervisorID string, tx Tx) error
}
