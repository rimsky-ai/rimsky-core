// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package persistence

import (
	"context"
	"errors"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

var ErrRunClaimantMismatch = errors.New("persistence: rimsky_node_runs row not in expected (active, claimed_by) state")

type DispatchRequest struct {
	NodeID                 shared.UUID
	ExecutorName           string
	RequiredClaimProducers []string
	EnqueuedAt             time.Time
	FrameID                shared.UUID
	// @concept: run-scope
	RunScopeID shared.UUID

	// @concept: run-scope
	PriorNodeRunID *shared.UUID

	// @concept: run-scope
	PriorDispatchDisposition string

	// @concept: node-run
	// @decision: non-cascade-direct-to-stale
	CreationReason cascade.CreationReason

	// @concept: executor
	InitialScratchInline        []byte
	InitialScratchHandle        string
	InitialScratchHandleBackend string
}

// @concept: service-address-book
type SelectCandidatesRequest struct {
	Limit int

	CursorEnqueuedAfter  time.Time
	CursorAfterSequence  int64
	CursorAfterNodeRunID shared.UUID
}

type Candidate struct {
	NodeRunID              shared.UUID
	NodeID                 shared.UUID
	NodeType               string
	ExecutorName           string
	RequiredClaimProducers []string
	EnqueuedAt             time.Time
	FrameID                shared.UUID

	// @concept: wait-set
	Sequence int64

	// @concept: run-scope
	PriorNodeRunID *shared.UUID
	// @concept: run-scope
	PriorDispatchDisposition string
}

type ClaimOwnership struct {
	Kind         string
	SupervisorID string
}

const (
	ClaimOwnershipKindNotFound  = "not_found"
	ClaimOwnershipKindUnclaimed = "unclaimed"
	ClaimOwnershipKindClaimedBy = "claimed_by"
)

type DispatchListFilter struct {
	State        string
	ExecutorName string
	InstanceID   *shared.UUID
}

type Queue interface {
	Enqueue(ctx context.Context, req DispatchRequest, tx Tx) error

	SelectCandidates(ctx context.Context, req SelectCandidatesRequest, tx Tx) ([]Candidate, error)

	// @concept: wait-set
	// @concept: cascade
	ListInFlightRunStates(ctx context.Context, nodeIDs []shared.UUID, frameID, runScopeID shared.UUID, tx Tx) (map[shared.UUID][]string, error)

	ClaimDispatchRow(ctx context.Context, nodeRunID shared.UUID, supervisorID string, tx Tx) (claimed bool, err error)

	PromoteClaimedToRunning(ctx context.Context, nodeRunID shared.UUID, supervisorID string, tx Tx) (promoted bool, err error)

	Complete(ctx context.Context, nodeRunID shared.UUID, expectedClaimedBy string) error

	ForceComplete(ctx context.Context, nodeRunID shared.UUID) error

	RemoveForNode(ctx context.Context, nodeID shared.UUID, runScopeID shared.UUID, expectedClaimedBy string, tx Tx) error

	// @concept: run-scope
	ForceRemoveForNode(ctx context.Context, nodeID shared.UUID, runScopeID shared.UUID, tx Tx) error

	ListOrphanedClaims(ctx context.Context) ([]DispatchRow, error)

	ReleaseClaim(ctx context.Context, nodeRunID shared.UUID, expectedClaimedBy string) error

	// @concept: node-run
	ReleaseClaimWithDisposition(ctx context.Context, nodeRunID shared.UUID, expectedClaimedBy string, disposition string) error

	// @concept: node-run
	StampPriorDispatch(ctx context.Context, nodeRunID shared.UUID, priorNodeRunID shared.UUID, disposition string, tx Tx) error

	ForceReleaseClaim(ctx context.Context, nodeRunID shared.UUID) error

	GetClaimedBy(ctx context.Context, nodeRunID shared.UUID) (ClaimOwnership, error)

	GetDispatchNode(ctx context.Context, nodeRunID shared.UUID, tx Tx) (shared.UUID, ClaimOwnership, error)

	LookupRunByAsyncAckID(ctx context.Context, ackID string, tx Tx) (*DispatchRow, error)

	RegisterAsyncAck(ctx context.Context, runID shared.UUID, ackID string, now time.Time, maxQuietSec *int, maxRuntimeSec *int, expectedPrincipal string, callbackURL string, tx Tx) error

	BumpLastProgressAt(ctx context.Context, runID shared.UUID, now time.Time, tx Tx) (bool, error)

	ListLive(ctx context.Context, filter DispatchListFilter, pag ListPagination) (PaginatedListResult[DispatchRow], error)

	CountLive(ctx context.Context, filter DispatchListFilter) (int, error)

	CountParked(ctx context.Context) (int, error)

	GetByID(ctx context.Context, id shared.UUID) (*DispatchRow, error)

	// @decision: node-state-retired-from-operator-api
	GetAnyByID(ctx context.Context, id shared.UUID) (*DispatchRow, error)

	// @concept: run-scope
	GetInFlightRunForNode(ctx context.Context, nodeID, runScopeID shared.UUID, tx Tx) (shared.UUID, bool, error)

	// @concept: run-scope
	GetMostRecentRunForNodeInScope(ctx context.Context, nodeID, runScopeID shared.UUID, tx Tx) (shared.UUID, bool, error)

	ParkActive(ctx context.Context, in ParkActiveInput, tx Tx) error

	ListParkedReadyForResume(ctx context.Context, cutoff time.Time, limit int) ([]ParkedRow, error)

	ListParkedDiagnostic(ctx context.Context, tx Tx) ([]ParkedDiagnosticRow, error)

	// @concept: run-scope
	GetParkedByNode(ctx context.Context, nodeID shared.UUID, runScopeID shared.UUID, tx Tx) (*ParkedRow, error)

	ResumeParked(ctx context.Context, nodeRunID shared.UUID, tx Tx) (resumed bool, err error)

	// @concept: executor
	LoadScratch(ctx context.Context, nodeRunID shared.UUID, tx Tx) (inline []byte, handle, handleBackend string, err error)

	// @concept: executor
	// @decision: scratch-column
	WriteScratch(ctx context.Context, nodeRunID shared.UUID, inline []byte, handle, handleBackend string, tx Tx) error
}

type ParkActiveInput struct {
	NodeRunID         shared.UUID
	ExpectedClaimedBy string
	ParkedAt          time.Time
	ResumeAt          time.Time
}

type ParkedDiagnosticRow struct {
	NodeRunID  shared.UUID
	InstanceID string
	NodeID     string
	FrameID    string
	ParkedAt   time.Time
	ResumeAt   time.Time
}

type ParkedRow struct {
	NodeRunID                shared.UUID
	NodeID                   shared.UUID
	ExecutorName             string
	RequiredClaimProducers   []string
	FrameID                  shared.UUID
	ParkedAt                 time.Time
	ResumeAt                 *time.Time
	ConsecutiveRetriesNoProg int
}
