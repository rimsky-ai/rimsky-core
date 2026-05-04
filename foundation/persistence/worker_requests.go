package persistence

import (
	"context"
	"time"

	"github.com/fallguy/rimsky/modeling/shared"
)

// DispatchRequest is the payload for Enqueue.
//
// ExecutorName is empty for native (claim-only) nodes, which the supervisor's
// omnibus runner picks up directly (spec §7.3). It remains empty for
// pure-cascade nodes too, but those never enqueue.
//
// RequiredStores is denormalised from the template's per-node-type
// `nodeRequiredStores(node_type)` at enqueue time (spec §9.6 / §6.2). It
// drives the supervisor-pool specialisation predicate at claim time —
// supervisors only consider rows whose RequiredStores ⊆ AcceptedStores.
type DispatchRequest struct {
	NodeID         shared.UUID
	ExecutorName   string
	RequiredStores []string
	EnqueuedAt     time.Time // may be future-dated for backoff
	// FrameID is the frame this dispatch belongs to (per
	// docs/history/2026-04-26-frame-resolution-design.md §10.2). Required:
	// rimsky_dispatch.frame_id is NOT NULL. Sourced from
	// rimsky_nodes.frame_id at enqueue time.
	//
	// @blessed-invariant 19: dispatch rows always carry a non-zero frame id.
	FrameID shared.UUID
}

// SelectCandidatesRequest is the input to SelectCandidates (spec §7.3
// step 1). The supervisor passes its accept-lists; the queue returns
// candidate dispatch rows the supervisor pool is allowed to consider.
type SelectCandidatesRequest struct {
	// AcceptedExecutors is the supervisor's executor accept-list. The
	// dispatch SELECT filters
	// (executor_name = ANY(AcceptedExecutors) OR executor_name IS NULL).
	// IS NULL covers native (claim-only) nodes per §7.3.
	AcceptedExecutors []string

	// AcceptedStores is the supervisor's store accept-list (spec §6.2).
	// The dispatch SELECT filters required_stores <@ AcceptedStores.
	AcceptedStores []string

	// Limit caps the candidate batch returned (FOR UPDATE SKIP LOCKED).
	// Implementations should pick a reasonable default (e.g. 100) when
	// Limit==0 to bound the working set on large backlogs.
	Limit int
}

// Candidate is a single dispatch row returned from SelectCandidates. The
// runner iterates these inside the open tx; rows it doesn't claim release
// their FOR UPDATE locks at tx end.
type Candidate struct {
	DispatchID     shared.UUID
	NodeID         shared.UUID
	NodeType       string
	ExecutorName   string
	RequiredStores []string
	EnqueuedAt     time.Time
	// FrameID is the frame this dispatch row belongs to. Per spec §10.2 +
	// blessed-invariant 19, this is non-zero for every claimable
	// candidate.
	FrameID shared.UUID
}

// ClaimOwnership is the return shape of GetClaimedBy. Kind is:
//
//	"not_found" — dispatch row does not exist
//	"unclaimed" — row exists, claimed_by IS NULL
//	"claimed_by" — row exists, claimed_by = SupervisorID
type ClaimOwnership struct {
	Kind         string
	SupervisorID string
}

// DispatchListFilter is the observability browse filter for live
// rimsky_dispatch rows. State is one of "" (any) / "pending"
// (claimed_by IS NULL) / "claimed" (claimed_by IS NOT NULL).
// ExecutorName matches rimsky_dispatch.executor_name exactly when set.
// InstanceID joins via node_id → rimsky_nodes when set.
type DispatchListFilter struct {
	State        string
	ExecutorName string
	InstanceID   *shared.UUID
}

