// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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
	// rimsky_worker_request.frame_id is NOT NULL. Sourced from
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
// rimsky_worker_request rows. State is one of "" (any) / "pending"
// (claimed_by IS NULL) / "claimed" (claimed_by IS NOT NULL).
// ExecutorName matches rimsky_worker_request.executor_name exactly when set.
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
	// rimsky_worker_request.claimed_by from NULL to supervisorID for the
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

	// RefreshHeartbeat extends rimsky_worker_request.last_heartbeat_at to now()
	// for every row claimed by supervisorID.
	RefreshHeartbeat(ctx context.Context, supervisorID string) error

	// ListLive returns currently-live dispatch rows (the table holds only
	// rows with no terminal yet — terminals delete the row). Used by the
	// observability dispatches endpoint. Cursor pagination follows the
	// (enqueued_at DESC, id DESC) ordering documented in the spec §1.2.3.
	ListLive(ctx context.Context, filter DispatchListFilter, pag ListPagination) (PaginatedListResult[shared.DispatchRow], error)

	// CountLive counts currently-live dispatch rows matching filter.
	CountLive(ctx context.Context, filter DispatchListFilter) (int, error)

	// CountParkedByReason returns counts of currently-parked
	// rimsky_worker_request rows grouped by parked_reason. Empty
	// reason buckets under the literal string "" so callers can
	// disambiguate "not parked" (absent from map) from "parked with
	// no reason" (key=""). Used by the metrics gauge refresher
	// (`rimsky_parked_by_reason`).
	CountParkedByReason(ctx context.Context) (map[string]int, error)

	// GetByID returns the live dispatch row for id, or nil when no such
	// row exists (e.g. terminal-deleted). Used by the observability
	// /v1/observability/dispatches/{id} endpoint to avoid a full O(N)
	// ListLive scan.
	GetByID(ctx context.Context, id shared.UUID) (*shared.DispatchRow, error)

	// ParkActive transitions a worker_request row from phase='active' to
	// phase='parked' under the claimant's id. Persists the park metadata
	// (parked_at, resume_at, parked_reason, session_token) and the
	// payload via inline-or-handle (exactly one is non-empty). Clears
	// claimed_by / claimed_at / last_heartbeat_at so the orphan-claim
	// reaper's `claimed_by IS NOT NULL` predicate excludes the row. Used
	// by E1's applyTerminalPark.
	ParkActiveInTx(ctx context.Context, tx Tx, in ParkActiveInput) error

	// ListParkedReadyForResume returns parked rows whose resume_at has
	// elapsed (resume_at <= cutoff), ordered by resume_at ascending.
	// Limit caps the per-tick batch. Used by E3's SweepParkedNodes.
	ListParkedReadyForResume(ctx context.Context, cutoff time.Time, limit int) ([]ParkedRow, error)

	// ListParkedOverdue returns parked rows whose
	// parked_at + max_park_duration_seconds has elapsed. The watchdog
	// path (SweepParkedNodes) uses this to force a park_timeout failure.
	// Limit caps the per-tick batch.
	ListParkedOverdue(ctx context.Context, now time.Time, limit int) ([]ParkedRow, error)

	// GetParkedByNode returns the parked worker_request row for a node,
	// or nil when the node is not parked. Used by the
	// admin-invalidate-against-parked path (G3) and by E4 resume dispatch
	// to load the persisted park metadata.
	GetParkedByNode(ctx context.Context, nodeID shared.UUID) (*ParkedRow, error)

	// ResumeParkedInTx transitions a parked row back to phase='pending'
	// (so any eligible supervisor can pick it up — the row's claimed_by is
	// reset to NULL). Park metadata (parked_payload_*, parked_reason,
	// session_token) is preserved so the resume-dispatch path can build
	// ResumeContext from it; the runner clears it via
	// ClearResumeMetadataInTx after a successful dispatch.
	//
	// Used by E3's sweep-driven wake and by the unified invalidate
	// handler (G3) for handler-emitted wakes. Returns resumed=true when
	// exactly one row was updated.
	//
	// supervisorID is recorded in audit-log rows by callers; the row
	// goes back to claimed_by=NULL so any supervisor can pick it up.
	// wakeReason is persisted on rimsky_worker_request.wake_reason so
	// the resume-dispatch path (LoadResumeMetadataInTx) can attach it
	// to ResumeContext.resume_reason ("deadline_elapsed" |
	// "external_invalidate"). Empty wakeReason persists NULL.
	ResumeParkedInTx(ctx context.Context, tx Tx, dispatchID shared.UUID, supervisorID, wakeReason string) (resumed bool, err error)

	// GetRetryNoProgress returns the current counter value plus the
	// per-row max_retries_without_progress override (NULL → use deployment
	// default). Used by E5 to test the cap.
	GetRetryNoProgress(ctx context.Context, dispatchID shared.UUID) (count int, override *int, err error)

	// SetRetryNoProgressForNodeInTx writes the carry-forward counter
	// onto the worker_request row identified by node_id (not by
	// dispatch_id). Used by the retry path: after the original
	// dispatch row is removed and a new one is inserted, the supervisor
	// re-stamps the carried-forward counter so the cap can accumulate
	// across retries. NodeID is the lookup key because the new row's
	// dispatch_id is the freshly-allocated UUID.
	SetRetryNoProgressForNodeInTx(ctx context.Context, tx Tx, nodeID shared.UUID, count int) error

	// UpdateDispatchTuning sets the per-row max_park_duration_seconds and
	// max_retries_without_progress denormalized columns at dispatch time
	// from the resolved template DSL. Used by F2/F3 dispatch wiring.
	UpdateDispatchTuningInTx(ctx context.Context, tx Tx, dispatchID shared.UUID, maxParkDurationSeconds *int, maxRetriesWithoutProgress *int) error

	// LoadResumeMetadataInTx returns the parked metadata that survived
	// the parked → pending transition (parked_payload_inline /
	// parked_payload_handle / parked_payload_handle_backend /
	// parked_reason / session_token). Returns (nil, nil) when the row
	// has no parked metadata (fresh dispatch). Used by E4's resume
	// dispatch.
	LoadResumeMetadataInTx(ctx context.Context, tx Tx, dispatchID shared.UUID) (*ResumeMetadataRow, error)

	// ClearResumeMetadataInTx clears the parked_payload_* /
	// parked_reason / session_token columns. Called by the runner
	// after a successful resume dispatch so a re-park cycle starts
	// clean.
	ClearResumeMetadataInTx(ctx context.Context, tx Tx, dispatchID shared.UUID) error
}

