// Package storage defines the core state-table interfaces used by the
// orchestrator. The Postgres implementation lives in core/storage/postgres.
// The rimsky_events table is treated as append-only; the EventStore
// interface reflects that (no update or delete).
package storage

import (
	"context"
	"time"

	"github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/shared"
)

// Tx is an opaque transaction handle. Stores accept an optional tx parameter
// to participate in a caller's transaction. Callers construct a Tx via
// StorageBackend.Transaction.
//
// Implementations embed TxMarker; the unexported field keeps third-party code
// from forging a Tx value, but in-tree implementations (e.g. the postgres
// package) can still satisfy the interface by embedding TxMarker.
type Tx interface {
	isTx()
}

// TxMarker is the zero-cost embeddable marker that concrete Tx implementations
// in this repository embed to satisfy the Tx interface.
type TxMarker struct{}

// isTx satisfies Tx.
func (TxMarker) isTx() {}

// Pagination inputs/outputs.
type ListPagination struct {
	Limit  int    // 0 → default (implementation-defined)
	Cursor string // opaque; empty for first page
}
type PaginatedListResult[T any] struct {
	Rows       []T
	NextCursor string
}

// -------- Templates --------

type TemplateSummary struct {
	ID         shared.UUID
	Name       string
	Version    string
	DeployedAt time.Time
}
type TemplateRow struct {
	TemplateSummary
	Spec node.TemplateSpec
}
type TemplateStore interface {
	Deploy(ctx context.Context, spec node.TemplateSpec, tx Tx) (TemplateSummary, error)
	Get(ctx context.Context, id shared.UUID, tx Tx) (*TemplateRow, error)
	List(ctx context.Context, filter TemplateListFilter, pag ListPagination, tx Tx) (PaginatedListResult[TemplateSummary], error)
	Delete(ctx context.Context, id shared.UUID, tx Tx) error // ErrTemplateInUse if instances reference
}
type TemplateListFilter struct{ Name string }

// -------- Instances --------

type InstanceRow struct {
	ID          shared.UUID
	TemplateID  shared.UUID
	ConsumerKey string
	Params      map[string]any
	CreatedAt   time.Time
}
type InstanceStore interface {
	Create(ctx context.Context, args InstanceCreateInput, tx Tx) (InstanceRow, error)
	Get(ctx context.Context, id shared.UUID, tx Tx) (*InstanceRow, error)
	GetByConsumerKey(ctx context.Context, templateID shared.UUID, consumerKey string, tx Tx) (*InstanceRow, error)
	List(ctx context.Context, filter InstanceListFilter, pag ListPagination, tx Tx) (PaginatedListResult[InstanceRow], error)
	Delete(ctx context.Context, id shared.UUID, tx Tx) error
}
type InstanceCreateInput struct {
	TemplateID  shared.UUID
	ConsumerKey string
	Params      map[string]any
}
type InstanceListFilter struct {
	TemplateID  shared.UUID
	ConsumerKey string
}

// -------- Nodes --------

