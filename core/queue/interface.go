// Package queue defines the DispatchQueue interface — the claimable-work
// primitive between scheduler and supervisors. The Postgres implementation
// lives in core/queue/postgres.
//
// # Claim-time eligibility model (spec §13.1–§13.3)
//
// The pre-redesign Claim() did everything in one call: candidate selection,
// per-tag advisory locking, dispatch UPDATE, and tag-limit accounting. That
// design assumed the only per-row gating data was the row itself
// (concurrency_tags, executor_name).
//
// Under the stores redesign, gating is split between row-level data and
// node-type-level data:
//
//   - Row-level (in rimsky_dispatch): executor_name, required_stores. The
//     dispatch SELECT filters on these via the supervisor's accept-lists.
//   - Node-type-level (in the in-memory template registry, NOT in the row):
//     locks: [] (named/region/claim specs). Every candidate has lock specs
//     keyed by its node_type, but the queue cannot look them up — the
//     template registry lives in core/node, which the queue does not import.
//
// Because of this split, the queue exposes building-block helpers and the
// supervisor's runner.go orchestrates the §13.3 atomic-acquisition tx end
// to end. The runner takes one pgx.Tx and:
//
//  1. Calls SelectCandidates to fetch the FOR UPDATE SKIP LOCKED candidate
//     batch (executor + required_stores filtered).
//  2. For each candidate, looks up the node_type's lock specs from the
//     in-memory template registry and runs the in-Go eligibility checks
//     (§13.2): named-lock count, region conflict via store.RegionsConflict,
//     claim availability via store.HasClaimableItem.
//  3. On the first eligible candidate, takes per-named-lock advisory
//     locks (§13.3 step 3b), calls ClaimDispatchRow to do the claimant-
//     guarded UPDATE (§13.3 step 3c), re-evaluates region locks under tx
//     (§13.3 step 3d), then for each non-rebound spec calls
//     store.AcquireLock and inserts a rimsky_lock_holders row (§13.3 step
//     3e). All inside the same tx. COMMIT.
//
// The claim-time inputs the runner aggregates per candidate are described
// by ClaimEligibilityInput below; see that doc-comment for the contract.
//
// Lock-holder reads/writes used by the runner during the acquisition tx
// (Insert, ListByNodeAndStore for rebind, RebindForResume, ListByStoreRegion,
// CountByNamedLock) live on core/store.LockHoldersClient. The queue
// interface does NOT expose those — they are the store layer's
// responsibility per the package import rules. The queue layer only owns
// rimsky_dispatch.
//
// Tag-limit / concurrency_tags semantics are gone. Named locks (a §11.5
// node-template construct) replace concurrency tags. The dispatch row no
// longer stores any per-row eligibility state beyond required_stores +
// executor_name; everything else is node-type-level.
package queue

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/store"
)

// DispatchRequest is the payload for Enqueue.
//
// ExecutorName is empty for native (claim-only) nodes, which the supervisor's
// omnibus runner picks up directly (spec §17.1). It remains empty for
// pure-cascade nodes too, but those never enqueue.
//
// RequiredStores is denormalised from the template's per-node-type
// `nodeRequiredStores(node_type)` at enqueue time (spec §9.6 / §14.2). It
// drives the supervisor-pool specialisation predicate at claim time —
// supervisors only consider rows whose RequiredStores ⊆ AcceptedStores.
type DispatchRequest struct {
	NodeID         shared.UUID
	ExecutorName   string
	RequiredStores []string
	EnqueuedAt     time.Time // may be future-dated for backoff
	// FrameID is the frame this dispatch belongs to (per
	// docs/specs/2026-04-26-frame-resolution-design.md §10.2). Required:
	// rimsky_dispatch.frame_id is NOT NULL. Sourced from
	// rimsky_nodes.frame_id at enqueue time.
	FrameID shared.UUID
}

