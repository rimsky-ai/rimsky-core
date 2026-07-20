// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package persistence

import (
	"context"
	"encoding/json"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

type LockKind string

const (
	LockKindNamed LockKind = "named"
	LockKindScope LockKind = "claim_scope"
)

type ClaimHandleRow struct {
	ID             shared.UUID     `json:"id"`
	LockKind       LockKind        `json:"lock_kind"`
	LockName       *string         `json:"lock_name,omitempty"`
	ProducerName   *string         `json:"producer_name,omitempty"`
	ClaimScopeData json.RawMessage `json:"claim_scope_data,omitempty"`
	Address        json.RawMessage `json:"-"`
	// @concept: claim-co-holdership
	Payload                json.RawMessage `json:"-"`
	Intent                 *string         `json:"intent,omitempty"`
	HolderSupervisorID     *string         `json:"holder_supervisor_id,omitempty"`
	HolderNodeID           shared.UUID     `json:"holder_node_id"`
	ClaimedAt              time.Time       `json:"claimed_at"`
	ExpiresAt              time.Time       `json:"expires_at"`
	FrameID                *shared.UUID    `json:"frame_id,omitempty"`
	RealizedWriteSemantics string          `json:"realized_write_semantics,omitempty"`
	NodeRunID              *shared.UUID    `json:"node_run_id,omitempty"`
	IsHeld                 bool            `json:"is_held"`
	ParentClaimHandleID    *shared.UUID    `json:"parent_claim_handle_id,omitempty"`
	// @concept: claim-lifetime
	Lifetime spec.ClaimLifetime `json:"lifetime,omitempty"`
	// @concept: claim-handle
	State                   spec.ClaimHandleState `json:"state,omitempty"`
	ResolvedAt              *time.Time            `json:"resolved_at,omitempty"`
	VersionID               string                `json:"version_id,omitempty"`
	ProducerCandidateHandle []byte                `json:"-"`
	ProducerLeaseToken      string                `json:"-"`
	AggregationPolicy       json.RawMessage       `json:"aggregation_policy,omitempty"`
	ExpectedChildrenCount   int                   `json:"expected_children_count,omitempty"`
	CommittedChildrenCount  int                   `json:"committed_children_count,omitempty"`
	AbandonedChildrenCount  int                   `json:"abandoned_children_count,omitempty"`
}

type ClaimHandleInsertInput struct {
	ID                     shared.UUID
	NodeRunID              *shared.UUID
	LockKind               LockKind
	LockName               *string
	ProducerName           *string
	ClaimScopeData         json.RawMessage
	Address                json.RawMessage
	Payload                json.RawMessage
	Intent                 *string
	HolderSupervisorID     string
	HolderNodeID           shared.UUID
	ExpiresAt              time.Time
	FrameID                *shared.UUID
	RealizedWriteSemantics string
	IsHeld                 bool
	ParentClaimHandleID    *shared.UUID
	// @concept: claim-lifetime
	Lifetime                spec.ClaimLifetime
	ProducerCandidateHandle []byte
	ProducerLeaseToken      string
	AggregationPolicy       json.RawMessage
}

type ClaimHandleTable interface {
	Insert(ctx context.Context, in ClaimHandleInsertInput, tx Tx) error
	UpdateAddress(ctx context.Context, id shared.UUID, supervisorID string, address json.RawMessage, tx Tx) error
	// @concept: claim-co-holdership
	UpdatePayload(ctx context.Context, id shared.UUID, supervisorID string, payload json.RawMessage, tx Tx) error
	Get(ctx context.Context, id shared.UUID, tx Tx) (*ClaimHandleRow, error)
	ListByHolderNode(ctx context.Context, holderNodeID shared.UUID, tx Tx) ([]ClaimHandleRow, error)

	ListByNodeRun(ctx context.Context, nodeRunID shared.UUID, tx Tx) ([]ClaimHandleRow, error)
	ListExpired(ctx context.Context, tx Tx) ([]ClaimHandleRow, error)

	// @concept: orphan-reaper
	RenewExpiryForHolderRun(ctx context.Context, nodeRunID shared.UUID, newExpiry time.Time, tx Tx) error
	Delete(ctx context.Context, id shared.UUID, expectedSupervisorID string, tx Tx) error

	CountByNamedLock(ctx context.Context, lockName string, tx Tx) (int, error)

	ListByProducerClaimScope(ctx context.Context, producerName string, tx Tx) ([]ClaimHandleRow, error)

	DeleteIfExpired(ctx context.Context, id shared.UUID, supervisorID string, tx Tx) (bool, error)

	LockForUpdate(ctx context.Context, id shared.UUID, tx Tx) (*ClaimHandleRow, error)

	UpdateClaimScope(ctx context.Context, id shared.UUID, supervisorID string, scope json.RawMessage, tx Tx) error

	UpdateNodeRunID(ctx context.Context, id shared.UUID, nodeRunID shared.UUID, supervisorID string, tx Tx) error

	ReassignHolderSupervisor(ctx context.Context, id shared.UUID, fromSupervisorID, toSupervisorID string, tx Tx) error

	UpdateRealizedWriteSemantics(ctx context.Context, id shared.UUID, supervisorID string, ws string, tx Tx) error

	ListForObservability(ctx context.Context, filter ClaimHandleListFilter, pag ListPagination, tx Tx) (PaginatedListResult[ClaimHandleRow], error)

	GetByFrameAndNode(ctx context.Context, nodeID shared.UUID, frameID shared.UUID, tx Tx) (*ClaimHandleRow, error)

	ListChildClaimHandles(ctx context.Context, parentID shared.UUID, tx Tx) ([]ClaimHandleRow, error)

	SetVersionID(ctx context.Context, id shared.UUID, supervisorID string, versionID string, tx Tx) error

	// @concept: claim-handle
	// @concept: claim-lifetime
	DeleteResolvedOlderThan(ctx context.Context, cutoff time.Time) (int, error)

	DeleteResolved(ctx context.Context, id shared.UUID, tx Tx) error

	DeleteResolvedIfNoActiveHolders(ctx context.Context, id shared.UUID, tx Tx) (bool, error)

	Promote(ctx context.Context, id shared.UUID, supervisorID string,
		newState spec.ClaimHandleState, tx Tx) error

	ListByState(ctx context.Context, state spec.ClaimHandleState, tx Tx) ([]ClaimHandleRow, error)

	ListByInstanceAndState(ctx context.Context, instanceID shared.UUID,
		state spec.ClaimHandleState, lifetime spec.ClaimLifetime, tx Tx) ([]ClaimHandleRow, error)

	SetAggregationPolicy(ctx context.Context, id shared.UUID, supervisorID string, policy json.RawMessage, tx Tx) error

	BumpExpectedChildrenCount(ctx context.Context, id shared.UUID, supervisorID string, delta int, tx Tx) error

	BumpChildOutcomeCount(ctx context.Context, id shared.UUID, supervisorID string, outcome string, delta int, tx Tx) error
}

type ClaimHandleListFilter struct {
	ProducerName     string
	HolderNodeID     *shared.UUID
	HolderSupervisor string
	InstanceID       *shared.UUID
	NodeType         string
}