type NodeRow struct {
	ID                   shared.UUID
	InstanceID           shared.UUID
	NodeType             string
	Executor             string // empty = native (claim-only or pure-cascade)
	ScheduleCron         string // empty = no schedule
	State                shared.NodeState
	Dependencies         []shared.UUID
	CurrentErrorClass    string
	RetryCounter         int
	ActionIndex          int
	LastHeartbeatAt      *time.Time
	AssignedSupervisorID string
	// FrameID is non-nil when the node is part of an in-flight frame
	// (state IN ('stale','running')) or carries the frame_id that
	// transitioned it to the current state. Cleared on terminal-success
	// transition to fresh; preserved on failed.
	// Per docs/specs/2026-04-26-frame-resolution-design.md §10.3.
	FrameID   *shared.UUID
	CreatedAt time.Time
	UpdatedAt time.Time
}
type NodeCreateInput struct {
	ID           shared.UUID
	InstanceID   shared.UUID
	NodeType     string
	Executor     string
	ScheduleCron string
	Dependencies []shared.UUID
}
type NodeStore interface {
	Create(ctx context.Context, in NodeCreateInput, tx Tx) (NodeRow, error)
	Get(ctx context.Context, id shared.UUID, tx Tx) (*NodeRow, error)
	ListByInstance(ctx context.Context, instanceID shared.UUID, tx Tx) ([]NodeRow, error)
	ListByInstancePaged(ctx context.Context, instanceID shared.UUID, pag ListPagination, tx Tx) (PaginatedListResult[NodeRow], error)
	ListReadyForDispatch(ctx context.Context, tx Tx) ([]NodeRow, error)
	ListRunning(ctx context.Context, tx Tx) ([]NodeRow, error)
	ListDependentsOf(ctx context.Context, nodeID shared.UUID, tx Tx) ([]NodeRow, error)
	ListWithStaleHeartbeat(ctx context.Context, cutoff time.Time, tx Tx) ([]NodeRow, error)
	// ListPureCascadeReady: Executor == '' AND State == stale AND deps fresh.
	ListPureCascadeReady(ctx context.Context, tx Tx) ([]NodeRow, error)
	CountByState(ctx context.Context, tx Tx) (map[shared.NodeState]int, error)
	UpdateState(ctx context.Context, id shared.UUID, state shared.NodeState, reason node.TransitionReason, tx Tx) error
	UpdateError(ctx context.Context, id shared.UUID, es node.EvaluatorState, tx Tx) error
	UpdateHeartbeat(ctx context.Context, id shared.UUID, at time.Time, supervisorID string, tx Tx) error
	// SetFrameID writes (or clears) the frame_id column on a node. The
	// scheduler's frame engine sets it at frame-start; the supervisor's
	// terminal-commit path clears it on success and preserves it on
	// failure. Per spec §10.3.
	SetFrameID(ctx context.Context, id shared.UUID, frameID *shared.UUID, tx Tx) error
	ClearSupervisorAssignment(ctx context.Context, id shared.UUID, tx Tx) error
	DeleteByInstance(ctx context.Context, instanceID shared.UUID, tx Tx) error
}

// -------- Lock holders (rimsky_lock_holders) --------

// LockKind discriminates a lock-holder row's payload columns. See spec §9.9.2.
type LockKind string

const (
	LockKindNamed  LockKind = "named"
	LockKindRegion LockKind = "region"
	LockKindClaim  LockKind = "claim"
)

// LockHolderRow mirrors a row of rimsky_lock_holders (spec §9.9.2). Exactly
// one of (LockName) / (StoreName, RegionData) / (StoreName, ClaimID) is
// populated, keyed by LockKind. The CHECK constraint in §9.9.2 enforces this.
type LockHolderRow struct {
	ID                 shared.UUID
	LockKind           LockKind
	LockName           *string // non-nil when LockKind = "named"
	StoreName          *string // non-nil when LockKind in ("region","claim")
	RegionData         []byte  // raw JSONB; non-nil when LockKind = "region"
	ClaimID            *string // non-nil when LockKind = "claim"
	HolderSupervisorID string
	HolderNodeID       shared.UUID
	ClaimedAt          time.Time
	LastHeartbeatAt    time.Time
	ExpiresAt          time.Time
}

// LockHolderInsertInput is the input shape for InsertLockHolder. Callers
// populate the discriminator-specific fields per LockKind; the postgres
// implementation maps to the §9.9.2 CHECK constraint.
type LockHolderInsertInput struct {
	ID                 shared.UUID
	LockKind           LockKind
	LockName           *string
	StoreName          *string
	RegionData         []byte
	ClaimID            *string
	HolderSupervisorID string
	HolderNodeID       shared.UUID
	ExpiresAt          time.Time
}

// LockHoldersStore is the rimsky_lock_holders accessor exposed on
// StorageBackend. The concrete implementation lives in
// core/store/lockholders.go (per spec §16.1) — this interface is the
// supervisor / scheduler / control-api facing surface.
//
// Sweep predicate (spec §13.5 step 2): ListExpired returns rows where
// expires_at < now(). The scheduler's lock-holder sweep iterates these,
// runs store-side ReleaseLock (for claim-kind), then deletes the row
// claimant-guarded on holder_supervisor_id.
type LockHoldersStore interface {
	Insert(ctx context.Context, in LockHolderInsertInput, tx Tx) error
	Get(ctx context.Context, id shared.UUID, tx Tx) (*LockHolderRow, error)
	ListByHolderNode(ctx context.Context, holderNodeID shared.UUID, tx Tx) ([]LockHolderRow, error)
	ListBySupervisor(ctx context.Context, supervisorID string, tx Tx) ([]LockHolderRow, error)
	ExtendHeartbeat(ctx context.Context, supervisorID string, expiresAt time.Time, tx Tx) error
	ListExpired(ctx context.Context, tx Tx) ([]LockHolderRow, error)
	Delete(ctx context.Context, id shared.UUID, expectedSupervisorID string, tx Tx) error
}