// SelectCandidatesRequest is the input to SelectCandidates (spec §13.3
// step 1). The supervisor passes its accept-lists; the queue returns
// candidate dispatch rows the supervisor pool is allowed to consider.
type SelectCandidatesRequest struct {
	// AcceptedExecutors is the supervisor's executor accept-list. The
	// dispatch SELECT filters
	// (executor_name = ANY(AcceptedExecutors) OR executor_name IS NULL).
	// IS NULL covers native (claim-only) nodes per §17.1.
	AcceptedExecutors []string

	// AcceptedStores is the supervisor's store accept-list (spec §14.2).
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

// ClaimEligibilityInput is the per-candidate data the runner aggregates
// before committing to a candidate (spec §13.2 / §13.3 step 2).
//
// LockSpecs comes from the in-memory template registry keyed by the
// candidate's node_type — the queue does not look these up itself. The
// runner uses LockSpecs to run the in-Go eligibility checks (named-lock
// count, region conflict, claim availability) and, if every spec is
// eligible, to drive the §13.3 step 3 acquisition path (advisory locks,
// store.AcquireLock calls, lock-holder inserts).
//
// SupervisorID is the candidate-claimant identity (FK target on
// rimsky_dispatch.claimed_by and rimsky_lock_holders.holder_supervisor_id).
type ClaimEligibilityInput struct {
	Candidate    Candidate
	LockSpecs    []store.LockSpec
	SupervisorID string
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

// DispatchQueue is the claimable-work primitive. The Postgres implementation
// at core/queue/postgres/queue.go carries the load-bearing @blessed-invariant
// on ClaimDispatchRow (the dispatch row is the running-window primitive —
// see that file's comment).
//
// SelectCandidates and ClaimDispatchRow are building-block helpers used by
// core/supervisor/runner.go to orchestrate the §13.3 atomic-acquisition
// transaction. The runner owns the pgx.Tx; the queue helpers participate
// in it.
type DispatchQueue interface {
	// Enqueue inserts or refreshes a dispatch row for the given node.
	// On UNIQUE(node_id) conflict the row is updated only when still
	// unclaimed and already eligible (claimed or future-dated rows are
	// left alone). RequiredStores overwrites the prior value.
	Enqueue(ctx context.Context, req DispatchRequest) error

	// SelectCandidates returns up to req.Limit dispatch rows the
	// supervisor pool is allowed to consider, filtered by accept-lists
	// and ordered by enqueued_at ascending. Rows are FOR UPDATE
	// SKIP LOCKED inside the caller's tx; rows the caller does not
	// claim release their locks at tx end. The caller MUST hold an
	// open transaction; implementations return an error when passed
	// a nil tx.
	//
	// This is spec §13.3 step 1. Per-spec lock eligibility (step 2) is
	// the caller's responsibility — see ClaimEligibilityInput.
	SelectCandidates(ctx context.Context, tx pgx.Tx, req SelectCandidatesRequest) ([]Candidate, error)

	// ClaimDispatchRow performs the claimant-guarded UPDATE of
	// rimsky_dispatch.claimed_by from NULL to supervisorID for the
	// given dispatch row, inside the caller's tx. Sets claimed_at and
	// last_heartbeat_at to now(). Returns claimed=true when exactly one
	// row was updated; false when the row was already claimed by someone
	// else (the caller should ROLLBACK and try the next candidate,
	// though under SKIP LOCKED inside the same tx this should not
	// occur — the guard is defensive per spec §13.3 step 3c).
	ClaimDispatchRow(ctx context.Context, tx pgx.Tx, dispatchID shared.UUID, supervisorID string) (claimed bool, err error)

	// Complete deletes a dispatch row. If expectedClaimedBy is non-empty,
	// the delete is guarded (no-op on mismatch).
	Complete(ctx context.Context, dispatchID shared.UUID, expectedClaimedBy string) error

	// Fail deletes a dispatch row with an optional failure reason.
	// Implementations may log the reason but the row is always removed.
	Fail(ctx context.Context, dispatchID shared.UUID, reason string, expectedClaimedBy string) error

	// RemoveForNode deletes any pending dispatch row for a given node.
	// Used when a node is invalidated while queued (claim becomes moot).
	RemoveForNode(ctx context.Context, nodeID shared.UUID, expectedClaimedBy string) error

	// ListOrphanedClaims returns dispatch rows whose last_heartbeat_at is
	// older than cutoff. Spec §13.5 step 1: the dispatch-claim sweep
	// predicate switched from claimed_at to last_heartbeat_at under the
	// redesign so a long-running but heartbeating supervisor is not
	// reaped.
	ListOrphanedClaims(ctx context.Context, cutoff time.Time) ([]shared.DispatchRow, error)

	// ReleaseClaim sets claimed_by=NULL, claimed_at=NULL, last_heartbeat_at=NULL on a dispatch row.
	// If expectedClaimedBy is non-empty, the release is claimant-guarded
	// (no-op on mismatch — protects a fresh supervisor's live claim from a
	// stale sweep).
	ReleaseClaim(ctx context.Context, dispatchID shared.UUID, expectedClaimedBy string) error

	// GetClaimedBy returns current ownership of a dispatch row. Used by the
	// supervisor's verify-before-run invariant (§13.3 step 4 / §17).
	GetClaimedBy(ctx context.Context, dispatchID shared.UUID) (ClaimOwnership, error)

	// RefreshHeartbeat extends rimsky_dispatch.last_heartbeat_at to now()
	// for every row claimed by supervisorID. Spec §13.4 — paired with the
	// lock-holder heartbeat extend; the dispatch sweep predicate filters
	// on this column.
	RefreshHeartbeat(ctx context.Context, supervisorID string) error
}