// ResumeMetadataRow is the parked metadata loaded by
// LoadResumeMetadataInTx for the runner's resume-dispatch path.
type ResumeMetadataRow struct {
	PayloadInline        []byte
	PayloadHandle        string
	PayloadHandleBackend string
	Reason               string
	SessionToken         string
	// WakeReason carries the WakeReason enum value persisted by
	// ResumeParkedInTx ("deadline_elapsed" | "external_invalidate").
	// Empty when no wake has been recorded.
	WakeReason string
	// ParkedAt is the timestamp recorded when the worker_request first
	// transitioned to phase='parked' (ParkActiveInTx). Preserved across
	// the parked → pending transition; reset to NULL by
	// ClearResumeMetadataInTx after a successful resume dispatch. Zero
	// when no park occurred (fresh dispatch).
	ParkedAt time.Time
}

// ParkActiveInput is the payload for Queue.ParkActiveInTx.
//
// PayloadInline and PayloadHandle are mutually exclusive: at most one is
// non-empty per write. When PayloadHandle is set, PayloadHandleBackend
// MUST also be non-empty so the read path can route the fetch.
//
// ResumeAt may be zero (indefinite park; resume only via
// external invalidate). Reason is recommended non-empty but not enforced.
type ParkActiveInput struct {
	DispatchID           shared.UUID
	ExpectedClaimedBy    string
	ParkedAt             time.Time
	ResumeAt             time.Time // zero ⇒ NULL (no deadline-based resume)
	Reason               string
	SessionToken         string
	PayloadInline        []byte
	PayloadHandle        string
	PayloadHandleBackend string
}

// ParkedRow is a row returned by ListParkedReadyForResume,
// ListParkedOverdue, and GetParkedByNode. Carries the persisted park
// metadata so the resume-dispatch path (E4) can build ResumeContext.
type ParkedRow struct {
	DispatchID                shared.UUID
	NodeID                    shared.UUID
	ExecutorName              string
	RequiredStores            []string
	FrameID                   shared.UUID
	ParkedAt                  time.Time
	ResumeAt                  *time.Time
	Reason                    string
	SessionToken              string
	PayloadInline             []byte
	PayloadHandle             string
	PayloadHandleBackend      string
	MaxParkDurationSeconds    *int
	ConsecutiveRetriesNoProg  int
}