// -------- Node attributes (rimsky_node_attributes) --------

// NodeAttributesRow mirrors a row of rimsky_node_attributes (spec §9.9.1).
type NodeAttributesRow struct {
	NodeID     shared.UUID
	RunAttempt int
	Data       map[string]any
	UpdatedAt  time.Time
}

// NodeAttributesStore is the rimsky_node_attributes accessor exposed on
// StorageBackend. Concrete implementation lives in core/attributes/store.go.
//
// Get returns (nil, nil) when the row is absent — absence is a normal
// lifecycle state (the row is created lazily on first dispatch, spec §9.9.1).
//
// Upsert replaces `data` outright; MergeDelta performs a SHALLOW JSONB merge
// (`data || $1::jsonb`) and requires the row to exist (spec §5.7.2).
type NodeAttributesStore interface {
	Get(ctx context.Context, nodeID shared.UUID) (*NodeAttributesRow, error)
	Upsert(ctx context.Context, nodeID shared.UUID, runAttempt int, data map[string]any) error
	MergeDelta(ctx context.Context, nodeID shared.UUID, delta map[string]any) error
}

// -------- Claim holders (rimsky_claim_holders) --------

// ClaimHolderState is the lifecycle state of a rimsky_claim_holders row.
type ClaimHolderState string

const (
	ClaimHolderStateActive    ClaimHolderState = "active"
	ClaimHolderStateCompleted ClaimHolderState = "completed"
)

// ClaimHolderAction is the declared / actual action vocabulary for held
// claims (spec §9.9.3). 'delete_won' is recorded on a sibling row that was
// collapsed by another sibling's winning delete (§5.6.4).
type ClaimHolderAction string

const (
	ClaimHolderActionDelete        ClaimHolderAction = "delete"
	ClaimHolderActionReleaseToBack ClaimHolderAction = "release_to_back"
	ClaimHolderActionReleaseToHead ClaimHolderAction = "release_to_head"
	ClaimHolderActionDeleteWon     ClaimHolderAction = "delete_won"
)

// ClaimHolderRow mirrors a row of rimsky_claim_holders (spec §9.9.3).
//
// ActualAction is nil while State is 'active'; populated when the row
// transitions to 'completed' per the §5.6.4 resolution algorithm.
type ClaimHolderRow struct {
	ID           shared.UUID
	ClaimID      string
	StoreName    string
	HolderNodeID shared.UUID
	OnCommit     ClaimHolderAction
	OnGiveUp     ClaimHolderAction
	ActualAction *ClaimHolderAction
	State        ClaimHolderState
	CreatedAt    time.Time
	CompletedAt  *time.Time
}

// ClaimHolderInsertInput is the input shape for InsertClaimHolder.
type ClaimHolderInsertInput struct {
	ID           shared.UUID
	ClaimID      string
	StoreName    string
	HolderNodeID shared.UUID
	OnCommit     ClaimHolderAction
	OnGiveUp     ClaimHolderAction
	// FrameID is observability-only (spec §10.5): it records which
	// frame the holding node was in when this row was inserted. Reads
	// and the §5.6.4 resolution algorithm do NOT consult this field.
	FrameID *shared.UUID
}

// ClaimHoldersStore is the rimsky_claim_holders accessor exposed on
// StorageBackend.
//
// Sweep predicate (spec §13.5 step 3): the scheduler's claim-holder GC
// queries rimsky_claim_holders directly via raw SQL (see
// core/scheduler/sweep_locks.go::listLeakedClaimHolders) — rows whose
// holder node's current state is 'failed' or 'fresh' AND whose state is
// still 'active'. It then runs the §5.6.4 algorithm with
// actual_action = on_give_up.
type ClaimHoldersStore interface {
	Insert(ctx context.Context, in ClaimHolderInsertInput, tx Tx) error
	Get(ctx context.Context, id shared.UUID, tx Tx) (*ClaimHolderRow, error)
	ListByClaimID(ctx context.Context, claimID string, tx Tx) ([]ClaimHolderRow, error)
	ListByHolderNode(ctx context.Context, holderNodeID shared.UUID, tx Tx) ([]ClaimHolderRow, error)
	ListActiveByClaimID(ctx context.Context, claimID string, tx Tx) ([]ClaimHolderRow, error)
	Complete(ctx context.Context, id shared.UUID, actualAction ClaimHolderAction, tx Tx) error
}