// Queue is the claimable-work primitive. Implementations carry the
// load-bearing @blessed-invariant on ClaimDispatchRow (the dispatch row
// is the running-window primitive).
//
// SelectCandidates and ClaimDispatchRow are building-block helpers used by
// core/supervisor/runner.go to orchestrate the §7.3 atomic-acquisition
// transaction. The runner owns the persistence.Tx; the queue helpers
// participate in it.
type Queue interface {
	// Enqueue inserts or refreshes a dispatch row for the given node.
	// On UNIQUE(node_id) conflict the row is updated only when still
	// unclaimed and already eligible (claimed or future-dated rows are
	// left alone). RequiredStores overwrites the prior value.
	Enqueue(ctx context.Context, req DispatchRequest) error

	// EnqueueInTx is the tx-taking variant used inside the frame-tick tx.
	// Auto-commit Enqueue calls EnqueueInTx(ctx, req, nil) internally.
	EnqueueInTx(ctx context.Context, req DispatchRequest, tx Tx) error

	// SelectCandidates returns up to req.Limit dispatch rows the
	// supervisor pool is allowed to consider, filtered by accept-lists
	// and ordered by enqueued_at ascending. Rows are FOR UPDATE
	// SKIP LOCKED inside the caller's tx; rows the caller does not
	// claim release their locks at tx end. The caller MUST hold an
	// open transaction; implementations return an error when passed
	// a nil tx.
	SelectCandidates(ctx context.Context, tx Tx, req SelectCandidatesRequest) ([]Candidate, error)

	// ClaimDispatchRow performs the claimant-guarded UPDATE of
	// rimsky_dispatch.claimed_by from NULL to supervisorID for the
	// given dispatch row, inside the caller's tx. Sets claimed_at and
	// last_heartbeat_at to now(). Returns claimed=true when exactly one
	// row was updated; false when the row was already claimed by someone
	// else.
	ClaimDispatchRow(ctx context.Context, tx Tx, dispatchID shared.UUID, supervisorID string) (claimed bool, err error)

	// Complete deletes a dispatch row. If expectedClaimedBy is non-empty,
	// the delete is guarded (no-op on mismatch).
	Complete(ctx context.Context, dispatchID shared.UUID, expectedClaimedBy string) error

	// RemoveForNode deletes any pending dispatch row for a given node.
	// Used when a node is invalidated while queued (claim becomes moot).
	RemoveForNode(ctx context.Context, nodeID shared.UUID, expectedClaimedBy string) error

	// RemoveForNodeInTx is the tx-taking variant. The auto-commit
	// RemoveForNode calls this internally with tx=nil.
	RemoveForNodeInTx(ctx context.Context, nodeID shared.UUID, expectedClaimedBy string, tx Tx) error

	// ListOrphanedClaims returns dispatch rows whose last_heartbeat_at is
	// older than cutoff.
	ListOrphanedClaims(ctx context.Context, cutoff time.Time) ([]shared.DispatchRow, error)

	// ReleaseClaim sets claimed_by=NULL, claimed_at=NULL, last_heartbeat_at=NULL on a dispatch row.
	// If expectedClaimedBy is non-empty, the release is claimant-guarded
	// (no-op on mismatch — protects a fresh supervisor's live claim from a
	// stale sweep).
	ReleaseClaim(ctx context.Context, dispatchID shared.UUID, expectedClaimedBy string) error

	// GetClaimedBy returns current ownership of a dispatch row. Used by the
	// supervisor's verify-before-run invariant (§7.3 step 4 / §17).
	GetClaimedBy(ctx context.Context, dispatchID shared.UUID) (ClaimOwnership, error)

	// GetDispatchNode returns the node_id of a dispatch row plus its
	// current claim ownership. Used by the supervisor's §12.5
	// attributes-callback auth path. Returns ClaimOwnership{Kind:
	// "not_found"} when the dispatch row does not exist.
	GetDispatchNode(ctx context.Context, dispatchID shared.UUID) (shared.UUID, ClaimOwnership, error)

	// RefreshHeartbeat extends rimsky_dispatch.last_heartbeat_at to now()
	// for every row claimed by supervisorID.
	RefreshHeartbeat(ctx context.Context, supervisorID string) error

	// ListLive returns currently-live dispatch rows (the table holds only
	// rows with no terminal yet — terminals delete the row). Used by the
	// observability dispatches endpoint. Cursor pagination follows the
	// (enqueued_at DESC, id DESC) ordering documented in the spec §1.2.3.
	ListLive(ctx context.Context, filter DispatchListFilter, pag ListPagination) (PaginatedListResult[shared.DispatchRow], error)

	// CountLive counts currently-live dispatch rows matching filter.
	CountLive(ctx context.Context, filter DispatchListFilter) (int, error)

	// GetByID returns the live dispatch row for id, or nil when no such
	// row exists (e.g. terminal-deleted). Used by the observability
	// /v1/observability/dispatches/{id} endpoint to avoid a full O(N)
	// ListLive scan.
	GetByID(ctx context.Context, id shared.UUID) (*shared.DispatchRow, error)
}
