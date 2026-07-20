// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package persistence

import (
	"context"
	"errors"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

var ErrRunRowMissing = errors.New("persistence: rimsky_node_runs row not found")

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

type SelectCandidatesRequest struct {
	AcceptedExecutors []string

	AcceptedClaimProducers []string

	Limit int

	LateBindExecutorProxy string

	LateBindClaimProducerProxy string

	CursorEnqueuedAfter  time.Time
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

	// @concept: run-scope
	PriorNodeRunID *shared.UUID
	// @concept: run-scope
	PriorDispatchDisposition string

	// @concept: parked-state
	PreClaimState string
}

type ClaimOwnership struct {
	Kind         string
	SupervisorID string
}

type DispatchListFilter struct {
	State        string
	ExecutorName string
	InstanceID   *shared.UUID
}

type Queue interface {
	Enqueue(ctx context.Context, req DispatchRequest) error

	EnqueueInTx(ctx context.Context, req DispatchRequest, tx Tx) error

	SelectCandidates(ctx context.Context, tx Tx, req SelectCandidatesRequest) ([]Candidate, error)

	// @concept: wait-set
	// @concept: cascade
	ListInFlightRunStates(ctx context.Context, tx Tx, nodeIDs []shared.UUID, frameID, runScopeID shared.UUID) (map[shared.UUID][]string, error)

	ClaimDispatchRow(ctx context.Context, tx Tx, nodeRunID shared.UUID, supervisorID string) (claimed bool, err error)

	PromoteClaimedToRunning(ctx context.Context, tx Tx, nodeRunID shared.UUID, supervisorID string) (promoted bool, err error)

	Complete(ctx context.Context, nodeRunID shared.UUID, expectedClaimedBy string) error

	ForceComplete(ctx context.Context, nodeRunID shared.UUID) error

	// @concept: run-scope
	RemoveForNode(ctx context.Context, nodeID shared.UUID, runScopeID shared.UUID, expectedClaimedBy string) error

	RemoveForNodeInTx(ctx context.Context, nodeID shared.UUID, runScopeID shared.UUID, expectedClaimedBy string, tx Tx) error

	// @concept: run-scope
	ForceRemoveForNode(ctx context.Context, nodeID shared.UUID, runScopeID shared.UUID) error

	ForceRemoveForNodeInTx(ctx context.Context, nodeID shared.UUID, runScopeID shared.UUID, tx Tx) error

	ListOrphanedClaims(ctx context.Context) ([]DispatchRow, error)

	ReleaseClaim(ctx context.Context, nodeRunID shared.UUID, expectedClaimedBy string) error

	// @concept: node-run
	ReleaseClaimWithDisposition(ctx context.Context, nodeRunID shared.UUID, expectedClaimedBy string, disposition string) error

	// @concept: node-run
	StampPriorDispatchInTx(ctx context.Context, tx Tx, nodeRunID shared.UUID, priorNodeRunID shared.UUID, disposition string) error

	ForceReleaseClaim(ctx context.Context, nodeRunID shared.UUID) error

	GetClaimedBy(ctx context.Context, nodeRunID shared.UUID) (ClaimOwnership, error)

	GetDispatchNode(ctx context.Context, nodeRunID shared.UUID) (shared.UUID, ClaimOwnership, error)

	LookupRunByAsyncAckID(ctx context.Context, tx Tx, ackID string) (*DispatchRow, error)

	RegisterAsyncAck(ctx context.Context, tx Tx, runID shared.UUID, ackID string, now time.Time, maxQuietSec *int, maxRuntimeSec *int, expectedPrincipal string) error

	BumpLastProgressAt(ctx context.Context, tx Tx, runID shared.UUID, now time.Time) (bool, error)

	ListLive(ctx context.Context, filter DispatchListFilter, pag ListPagination) (PaginatedListResult[DispatchRow], error)

	CountLive(ctx context.Context, filter DispatchListFilter) (int, error)

	CountParked(ctx context.Context) (int, error)

	GetByID(ctx context.Context, id shared.UUID) (*DispatchRow, error)

	// @concept: run-scope
	GetInFlightRunForNode(ctx context.Context, tx Tx, nodeID, runScopeID shared.UUID) (shared.UUID, bool, error)

	// @concept: run-scope
	GetMostRecentRunForNodeInScope(ctx context.Context, tx Tx, nodeID, runScopeID shared.UUID) (shared.UUID, bool, error)

	ParkActiveInTx(ctx context.Context, tx Tx, in ParkActiveInput) error

	ListParkedReadyForResume(ctx context.Context, cutoff time.Time, limit int) ([]ParkedRow, error)

	ListParkedDiagnostic(ctx context.Context, tx Tx) ([]ParkedDiagnosticRow, error)

	// @concept: run-scope
	GetParkedByNode(ctx context.Context, tx Tx, nodeID shared.UUID, runScopeID shared.UUID) (*ParkedRow, error)

	ResumeParkedInTx(ctx context.Context, tx Tx, nodeRunID shared.UUID) (resumed bool, err error)

	GetRetryNoProgress(ctx context.Context, nodeRunID shared.UUID) (count int, override *int, err error)

	SetRetryNoProgressForRunInTx(ctx context.Context, tx Tx, nodeRunID shared.UUID, count int) error

	UpdateDispatchTuningInTx(ctx context.Context, tx Tx, nodeRunID shared.UUID, maxRetriesWithoutProgress *int) error

	// @concept: executor
	LoadScratchInTx(ctx context.Context, tx Tx, nodeRunID shared.UUID) (inline []byte, handle, handleBackend string, err error)

	// @concept: executor
	WriteScratchInTx(ctx context.Context, tx Tx, nodeRunID shared.UUID, inline []byte, handle, handleBackend string) error
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
