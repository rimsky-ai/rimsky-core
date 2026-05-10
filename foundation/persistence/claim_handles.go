// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package persistence

import (
	"context"
	"encoding/json"
	"time"

	"github.com/fallguy/rimsky/modeling/shared"
)

// LockKind enumerates the two flavours of rimsky_claim_handle row.
type LockKind string

const (
	LockKindNamed LockKind = "named"
	LockKindScope LockKind = "scope"
)

// ClaimHandleRow mirrors a row of rimsky_claim_handle.
type ClaimHandleRow struct {
	ID        shared.UUID     `json:"claim_id"`
	LockKind  LockKind        `json:"lock_kind"`
	LockName  *string         `json:"lock_name,omitempty"`
	StoreName *string         `json:"store_name,omitempty"`
	ScopeData json.RawMessage `json:"scope_data,omitempty"`
	// Address carries `json:"-"` so the observability handlers can
	// pass *ClaimHandleRow to writeJSON without leaking store-supplied
	// claim address bytes (spec §1.3 / blessed-invariant 20). Scope
	// is exposed because operators legitimately need to see what
	// scope a claim covers; address is opaque to rimsky and meant
	// for the store/executor only.
	Address            json.RawMessage `json:"-"`
	Intent             *string         `json:"intent,omitempty"`
	HolderSupervisorID string          `json:"holder_supervisor_id"`
	HolderNodeID       shared.UUID     `json:"holder_node_id"`
	ClaimedAt          time.Time       `json:"claimed_at"`
	LastHeartbeatAt    time.Time       `json:"last_heartbeat_at"`
	ExpiresAt          time.Time       `json:"expires_at"`
	FrameID            *shared.UUID    `json:"frame_id,omitempty"`
	// RealizedWriteSemantics is the per-claim semantics returned by
	// ClaimProducer.Open. Persisted on the lock-holder row so the
	// scope-conflict check (foundation/integration/runner_acquire.go::
	// evaluateScopeConflict) can apply ModeCoexists without re-dialing
	// the producer. Empty for named-lock rows.
	RealizedWriteSemantics string `json:"realized_write_semantics,omitempty"`
	// WorkerRequestID is the parent worker-request this claim handle
	// belongs to (FK rimsky_claim_handle.worker_request_id; CASCADE
	// delete on worker-request removal).
	WorkerRequestID *shared.UUID `json:"worker_request_id,omitempty"`
	// IsHeld marks claims that persist past the worker-request's
	// active terminal until the holding-subgraph completes (auto-
	// terminal mechanism per foundation contract §5.5).
	IsHeld bool `json:"is_held"`
}

// ClaimHandleInsertInput is the per-row input for Insert.
type ClaimHandleInsertInput struct {
	ID                     shared.UUID
	WorkerRequestID        *shared.UUID // FK to rimsky_worker_request.id (nullable for legacy/orphan paths)
	LockKind               LockKind
	LockName               *string
	StoreName              *string
	ScopeData              json.RawMessage
	Address                json.RawMessage
	Intent                 *string
	HolderSupervisorID     string
	HolderNodeID           shared.UUID
	ExpiresAt              time.Time
	FrameID                *shared.UUID
	RealizedWriteSemantics string // empty for named-lock rows
	// IsHeld marks claims that persist past the active terminal of
	// the owning worker-request. Computed from the holding-subgraph
	// declarations at acquisition time; auto-terminal fires aggregate-
	// outcome resolution when all rimsky_claim_holders rows for a held
	// claim_handle reach a non-active state.
	IsHeld bool
}

