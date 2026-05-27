// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package persistence

import (
	"context"
	"encoding/json"
	"time"

	"github.com/rimsky-ai/rimsky-core/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/foundation/spec"
)

// LockKind enumerates the two flavours of rimsky_claim_handles row.
type LockKind string

const (
	LockKindNamed LockKind = "named"
	// LockKindScope is the on-the-wire / on-disk lock kind for claim-
	// scope rows. Post-2026-05-22 ClaimScope rename: the enum value
	// stored in rimsky_claim_handles.lock_kind is 'claim_scope' (see
	// migration 009-claim-scope-rename.sql). The Go-level constant
	// name `LockKindScope` is preserved for ergonomic call sites but
	// its string value is 'claim_scope' so the CHECK constraint on
	// rimsky_claim_handles passes.
	LockKindScope LockKind = "claim_scope"
)

// ClaimHandleRow mirrors a row of rimsky_claim_handles.
type ClaimHandleRow struct {
	ID             shared.UUID     `json:"claim_id"`
	LockKind       LockKind        `json:"lock_kind"`
	LockName       *string         `json:"lock_name,omitempty"`
	ProducerName   *string         `json:"producer_name,omitempty"`
	ClaimScopeData json.RawMessage `json:"claim_scope_data,omitempty"`
	// Address carries `json:"-"` so the observability handlers can
	// pass *ClaimHandleRow to writeJSON without leaking store-supplied
	// claim address bytes (spec §1.3 / blessed-invariant 20). Scope
	// is exposed because operators legitimately need to see what
	// scope a claim covers; address is opaque to rimsky and meant
	// for the store/executor only.
	Address json.RawMessage `json:"-"`
	Intent  *string         `json:"intent,omitempty"`
	// HolderSupervisorID is the supervisor id currently bracketing this
	// claim. Nil iff `State != active` per the
	// `rimsky_claim_handles_active_has_holder` /
	// `rimsky_claim_handles_inactive_has_no_holder` CHECK pair (migration
	// 009). The pointer-vs-empty-string distinction is load-bearing for
	// `@blessed-invariant 4` (claimant-guarded release): callers compare
	// `*row.HolderSupervisorID == args.SupervisorID`, so a NULL row
	// scanned as "" would silently bypass the guard if compared against an
	// also-empty supervisor id. Active rows always carry a non-nil pointer
	// (the active-has-holder CHECK rejects active+NULL); the orphan
	// reaper, terminal-decision cancel walker, and auto-terminal chain
	// guard against nil before dereferencing.
	HolderSupervisorID *string      `json:"holder_supervisor_id,omitempty"`
	HolderNodeID       shared.UUID  `json:"holder_node_id"`
	ClaimedAt          time.Time    `json:"claimed_at"`
	LastHeartbeatAt    time.Time    `json:"last_heartbeat_at"`
	ExpiresAt          time.Time    `json:"expires_at"`
	FrameID            *shared.UUID `json:"frame_id,omitempty"`
	// RealizedWriteSemantics is the per-claim semantics returned by
	// ClaimProducer.Open. Persisted on the lock-holder row so the
	// claim-scope-conflict check (runtime/runner_acquire.go::
	// evaluateClaimScopeConflict) can apply ModeCoexists without re-dialing
	// the producer. Empty for named-lock rows.
	RealizedWriteSemantics string `json:"realized_write_semantics,omitempty"`
	// NodeRunID is the parent node-run this claim handle
	// belongs to (FK rimsky_claim_handles.node_run_id; CASCADE
	// delete on node-run removal).
	NodeRunID *shared.UUID `json:"node_run_id,omitempty"`
	// IsHeld marks claims that persist past the node-run's
	// active terminal until the holding-subgraph completes (auto-
	// terminal mechanism per foundation contract §5.5).
	IsHeld bool `json:"is_held"`
	// ParentClaimHandleID is the parent claim handle in a sub-claim
	// tree (fan-out parent → leaf sub-claims). NULL for root claims.
	// FK rimsky_claim_handles.parent_claim_handle_id with
	// ON DELETE SET NULL so sub-claim rows outlive parent during
	// auto-terminal staging. Spec
	// .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
	// §Recursive claim-tree resolution.
	ParentClaimHandleID *shared.UUID `json:"parent_claim_handle_id,omitempty"`
	// Lifetime is "subgraph" (default) or "durable". Durable claims
	// persist past auto-terminal as state='committed' rows until
	// instance termination explicitly Releases them.
	//
	// @concept: claim-lifetime
	Lifetime spec.ClaimLifetime `json:"lifetime,omitempty"`
	// State: rimsky_claim_handles.state enum. Active during the
	// holding-subgraph run; transitions to committed / abandoned at
	// Promote. Post-Stage-4 of the claim-handle state-column refactor,
	// State is the sole source of truth for terminal disposition (the
	// pre-Stage-4 `held_durable` dual-write column has been retired).
	//
	// @concept: claim-handle
	State spec.ClaimHandleState `json:"state,omitempty"`
	// ResolvedAt: timestamp the row exited 'active'. NULL while
	// State == active. Used by the retention sweep cutoff predicate
	// (Stage 3+).
	ResolvedAt *time.Time `json:"resolved_at,omitempty"`
	// VersionID is the DataProcessing-producer-returned canonical
	// version identifier persisted on Commit for durable claims. Empty
	// for non-DataProcessing producers or pre-Commit rows. Inert in
	// rimsky (@blessed-invariant 20-class).
	VersionID string `json:"version_id,omitempty"`
	// ProducerCandidateHandle is the per-sub-claim handle returned by
	// `DataProcessing.BeginCandidate`. Used by the leaf-dispatch path
	// (`runtime/runner_dispatch.go`) to populate
	// `ExecuteRequest.StoreHandle.CandidateHandle`. Inert in rimsky.
	ProducerCandidateHandle []byte `json:"-"`
	// AggregationPolicy is the snapshotted parent-aggregation policy
	// for fan-out parents. Set at parent claim_handle Insert time when
	// the row represents a fan-out parent (sub-claim children expected);
	// NULL on leaf / non-fan-out claim handles. Drives
	// `runtime/auto_terminal.go::resolveParentClaimChain`'s Commit /
	// Abandon decision over the children's outcomes.
	AggregationPolicy json.RawMessage `json:"aggregation_policy,omitempty"`
	// ExpectedChildrenCount is the number of sub-claim children
	// expected for a fan-out parent. Bumped by `AcquireSubClaims` per
	// sub-scope INSERT. The recursive walker compares it against
	// committed+abandoned counts to decide whether all children have
	// resolved. Zero on leaf / non-fan-out claim handles (no recursion
	// reaches them).
	ExpectedChildrenCount int `json:"expected_children_count,omitempty"`
	// CommittedChildrenCount + AbandonedChildrenCount accumulate per-child
	// outcomes as sub-claims resolve. Bumped atomically inside the same
	// tx as the child's terminal Delete; read inside the parent's
	// SELECT … FOR UPDATE so the aggregate decision is consistent.
	CommittedChildrenCount int `json:"committed_children_count,omitempty"`
	AbandonedChildrenCount int `json:"abandoned_children_count,omitempty"`
}

