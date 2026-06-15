// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package persistence

import (
	"context"
	"errors"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

// ErrRunRowMissing is returned by Queue mutators that key on a
// `rimsky_node_runs.id` and find no matching row. Callers reach those
// mutators after they've already resolved the dispatch row, so a silent
// no-op would mask programmer errors.
var ErrRunRowMissing = errors.New("persistence: rimsky_node_runs row not found")

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
	EnqueuedAt     time.Time // @deliberate: may be future-dated for backoff
	// FrameID is the frame this dispatch belongs to (per
	// docs/history/2026-04-26-frame-resolution-design.md §10.2). Required:
	// rimsky_node_runs.frame_id is NOT NULL. Sourced from
	// rimsky_nodes.frame_id at enqueue time.
	//
	// @blessed-invariant 19: dispatch rows always carry a non-zero frame id.
	FrameID shared.UUID
	// RunScopeID is the RunScope this dispatch belongs to. Non-nullable:
	// every dispatch occurs within a RunScope (main / sub-graph /
	// fan-out partition). The EnqueueInTx NOT EXISTS guard keys on
	// (node_id, run_scope_id) — unambiguous per the new unique index.
	// Per spec .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md.
	//
	// @concept: run-scope
	RunScopeID shared.UUID

	// PriorDispatchID, when non-nil, is the rimsky_node_runs.id of the
	// dispatch this one supersedes (heartbeat-stale recovery, retry,
	// recalculate). Persisted on the new row's prior_dispatch_id column
	// and surfaced to the executor on
	// proto:executor.proto::ExecuteRequest.prior_dispatch_id. Nil for
	// initial dispatches.
	//
	// @concept: run-scope
	PriorDispatchID *shared.UUID

	// PriorDispatchDisposition classifies why PriorDispatchID is set
	// (heartbeat-stale / retry-after-error / recalculate). Stored
	// lower_snake_case on the new row. Empty string when
	// PriorDispatchID is nil. Surfaces on
	// proto:executor.proto::ExecuteRequest.prior_dispatch_disposition.
	//
	// @concept: run-scope
	PriorDispatchDisposition string
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

	// LateBindExecutorProxy is the rimsky.yml-configured proxy peer name
	// for the executor protocol (late_bind_service_proxies.executor).
	// Empty string when no late-bind proxy is configured. Used by the
	// admit-list extension to claim rows whose executor_name appears
	// only in the instance's service_bindings.
	LateBindExecutorProxy string

	// LateBindClaimProducerProxy is the rimsky.yml-configured proxy peer
	// name for the claim_producer protocol. Empty string when none.
	LateBindClaimProducerProxy string

	// CursorEnqueuedAfter / CursorAfterDispatchID form an optional
	// keyset cursor over the selection ordering (enqueued_at, id): only
	// rows strictly after the cursor pair are returned. The runner uses
	// it to page past a head-of-line batch whose candidates were all
	// skipped in Go (e.g. the upstream in-flight gate) — without it, ≥
	// Limit long-gated old candidates would keep their slots every poll
	// and starve younger ungated rows for as long as the gates hold.
	// Zero values = no cursor (start at the head).
	CursorEnqueuedAfter   time.Time
	CursorAfterDispatchID shared.UUID
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

	// PriorDispatchID, when non-nil, identifies the predecessor
	// dispatch this row supersedes (recovery / retry / recalculate).
	// Threaded onto the wire via
	// proto:executor.proto::ExecuteRequest.prior_dispatch_id.
	//
	// @concept: run-scope
	PriorDispatchID *shared.UUID
	// PriorDispatchDisposition is the lower_snake_case classifier
	// stored alongside PriorDispatchID (e.g. "heartbeat_stale",
	// "retry_after_error", "recalculate"). Empty when
	// PriorDispatchID is nil.
	//
	// @concept: run-scope
	PriorDispatchDisposition string
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
// rimsky_node_runs rows. State is one of "" (any) / "pending"
// (claimed_by IS NULL) / "claimed" (claimed_by IS NOT NULL).
// ExecutorName matches rimsky_node_runs.executor_name exactly when set.
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
// runtime/runner.go to orchestrate the §7.3 atomic-acquisition
// transaction. The runner owns the persistence.Tx; the queue helpers
// participate in it.
type Queue interface {
	// @agent-contract: Enqueue inserts or refreshes a dispatch row for the
	// given node. On UNIQUE(node_id) conflict the row is updated only when
	// still unclaimed and already eligible (claimed or future-dated rows
	// are left alone). RequiredStores overwrites the prior value.
	Enqueue(ctx context.Context, req DispatchRequest) error

	// @agent-contract: EnqueueInTx is the tx-taking variant used inside
	// the frame-tick tx. Auto-commit Enqueue calls
	// EnqueueInTx(ctx, req, nil) internally.
	EnqueueInTx(ctx context.Context, req DispatchRequest, tx Tx) error

	// @agent-contract: SelectCandidates returns up to req.Limit dispatch
	// rows the supervisor pool is allowed to consider, filtered by
	// accept-lists and ordered by enqueued_at ascending. Rows are FOR
	// UPDATE SKIP LOCKED inside the caller's tx; rows the caller does not
	// claim release their locks at tx end. The caller MUST hold an open
	// transaction; implementations return an error when passed a nil tx.
	SelectCandidates(ctx context.Context, tx Tx, req SelectCandidatesRequest) ([]Candidate, error)

	// @agent-contract: ListInFlightRunPhases returns, for each node in
	// nodeIDs that has at least one in-flight rimsky_node_runs row (phase
	// pending / active / held / parked) in the given (frame, run scope),
	// the set of distinct phases present. Nodes with no in-flight row are
	// absent from the map. The persistence half of the supervisor's
	// upstream-gating eligibility condition: a stale receiver is not
	// dispatch-eligible while any subscribed upstream has an in-flight
	// run in the same frame, and the gate's pending-cycle tie-breaker
	// must distinguish a merely-pending upstream (itself gated) from a
	// progressing one. Returns an empty map for an empty node set. The
	// caller MUST hold an open transaction (the check shares the
	// candidate-acquisition tx so the answer is consistent with the
	// rows the tx already locked).
	//
	// @concept: wait-set
	// @concept: cascade
	ListInFlightRunPhases(ctx context.Context, tx Tx, nodeIDs []shared.UUID, frameID, runScopeID shared.UUID) (map[shared.UUID][]string, error)

	// @agent-contract: ClaimDispatchRow performs the claimant-guarded
	// UPDATE of rimsky_node_runs.claimed_by from NULL to supervisorID for
	// the given dispatch row, inside the caller's tx. Sets claimed_at and
	// last_heartbeat_at to now(). Returns claimed=true when exactly one
	// row was updated; false when the row was already claimed by someone
	// else.
	ClaimDispatchRow(ctx context.Context, tx Tx, dispatchID shared.UUID, supervisorID string) (claimed bool, err error)

	// @agent-contract: Complete deletes a dispatch row. If
	// expectedClaimedBy is non-empty, the delete is guarded (no-op on
	// mismatch).
	Complete(ctx context.Context, dispatchID shared.UUID, expectedClaimedBy string) error

	// @agent-contract: RemoveForNode retires the in-flight dispatch row
	// for a given (node, run scope). Used when a node is invalidated
	// while queued (claim becomes moot). The (node_id, run_scope_id)
	// keying is unambiguous under the new unique index — no separate
	// disambiguator needed.
	//
	// @concept: run-scope
	RemoveForNode(ctx context.Context, nodeID shared.UUID, runScopeID shared.UUID, expectedClaimedBy string) error

	// @agent-contract: RemoveForNodeInTx is the tx-taking variant. The
	// auto-commit RemoveForNode calls this internally with tx=nil.
	RemoveForNodeInTx(ctx context.Context, nodeID shared.UUID, runScopeID shared.UUID, expectedClaimedBy string, tx Tx) error

	// @agent-contract: ListOrphanedClaims returns dispatch rows whose
	// last_heartbeat_at is older than cutoff.
	ListOrphanedClaims(ctx context.Context, cutoff time.Time) ([]DispatchRow, error)

	// @agent-contract: ReleaseClaim sets claimed_by=NULL, claimed_at=NULL,
	// last_heartbeat_at=NULL on a dispatch row. If expectedClaimedBy is
	// non-empty, the release is claimant-guarded (no-op on mismatch —
	// protects a fresh supervisor's live claim from a stale sweep).
	ReleaseClaim(ctx context.Context, dispatchID shared.UUID, expectedClaimedBy string) error

	// @agent-contract: GetClaimedBy returns current ownership of a
	// dispatch row. Used by the supervisor's verify-before-run invariant
	// (§7.3 step 4 / §17).
	GetClaimedBy(ctx context.Context, dispatchID shared.UUID) (ClaimOwnership, error)

	// @agent-contract: GetDispatchNode returns the node_id of a dispatch
	// row plus its current claim ownership. Used by the supervisor's
	// §12.5 attributes-callback auth path. Returns ClaimOwnership{Kind:
	// "not_found"} when the dispatch row does not exist.
	GetDispatchNode(ctx context.Context, dispatchID shared.UUID) (shared.UUID, ClaimOwnership, error)

	// @agent-contract: RefreshHeartbeat extends
	// rimsky_node_runs.last_heartbeat_at to now() for every row claimed
	// by supervisorID.
	RefreshHeartbeat(ctx context.Context, supervisorID string) error

	// @agent-contract: ListLive returns currently-live dispatch rows (the
	// table holds only rows with no terminal yet — terminals delete the
	// row). Used by the observability dispatches endpoint. Cursor
	// pagination follows the (enqueued_at DESC, id DESC) ordering
	// documented in the spec §1.2.3.
	ListLive(ctx context.Context, filter DispatchListFilter, pag ListPagination) (PaginatedListResult[DispatchRow], error)

	// @agent-contract: CountLive counts currently-live dispatch rows
	// matching filter.
	CountLive(ctx context.Context, filter DispatchListFilter) (int, error)

	// @agent-contract: CountParkedByReason returns counts of
	// currently-parked rimsky_node_runs rows grouped by parked_reason.
	// Empty reason buckets under the literal string "" so callers can
	// disambiguate "not parked" (absent from map) from "parked with
	// no reason" (key=""). Used by the metrics gauge refresher
	// (`rimsky_parked_by_reason`).
	CountParkedByReason(ctx context.Context) (map[string]int, error)

	// @agent-contract: GetByID returns the live dispatch row for id, or
	// nil when no such row exists (e.g. terminal-deleted). Used by the
	// observability /v1/observability/node-runs/{id} endpoint to avoid a
	// full O(N) ListLive scan.
	GetByID(ctx context.Context, id shared.UUID) (*DispatchRow, error)

	// @agent-contract: GetInFlightRunForNode resolves the in-flight
	// `rimsky_node_runs.id` for the (node, run scope) pair. A row is
	// "in-flight" when its phase is one of pending / active / held /
	// parked. Returns (zero, false, nil) when no in-flight row exists.
	//
	// The (node_id, run_scope_id) keying is unambiguous per the
	// uq_node_runs_in_flight_per_run_scope partial-unique index —
	// no disambiguator needed.
	//
	// @concept: run-scope
	GetInFlightRunForNode(ctx context.Context, tx Tx, nodeID, runScopeID shared.UUID) (shared.UUID, bool, error)

	// @agent-contract: ParkActiveInTx transitions a node-run row from
	// phase='active' to phase='parked' under the claimant's id. Persists
	// the park metadata (parked_at, resume_at, parked_reason,
	// session_token) and the payload via inline-or-handle (exactly one is
	// non-empty). Clears claimed_by / claimed_at / last_heartbeat_at so
	// the orphan-claim reaper's `claimed_by IS NOT NULL` predicate
	// excludes the row. Used by E1's applyTerminalPark.
	ParkActiveInTx(ctx context.Context, tx Tx, in ParkActiveInput) error

	// @agent-contract: ListParkedReadyForResume returns parked rows whose
	// resume_at has elapsed (resume_at <= cutoff), ordered by resume_at
	// ascending. Limit caps the per-tick batch. Used by E3's
	// SweepParkedNodes.
	ListParkedReadyForResume(ctx context.Context, cutoff time.Time, limit int) ([]ParkedRow, error)

	// @agent-contract: ListParkedDiagnostic returns currently-parked rows
	// for the admin diagnostics endpoints (G1 / G2). When reasonFilter is
	// non-empty, only rows whose parked_reason equals the filter are
	// returned. Includes the joined instance_id (via rimsky_nodes) so the
	// diagnostics endpoint can group by instance/frame without a second
	// query. Ordered by parked_at ascending.
	ListParkedDiagnostic(ctx context.Context, tx Tx, reasonFilter string) ([]ParkedDiagnosticRow, error)

	// @agent-contract: ListParkedOverdue returns parked rows whose
	// parked_at + max_park_duration_seconds has elapsed. The watchdog
	// path (SweepParkedNodes) uses this to force a park_timeout failure.
	// Limit caps the per-tick batch.
	ListParkedOverdue(ctx context.Context, now time.Time, limit int) ([]ParkedRow, error)

	// @agent-contract: GetParkedByNode returns the parked node-run row
	// for (node, run scope), or nil when no such parked row exists. Used
	// by the admin-invalidate-against-parked path (G3) and by E4 resume
	// dispatch to load the persisted park metadata.
	//
	// @concept: run-scope
	GetParkedByNode(ctx context.Context, nodeID shared.UUID, runScopeID shared.UUID) (*ParkedRow, error)

	// @agent-contract: ResumeParkedInTx transitions a parked row back to
	// phase='pending' (so any eligible supervisor can pick it up — the
	// row's claimed_by is reset to NULL). Park metadata
	// (parked_payload_*, parked_reason, session_token) is preserved so
	// the resume-dispatch path can build ResumeContext from it; the
	// runner clears it via ClearResumeMetadataInTx after a successful
	// dispatch.
	//
	// Used by E3's sweep-driven wake and by the unified invalidate
	// handler (G3) for handler-emitted wakes. Returns resumed=true when
	// exactly one row was updated.
	//
	// wakeReason is persisted on rimsky_node_runs.wake_reason so
	// the resume-dispatch path (LoadResumeMetadataInTx) can attach it
	// to ResumeContext.resume_reason ("deadline_elapsed" |
	// "external_invalidate"). Empty wakeReason persists NULL. Callers
	// record their supervisor id in the parked_resume_started audit
	// event directly — this method does not touch claimed_by.
	ResumeParkedInTx(ctx context.Context, tx Tx, dispatchID shared.UUID, wakeReason string) (resumed bool, err error)

	// @agent-contract: RebindRunFrameInTx updates
	// rimsky_node_runs.frame_id for the given dispatch row to
	// `newFrameID`. Used after a cross-frame parked-run wake so the woken
	// run rejoins the active frame; without the rebind,
	// `GetInFlightRunForNode(node, newFrameID)` won't resolve the woken
	// row and the receiver's wait-set blocker can't bind to its run id.
	// Idempotent: re-binding to the same frame is a no-op.
	//
	// A missing row (no `rimsky_node_runs` row exists for `dispatchID`)
	// is an error: callers reach this primitive after they've already
	// resolved the dispatch row, so a silent no-op would hide
	// programmer errors. Returns `ErrRunRowMissing` so callers can
	// distinguish "row missing" from generic DB errors.
	//
	// Per the 2026-05-20 upstream-refresh cascade extension; supports the
	// parked-upstream branch of `runtime/runner_terminal.go::pullForceRefreshUpstreams`
	// and the standard cascade-subscription path's parked-receiver wake.
	RebindRunFrameInTx(ctx context.Context, tx Tx, dispatchID, newFrameID shared.UUID) error

	// @agent-contract: GetRetryNoProgress returns the current counter
	// value plus the per-row max_retries_without_progress override (NULL
	// → use deployment default). Used by E5 to test the cap.
	GetRetryNoProgress(ctx context.Context, dispatchID shared.UUID) (count int, override *int, err error)

	// @agent-contract: SetRetryNoProgressForNodeInTx writes the
	// carry-forward counter onto the node-run row identified by
	// (node_id, run_scope_id). Used by the retry path: after the
	// original dispatch row is removed and a new one is inserted, the
	// supervisor re-stamps the carried-forward counter so the cap can
	// accumulate across retries.
	//
	// The UPDATE is scoped to `phase = 'pending'` so only the
	// freshly-inserted pending row is touched within the given RunScope.
	//
	// @concept: run-scope
	SetRetryNoProgressForNodeInTx(ctx context.Context, tx Tx, nodeID shared.UUID, runScopeID shared.UUID, count int) error

	// @agent-contract: UpdateDispatchTuningInTx sets the per-row
	// max_park_duration_seconds and max_retries_without_progress
	// denormalized columns at dispatch time from the resolved template
	// DSL. Used by F2/F3 dispatch wiring.
	UpdateDispatchTuningInTx(ctx context.Context, tx Tx, dispatchID shared.UUID, maxParkDurationSeconds *int, maxRetriesWithoutProgress *int) error

	// @agent-contract: LoadResumeMetadataInTx returns the parked metadata
	// that survived the parked → pending transition
	// (parked_payload_inline / parked_payload_handle /
	// parked_payload_handle_backend / parked_reason / session_token).
	// Returns (nil, nil) when the row has no parked metadata (fresh
	// dispatch). Used by E4's resume dispatch.
	LoadResumeMetadataInTx(ctx context.Context, tx Tx, dispatchID shared.UUID) (*ResumeMetadataRow, error)

	// @agent-contract: ClearResumeMetadataInTx clears the
	// parked_payload_* / parked_reason / session_token columns. Called by
	// the runner after a successful resume dispatch so a re-park cycle
	// starts clean.
	ClearResumeMetadataInTx(ctx context.Context, tx Tx, dispatchID shared.UUID) error
}

// ResumeMetadataRow is the parked metadata loaded by
// LoadResumeMetadataInTx for the runner's resume-dispatch path.
type ResumeMetadataRow struct {
	PayloadInline        []byte
	PayloadHandle        string
	PayloadHandleBackend string
	Reason               string
	// ReasonNote is the free-form human annotation persisted alongside
	// the typed Reason (`col:rimsky_node_runs.parked_reason_note`).
	// Inert in rimsky (`concept:inertness structural-inertness discipline).
	ReasonNote   string
	SessionToken string
	// WakeReason carries the WakeReason enum value persisted by
	// ResumeParkedInTx ("deadline_elapsed" | "external_invalidate").
	// Empty when no wake has been recorded.
	WakeReason string
	// ParkedAt is the timestamp recorded when the node-run first
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
// external invalidate). Reason is the snake_case typed enum value
// stored in col:rimsky_node_runs.parked_reason; ReasonNote is the
// free-form human annotation stored in
// col:rimsky_node_runs.parked_reason_note (inert in rimsky).
//
// ReasonLabel is the freeform classification tag persisted on
// col:rimsky_node_runs.parked_reason_label. Spec
// .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
// §Parked-state taxonomy requires this when Reason == "other"; the
// runner-side guard rejects park terminals that violate that rule.
// Inert in rimsky.
type ParkActiveInput struct {
	DispatchID           shared.UUID
	ExpectedClaimedBy    string
	ParkedAt             time.Time
	ResumeAt             time.Time // @deliberate: zero ⇒ NULL (no deadline-based resume)
	Reason               string
	ReasonNote           string
	ReasonLabel          string
	SessionToken         string
	PayloadInline        []byte
	PayloadHandle        string
	PayloadHandleBackend string
}

// ParkedDiagnosticRow is the read-projection used by the admin
// diagnostics endpoints (G1 / G2). Trims the per-row fields to the
// columns the endpoints actually surface.
type ParkedDiagnosticRow struct {
	// DispatchID is the rimsky_node_runs.id of this parked row. Used by
	// downstream per-row probes (GetParkedByNode with a runID) to
	// disambiguate fan-out children sharing a node_id — a SELECT by
	// node_id alone could match any of the in-flight parked siblings.
	DispatchID shared.UUID
	InstanceID string
	NodeID     string
	FrameID    string
	ParkedAt   time.Time
	ResumeAt   time.Time
	Reason     string
	// ReasonNote is the free-form human annotation persisted alongside
	// Reason. Surfaced to the diagnostics endpoint and CLI; inert in
	// rimsky (no rimsky code path inspects it).
	ReasonNote string
}

// ParkedRow is a row returned by ListParkedReadyForResume,
// ListParkedOverdue, and GetParkedByNode. Carries the persisted park
// metadata so the resume-dispatch path (E4) can build ResumeContext.
type ParkedRow struct {
	DispatchID     shared.UUID
	NodeID         shared.UUID
	ExecutorName   string
	RequiredStores []string
	FrameID        shared.UUID
	ParkedAt       time.Time
	ResumeAt       *time.Time
	Reason         string
	// ReasonNote is the free-form human annotation persisted alongside
	// Reason in col:rimsky_node_runs.parked_reason_note. Inert.
	ReasonNote               string
	SessionToken             string
	PayloadInline            []byte
	PayloadHandle            string
	PayloadHandleBackend     string
	MaxParkDurationSeconds   *int
	ConsecutiveRetriesNoProg int
}