// ClaimHandlesStore is the rimsky_claim_handle accessor exposed on Store.
// The supervisor / scheduler / control-api facing surface for the lock
// ledger (per blessed-invariant 9a — `rimsky_claim_handle` is the sole
// authority for lock state).
type ClaimHandlesStore interface {
	Insert(ctx context.Context, in ClaimHandleInsertInput, tx Tx) error
	UpdateAddress(ctx context.Context, id shared.UUID, supervisorID string, address json.RawMessage, tx Tx) error
	Get(ctx context.Context, id shared.UUID, tx Tx) (*ClaimHandleRow, error)
	ListByHolderNode(ctx context.Context, holderNodeID shared.UUID, tx Tx) ([]ClaimHandleRow, error)
	ListBySupervisor(ctx context.Context, supervisorID string, tx Tx) ([]ClaimHandleRow, error)
	ExtendHeartbeat(ctx context.Context, supervisorID string, expiresAt time.Time, tx Tx) error
	ListExpired(ctx context.Context, tx Tx) ([]ClaimHandleRow, error)
	Delete(ctx context.Context, id shared.UUID, expectedSupervisorID string, tx Tx) error

	// CountByNamedLock returns the number of currently-held named-lock
	// rows for the given lock name. Used by the supervisor's named-lock
	// counting-mode eligibility check inside the acquisition tx.
	CountByNamedLock(ctx context.Context, lockName string, tx Tx) (int, error)

	// ListByStoreScope returns all lock-holder rows for a given store
	// name. The supervisor uses this for the in-Go scope-conflict check
	// inside the acquisition tx (byte-equal on scope_data per spec §7.7).
	ListByStoreScope(ctx context.Context, storeName string, tx Tx) ([]ClaimHandleRow, error)

	// DeleteIfExpired claimant-guards a delete on (id, supervisor_id,
	// expires_at). Returns true when the row was deleted; false otherwise.
	// Used by the orphan-reaper sweep.
	DeleteIfExpired(ctx context.Context, id shared.UUID, supervisorID string, tx Tx) (bool, error)

	// LockForUpdate runs SELECT ... FOR UPDATE on the lock-holder row.
	// Used by foundation/integration/auto_terminal.go::CheckAndFireResolution to
	// serialize auto-terminal resolution per blessed-invariant 13.
	// Returns (nil, nil) when the row does not exist (already deleted by
	// a prior resolution).
	LockForUpdate(ctx context.Context, id shared.UUID, tx Tx) (*ClaimHandleRow, error)

	// UpdateScope writes a new scope_data to a scope-kind row,
	// claimant-guarded on supervisorID. Used by the supervisor's
	// acquireClaim path when the store-chosen scope differs from the
	// substituted-selector scope the supervisor wrote at INSERT time.
	UpdateScope(ctx context.Context, id shared.UUID, supervisorID string, scope json.RawMessage, tx Tx) error

	// UpdateRealizedWriteSemantics writes the per-claim ClaimProducer-
	// declared realized_write_semantics on a scope-kind row,
	// claimant-guarded on supervisorID. Called after ClaimProducer.Open
	// returns; the value is then consumed by the in-Go scope-conflict
	// check (foundation/integration/runner_acquire.go::evaluateScopeConflict)
	// without re-dialing the producer.
	UpdateRealizedWriteSemantics(ctx context.Context, id shared.UUID, supervisorID string, ws string, tx Tx) error

	// ListForObservability returns rows matching the filter, paginated
	// by claimed_at DESC. Used by the observability /v1/observability/
	// lock-holders browse endpoint (spec §1.2.4).
	ListForObservability(ctx context.Context, filter LockHolderListFilter, pag ListPagination, tx Tx) (PaginatedListResult[ClaimHandleRow], error)

	// GetByFrameAndNode returns the lock-holder row whose holder_node_id
	// equals nodeID and frame_id equals frameID. Used by the
	// observability /v1/observability/dispatches/{id} endpoint to
	// resolve dispatch → claim_id without a full per-node scan. Returns
	// (nil, nil) when no matching row exists.
	GetByFrameAndNode(ctx context.Context, nodeID shared.UUID, frameID shared.UUID, tx Tx) (*ClaimHandleRow, error)
}

// LockHolderListFilter is the observability browse filter for the
// rimsky_claim_handle endpoint (spec §1.2.4).
type LockHolderListFilter struct {
	StoreName        string
	HolderNodeID     *shared.UUID
	HolderSupervisor string
	InstanceID       *shared.UUID
	NodeType         string
}