// ClaimHandleInsertInput is the per-row input for Insert.
type ClaimHandleInsertInput struct {
	ID                     shared.UUID
	NodeRunID              *shared.UUID // FK to rimsky_node_runs.id (nullable for legacy/orphan paths)
	LockKind               LockKind
	LockName               *string
	ProducerName           *string
	ClaimScopeData         json.RawMessage
	Address                json.RawMessage
	Intent                 *string
	HolderSupervisorID     string
	HolderNodeID           shared.UUID
	ExpiresAt              time.Time
	FrameID                *shared.UUID
	RealizedWriteSemantics string // empty for named-lock rows
	// IsHeld marks claims that persist past the active terminal of
	// the owning node-run. Computed from the holding-subgraph
	// declarations at acquisition time; auto-terminal fires aggregate-
	// outcome resolution when all rimsky_claim_holders rows for a held
	// claim_handle reach a non-active state.
	IsHeld bool
	// ParentClaimHandleID is the sub-claim parent in a fan-out tree.
	// NULL for root claims (the standard case); set for fan-out
	// children produced by `ClaimProducer.SplitScope`. Spec
	// §Recursive claim-tree resolution.
	ParentClaimHandleID *shared.UUID
	// Lifetime is "subgraph" (default; the row drops at auto-terminal)
	// or "durable" (the row persists as state='committed' past
	// auto-terminal). Empty defaults to "subgraph".
	//
	// @concept: claim-lifetime
	Lifetime spec.ClaimLifetime
	// ProducerCandidateHandle is the bytes returned by
	// `DataProcessing.BeginCandidate` per sub-claim. Empty for
	// non-DataProcessing producers and non-fan-out claims.
	ProducerCandidateHandle []byte
	// AggregationPolicy is the parent-aggregation policy snapshot at
	// fan-out parent acquisition time. Encoded via
	// `MarshalAggregationPolicy`. Nil bytes / empty policy → NULL on the
	// row (the recursive walker defaults to `strict` semantics when the
	// column is NULL). Spec
	// .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
	// §State aggregation rules.
	AggregationPolicy json.RawMessage
}