// -------- Events --------

type EventRow struct {
	ID         int64
	InstanceID *shared.UUID
	NodeID     *shared.UUID
	Kind       string
	Payload    map[string]any
	OccurredAt time.Time
}
type EventAppendInput struct {
	InstanceID *shared.UUID
	NodeID     *shared.UUID
	Kind       string
	Payload    map[string]any
	OccurredAt *time.Time // nil → server NOW()
}
type EventListFilter struct {
	InstanceID *shared.UUID
	NodeID     *shared.UUID
	Kind       string
	Since      *time.Time
	Until      *time.Time
}
type EventListResult struct {
	Events     []EventRow
	NextCursor string
}
type EventStore interface {
	Append(ctx context.Context, in EventAppendInput, tx Tx) error
	List(ctx context.Context, filter EventListFilter, pag ListPagination, tx Tx) (EventListResult, error)
	Tail(ctx context.Context, cursor string, limit int, tx Tx) (EventListResult, error)
}

// -------- Schedules --------

type ScheduleRow struct {
	NodeID      shared.UUID
	CronExpr    string
	NextFireAt  time.Time
	LastFiredAt *time.Time
}
type ScheduleRegisterInput struct {
	NodeID     shared.UUID
	CronExpr   string
	NextFireAt time.Time
}
type ScheduleStore interface {
	Register(ctx context.Context, in ScheduleRegisterInput, tx Tx) error
	DueBefore(ctx context.Context, cutoff time.Time, tx Tx) ([]ScheduleRow, error)
	RecordFired(ctx context.Context, nodeID shared.UUID, nextFireAt, firedAt time.Time, tx Tx) error
	ListAll(ctx context.Context, tx Tx) ([]ScheduleRow, error)
	// ForceFire bumps next_fire_at to now() so the next scheduler tick will
	// pick the node up. Used by the admin force-fire endpoint and by the
	// §19.2 smoke fixture to drive a deterministic source-node fire. No-ops
	// silently when no row matches node_id (the route layer treats that as
	// 404 if needed by reading the node first).
	ForceFire(ctx context.Context, nodeID shared.UUID, tx Tx) error
}

// -------- Supervisors --------

type SupervisorRow struct {
	ID                string
	AcceptedExecutors []string
	AcceptedStores    []string
	Concurrency       int
	CallbackHost      string
	CallbackPort      int
	LastHeartbeatAt   time.Time
	ActiveNodeCount   int
	RegisteredAt      time.Time
}
type SupervisorRegisterInput struct {
	ID                string
	AcceptedExecutors []string
	AcceptedStores    []string
	Concurrency       int
	CallbackHost      string
	CallbackPort      int
}
type SupervisorStore interface {
	Register(ctx context.Context, in SupervisorRegisterInput, tx Tx) error
	Heartbeat(ctx context.Context, id string, activeNodeCount int, tx Tx) error
	Get(ctx context.Context, id string, tx Tx) (*SupervisorRow, error)
	List(ctx context.Context, tx Tx) ([]SupervisorRow, error)
	ListStale(ctx context.Context, cutoff time.Time, tx Tx) ([]SupervisorRow, error)
	Unregister(ctx context.Context, id string, tx Tx) error
}

// -------- Backend aggregate --------

type StorageBackend interface {
	Templates() TemplateStore
	Instances() InstanceStore
	Nodes() NodeStore
	LockHolders() LockHoldersStore
	NodeAttributes() NodeAttributesStore
	ClaimHolders() ClaimHoldersStore
	Events() EventStore
	Schedules() ScheduleStore
	Supervisors() SupervisorStore
	Transaction(ctx context.Context, fn func(ctx context.Context, tx Tx) error) error
}