// ClaimHandleTable is the rimsky_claim_handles accessor exposed on Store.
// The supervisor / scheduler / control-api facing surface for the lock
// ledger (per blessed-invariant 9a — `rimsky_claim_handles` is the sole
// authority for lock state).
type ClaimHandleTable interface {
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

	// ListByProducerClaimScope returns all lock-holder rows for a given
	// producer name. The supervisor uses this for the in-Go claim-scope-
	// conflict check inside the acquisition tx (byte-equal on
	// claim_scope_data per spec §7.7).
	ListByProducerClaimScope(ctx context.Context, producerName string, tx Tx) ([]ClaimHandleRow, error)

	// DeleteIfExpired claimant-guards a delete on (id, supervisor_id,
	// expires_at). Returns true when the row was deleted; false otherwise.
	// Used by the orphan-reaper sweep.
	DeleteIfExpired(ctx context.Context, id shared.UUID, supervisorID string, tx Tx) (bool, error)

	// LockForUpdate runs SELECT ... FOR UPDATE on the lock-holder row.
	// Used by runtime/auto_terminal.go::CheckAndFireResolution to
	// serialize auto-terminal resolution per blessed-invariant 13.
	// Returns (nil, nil) when the row does not exist (already deleted by
	// a prior resolution).
	LockForUpdate(ctx context.Context, id shared.UUID, tx Tx) (*ClaimHandleRow, error)

	// UpdateClaimScope writes a new claim_scope_data to a claim-scope-kind row,
	// claimant-guarded on supervisorID. Used by the supervisor's
	// acquireClaim path when the store-chosen claim-scope differs from the
	// substituted-selector claim-scope the supervisor wrote at INSERT time.
	UpdateClaimScope(ctx context.Context, id shared.UUID, supervisorID string, scope json.RawMessage, tx Tx) error

	// UpdateRealizedWriteSemantics writes the per-claim ClaimProducer-
	// declared realized_write_semantics on a claim-scope-kind row,
	// claimant-guarded on supervisorID. Called after ClaimProducer.Open
	// returns; the value is then consumed by the in-Go claim-scope-conflict
	// check (runtime/runner_acquire.go::evaluateClaimScopeConflict)
	// without re-dialing the producer.
	UpdateRealizedWriteSemantics(ctx context.Context, id shared.UUID, supervisorID string, ws string, tx Tx) error

	// ListForObservability returns rows matching the filter, paginated
	// by claimed_at DESC. Used by the observability /v1/observability/
	// lock-holders browse endpoint (spec §1.2.4).
	ListForObservability(ctx context.Context, filter LockHolderListFilter, pag ListPagination, tx Tx) (PaginatedListResult[ClaimHandleRow], error)

	// GetByFrameAndNode returns the lock-holder row whose holder_node_id
	// equals nodeID and frame_id equals frameID. Used by the
	// observability /v1/observability/node-runs/{id} endpoint to
	// resolve node-run → claim_id without a full per-node scan. Returns
	// (nil, nil) when no matching row exists.
	GetByFrameAndNode(ctx context.Context, nodeID shared.UUID, frameID shared.UUID, tx Tx) (*ClaimHandleRow, error)

	// ListChildClaimHandles returns claim_handles rows whose
	// parent_claim_handle_id equals parentID. Used by the recursive
	// claim-tree resolution path (`runtime/auto_terminal.go::
	// CheckAndFireResolution`) to confirm all sub-claims have
	// terminated before firing the parent's resolution.
	// Spec §Recursive claim-tree resolution.
	ListChildClaimHandles(ctx context.Context, parentID shared.UUID, tx Tx) ([]ClaimHandleRow, error)

	// SetVersionID persists the DataProcessing-producer-returned
	// canonical version_id from `ClaimProducer.Commit`. Claimant-guarded.
	// Inert in rimsky (@blessed-invariant 20-class).
	SetVersionID(ctx context.Context, id shared.UUID, supervisorID string, versionID string, tx Tx) error

	// DeleteResolvedOlderThan deletes terminal (non-active)
	// claim_handle rows whose `resolved_at` is older than the cutoff,
	// skipping the committed-durable asset surface (which is released
	// only by `ReleaseHeldDurableClaims` or the operator `DELETE
	// /assets/{alias}` handler). Returns the deleted-rows count.
	//
	// Predicate:
	//
	//   state IN ('committed', 'abandoned')
	//   AND (state = 'abandoned' OR lifetime = 'subgraph')
	//   AND resolved_at < cutoff
	//   AND holder_supervisor_id IS NULL
	//
	// Absence-guarded: the post-Stage-4 CHECK constraint nulls
	// `holder_supervisor_id` whenever `state` exits `'active'`, so the
	// IS-NULL clause is structurally satisfied for every non-active row.
	// Serialized across replicas via the scheduler-tick advisory lock
	// at the caller site.
	//
	// @blessed-invariant 4 (post-refactor): non-active-row deletions
	// are guarded by absence + the row-discovery query filter.
	// @concept: claim-handle
	// @concept: claim-lifetime
	DeleteResolvedOlderThan(ctx context.Context, cutoff time.Time) (int, error)

	// DeleteResolved deletes a non-active claim_handle row (state ∈
	// {committed, abandoned}). Absence-guarded: the post-Stage-4 CHECK
	// constraint nulls `holder_supervisor_id` whenever `state` exits
	// `'active'`, so no per-row claimant-guard is meaningful. Used by
	// the asset Release path
	// (`runtime/instance_termination.go::ReleaseHeldDurableClaims`,
	// `control/controlapi/assets.go::DELETE /instances/{id}/assets/{alias}`)
	// after `ClaimProducer.Release` succeeds. Returns
	// spec.ErrIllegalClaimHandleTransition when the row was active
	// (a stricter caller can use this to fail loudly rather than
	// silently no-op a Delete that would otherwise NULL live
	// ownership).
	//
	// @blessed-invariant 4 (post-refactor): non-active-row deletions
	// are guarded by absence + the row-discovery query filter.
	DeleteResolved(ctx context.Context, id shared.UUID, tx Tx) error

	// Promote transitions a claim handle from active to committed or
	// abandoned. Claimant-guarded:
	//
	//   WHERE id = $1 AND state = 'active' AND holder_supervisor_id = $2
	//
	// The UPDATE sets state = newState, holder_supervisor_id = NULL,
	// resolved_at = now() in the same statement. Returns
	// spec.ErrIllegalClaimHandleTransition on affected-rows = 0 (row not
	// in active state, or supervisor mismatch).
	//
	// @blessed-invariant 4 (post-refactor): active-row mutations are
	// claimant-guarded.
	Promote(ctx context.Context, id shared.UUID, supervisorID string,
		newState spec.ClaimHandleState, tx Tx) error

	// ListByState returns claim-handle rows currently in the given state.
	// Used by the retention sweep (state ∈ {committed, abandoned}) and
	// by readers that need state-filtered listings.
	ListByState(ctx context.Context, state spec.ClaimHandleState, tx Tx) ([]ClaimHandleRow, error)

	// ListByInstanceAndState returns rows joined through
	// holder_node_id → rimsky_nodes filtered by instance + state +
	// lifetime. The asset query calls
	// `ListByInstanceAndState(instance, committed, durable)`.
	ListByInstanceAndState(ctx context.Context, instanceID shared.UUID,
		state spec.ClaimHandleState, lifetime spec.ClaimLifetime, tx Tx) ([]ClaimHandleRow, error)

	// SetAggregationPolicy writes the parent-claim aggregation policy
	// snapshot on a claim_handle row. Called once per fan-out parent at
	// sub-claim acquisition time so the recursive walker
	// (`runtime/auto_terminal.go::resolveParentClaimChain`) can compute a
	// true aggregate Commit/Abandon decision across all children — not
	// just the just-resolved seedOutcome. Claimant-guarded on
	// supervisorID. Empty `policy` bytes clear the column. Spec
	// §Recursive scope partitioning.
	SetAggregationPolicy(ctx context.Context, id shared.UUID, supervisorID string, policy json.RawMessage, tx Tx) error

	// BumpExpectedChildrenCount adds `delta` to the parent's
	// `expected_children_count`. Called by `AcquireSubClaims` per
	// sub-claim INSERT so the recursive walker can detect "all children
	// resolved" via committed+abandoned == expected. Claimant-guarded on
	// supervisorID. Spec §Recursive scope partitioning.
	BumpExpectedChildrenCount(ctx context.Context, id shared.UUID, supervisorID string, delta int, tx Tx) error

	// BumpChildOutcomeCount adds `delta` to either
	// `committed_children_count` (when outcome == "commit") or
	// `abandoned_children_count` (when outcome == "abandon"). Called by
	// the recursive walker before firing the parent's terminal verb;
	// runs inside the parent's SELECT … FOR UPDATE so the aggregate
	// decision sees a consistent counter view. Claimant-guarded on
	// supervisorID. The outcome string is restricted to {"commit","abandon"}
	// — any other value returns an error.
	BumpChildOutcomeCount(ctx context.Context, id shared.UUID, supervisorID string, outcome string, delta int, tx Tx) error
}

// LockHolderListFilter is the observability browse filter for the
// rimsky_claim_handles endpoint (spec §1.2.4).
type LockHolderListFilter struct {
	ProducerName     string
	HolderNodeID     *shared.UUID
	HolderSupervisor string
	InstanceID       *shared.UUID
	NodeType         string
}
