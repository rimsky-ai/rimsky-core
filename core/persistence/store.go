package persistence

import (
	"context"
	"encoding/json"
	"time"

	"github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/shared"
)

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
//
// Per docs/specs/2026-05-01-control-plane-and-store-lifecycle-design.md
// §1: templates are content-addressed (id is "sha256-<64-hex>" over an
// RFC 8785 JCS-canonicalized spec). State is one of three persisted
// values (registered, deployed, undeployed); deregistered is the
// absent state — i.e., row deleted. Tags live in rimsky_template_tags
// as movable aliases.

// TemplateState is the persisted template lifecycle state.
type TemplateState string

const (
	TemplateStateRegistered TemplateState = "registered"
	TemplateStateDeployed   TemplateState = "deployed"
	TemplateStateUndeployed TemplateState = "undeployed"
)

// TemplateRow mirrors a row of rimsky_templates.
type TemplateRow struct {
	ID           string
	Spec         node.TemplateSpec
	State        TemplateState
	RegisteredAt time.Time
	Source       string // "direct" | future package-manager values
}

type TemplateInsertInput struct {
	ID     string
	Spec   node.TemplateSpec
	State  TemplateState
	Source string
}

type TemplateStore interface {
	Insert(ctx context.Context, in TemplateInsertInput, tx Tx) error
	GetByHash(ctx context.Context, hash string, tx Tx) (*TemplateRow, error)
	List(ctx context.Context, filter TemplateListFilter, pag ListPagination, tx Tx) (PaginatedListResult[TemplateRow], error)
	UpdateState(ctx context.Context, hash string, newState TemplateState, tx Tx) error
	DeleteByHash(ctx context.Context, hash string, tx Tx) error
	LockForUpdate(ctx context.Context, hash string, tx Tx) (*TemplateRow, error)
}
type TemplateListFilter struct {
	State TemplateState // empty = no filter
}

// -------- Template tags --------

type TemplateTagRow struct {
	Tag        string
	TemplateID string // hash
	UpdatedAt  time.Time
}

type TemplateTagsStore interface {
	Upsert(ctx context.Context, tag, templateID string, tx Tx) error
	Get(ctx context.Context, tag string, tx Tx) (*TemplateTagRow, error)
	ListByTemplate(ctx context.Context, templateID string, tx Tx) ([]TemplateTagRow, error)
	List(ctx context.Context, pag ListPagination, tx Tx) (PaginatedListResult[TemplateTagRow], error)
	Delete(ctx context.Context, tag string, tx Tx) (deleted bool, err error)
	CountByTemplate(ctx context.Context, templateID string, tx Tx) (int, error)
}

// -------- Instances --------

type InstanceRow struct {
	ID           shared.UUID
	TemplateHash string  // FK to rimsky_templates.id
	InstanceKey  *string // nullable
	Params       map[string]any
	CreatedAt    time.Time
	TerminatedAt *time.Time // nullable; set at terminal-state detection
}
type InstanceStore interface {
	Create(ctx context.Context, args InstanceCreateInput, tx Tx) (InstanceRow, error)
	Get(ctx context.Context, id shared.UUID, tx Tx) (*InstanceRow, error)
	GetByInstanceKey(ctx context.Context, templateHash string, instanceKey string, tx Tx) (*InstanceRow, error)
	List(ctx context.Context, filter InstanceListFilter, pag ListPagination, tx Tx) (PaginatedListResult[InstanceRow], error)
	Delete(ctx context.Context, id shared.UUID, tx Tx) error
	MarkTerminated(ctx context.Context, id shared.UUID, tx Tx) error
	CountActiveByTemplate(ctx context.Context, templateHash string, tx Tx) (int, error)
	ListTerminatedWithLifecycleRows(ctx context.Context, limit int, tx Tx) ([]InstanceRow, error)
}
type InstanceCreateInput struct {
	ID           shared.UUID
	TemplateHash string
	InstanceKey  *string // nullable
	Params       map[string]any
}
type InstanceListFilter struct {
	TemplateHash string
	InstanceKey  string
}

// -------- Store lifecycle bookkeeping --------

type StoreLifecycleScopeKind string

const (
	StoreLifecycleScopeTemplate StoreLifecycleScopeKind = "template"
	StoreLifecycleScopeInstance StoreLifecycleScopeKind = "instance"
)

type StoreLifecycleState string

const (
	StoreLifecycleStateRegistered StoreLifecycleState = "registered"
	StoreLifecycleStateDeployed   StoreLifecycleState = "deployed"
	StoreLifecycleStateUndeployed StoreLifecycleState = "undeployed"
	StoreLifecycleStateCreated    StoreLifecycleState = "created"
)

type StoreLifecycleRow struct {
	StoreRegistrationName string
	ScopeKind             StoreLifecycleScopeKind
	ScopeID               string
	State                 StoreLifecycleState
	LastEventAt           time.Time
}

type StoreLifecycleStore interface {
	Get(ctx context.Context, storeName string, scopeKind StoreLifecycleScopeKind, scopeID string, tx Tx) (*StoreLifecycleRow, error)
	Upsert(ctx context.Context, row StoreLifecycleRow, tx Tx) error
	Delete(ctx context.Context, storeName string, scopeKind StoreLifecycleScopeKind, scopeID string, tx Tx) error
	DeleteByScope(ctx context.Context, scopeKind StoreLifecycleScopeKind, scopeID string, tx Tx) error
	ListByScope(ctx context.Context, scopeKind StoreLifecycleScopeKind, scopeID string, tx Tx) ([]StoreLifecycleRow, error)
}

// -------- Nodes --------

type NodeRow struct {
	ID                   shared.UUID
	InstanceID           shared.UUID
	NodeType             string
	Executor             string
	ScheduleCron         string
	State                shared.NodeState
	Dependencies         []shared.UUID
	CurrentErrorClass    string
	RetryCounter         int
	ActionIndex          int
	LastHeartbeatAt      *time.Time
	AssignedSupervisorID string
	FrameID              *shared.UUID
	CreatedAt            time.Time
	UpdatedAt            time.Time
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
	ListPureCascadeReady(ctx context.Context, tx Tx) ([]NodeRow, error)
	CountByState(ctx context.Context, tx Tx) (map[shared.NodeState]int, error)
	UpdateState(ctx context.Context, id shared.UUID, state shared.NodeState, reason node.TransitionReason, tx Tx) error
	UpdateError(ctx context.Context, id shared.UUID, es node.EvaluatorState, tx Tx) error
	UpdateHeartbeat(ctx context.Context, id shared.UUID, at time.Time, supervisorID string, tx Tx) error
	SetFrameID(ctx context.Context, id shared.UUID, frameID *shared.UUID, tx Tx) error
	ClearSupervisorAssignment(ctx context.Context, id shared.UUID, tx Tx) error
	DeleteByInstance(ctx context.Context, instanceID shared.UUID, tx Tx) error
	// MarkStaleForCascade is the cascade-from-parent-commit helper used by
	// the supervisor's terminal-complete path. Sets state='stale',
	// frame_id=$1 only for rows currently fresh OR (stale AND frame_id IS
	// NULL). Used in lieu of UpdateState because the cascade target needs
	// the frame_id atomically and the predicate is gated.
	MarkStaleForCascade(ctx context.Context, id shared.UUID, frameID shared.UUID, tx Tx) error
}

// -------- Lock holders (rimsky_lock_holders) --------

type LockKind string

const (
	LockKindNamed  LockKind = "named"
	LockKindRegion LockKind = "region"
)

type LockHolderRow struct {
	ID                 shared.UUID
	LockKind           LockKind
	LockName           *string
	StoreName          *string
	RegionData         json.RawMessage
	Address            json.RawMessage
	Intent             *string
	HolderSupervisorID string
	HolderNodeID       shared.UUID
	ClaimedAt          time.Time
	LastHeartbeatAt    time.Time
	ExpiresAt          time.Time
	FrameID            *shared.UUID
}

type LockHolderInsertInput struct {
	ID                 shared.UUID
	LockKind           LockKind
	LockName           *string
	StoreName          *string
	RegionData         json.RawMessage
	Address            json.RawMessage
	Intent             *string
	HolderSupervisorID string
	HolderNodeID       shared.UUID
	ExpiresAt          time.Time
	FrameID            *shared.UUID
}

// LockHoldersStore is the rimsky_lock_holders accessor exposed on Store.
// The supervisor / scheduler / control-api facing surface for the lock
// ledger (per blessed-invariant 9a — `rimsky_lock_holders` is the sole
// authority for lock state).
type LockHoldersStore interface {
	Insert(ctx context.Context, in LockHolderInsertInput, tx Tx) error
	UpdateAddress(ctx context.Context, id shared.UUID, supervisorID string, address json.RawMessage, tx Tx) error
	Get(ctx context.Context, id shared.UUID, tx Tx) (*LockHolderRow, error)
	ListByHolderNode(ctx context.Context, holderNodeID shared.UUID, tx Tx) ([]LockHolderRow, error)
	ListBySupervisor(ctx context.Context, supervisorID string, tx Tx) ([]LockHolderRow, error)
	ExtendHeartbeat(ctx context.Context, supervisorID string, expiresAt time.Time, tx Tx) error
	ListExpired(ctx context.Context, tx Tx) ([]LockHolderRow, error)
	Delete(ctx context.Context, id shared.UUID, expectedSupervisorID string, tx Tx) error

	// CountByNamedLock returns the number of currently-held named-lock
	// rows for the given lock name. Used by the supervisor's named-lock
	// counting-mode eligibility check inside the acquisition tx.
	CountByNamedLock(ctx context.Context, lockName string, tx Tx) (int, error)

	// ListByStoreRegion returns all lock-holder rows for a given store
	// name. The supervisor uses this for the in-Go region-conflict check
	// inside the acquisition tx (byte-equal on region_data per spec §7.7).
	ListByStoreRegion(ctx context.Context, storeName string, tx Tx) ([]LockHolderRow, error)

	// DeleteIfExpired claimant-guards a delete on (id, supervisor_id,
	// expires_at). Returns true when the row was deleted; false otherwise.
	// Used by the orphan-reaper sweep.
	DeleteIfExpired(ctx context.Context, id shared.UUID, supervisorID string, tx Tx) (bool, error)

	// LockForUpdate runs SELECT ... FOR UPDATE on the lock-holder row.
	// Used by core/supervisor/auto_terminal.go::CheckAndFireResolution to
	// serialize auto-terminal resolution per blessed-invariant 13.
	// Returns (nil, nil) when the row does not exist (already deleted by
	// a prior resolution).
	LockForUpdate(ctx context.Context, id shared.UUID, tx Tx) (*LockHolderRow, error)

	// UpdateRegion writes a new region_data to a region-kind row,
	// claimant-guarded on supervisorID. Used by the supervisor's
	// acquireClaim path when the store-chosen region differs from the
	// substituted-selector region the supervisor wrote at INSERT time.
	UpdateRegion(ctx context.Context, id shared.UUID, supervisorID string, region json.RawMessage, tx Tx) error
}

// -------- Node attributes --------

type NodeAttributesRow struct {
	NodeID     shared.UUID
	RunAttempt int
	Data       map[string]any
	UpdatedAt  time.Time
}

// NodeAttributesStore is the rimsky_node_attributes accessor exposed on Store.
//
// Get returns (nil, nil) when the row is absent — absence is a normal
// lifecycle state (the row is created lazily on first dispatch).
//
// Upsert replaces `data` outright; MergeDelta performs a SHALLOW JSONB
// merge and requires the row to exist (returns wrapped ErrNotFound when
// absent — both drivers wrap persistence.ErrNotFound, so callers may
// `errors.Is(err, persistence.ErrNotFound)` regardless of driver).
type NodeAttributesStore interface {
	Get(ctx context.Context, nodeID shared.UUID, tx Tx) (*NodeAttributesRow, error)
	Upsert(ctx context.Context, nodeID shared.UUID, runAttempt int, data map[string]any, tx Tx) error
	MergeDelta(ctx context.Context, nodeID shared.UUID, delta map[string]any, tx Tx) error
}

// -------- Claim holders (rimsky_claim_holders) --------

type ClaimHolderState string

const (
	ClaimHolderStateActive    ClaimHolderState = "active"
	ClaimHolderStateCompleted ClaimHolderState = "completed"
	ClaimHolderStateFailed    ClaimHolderState = "failed"
)

type ClaimHolderRow struct {
	ID           shared.UUID
	LockHolderID shared.UUID
	HolderNodeID shared.UUID
	State        ClaimHolderState
	CompletedAt  *time.Time
}

type ClaimHolderInsertInput struct {
	ID           shared.UUID
	LockHolderID shared.UUID
	HolderNodeID shared.UUID
	FrameID      *shared.UUID
}

type ClaimHoldersStore interface {
	Insert(ctx context.Context, in ClaimHolderInsertInput, tx Tx) error
	Get(ctx context.Context, id shared.UUID, tx Tx) (*ClaimHolderRow, error)
	ListByLockHolderID(ctx context.Context, lockHolderID shared.UUID, tx Tx) ([]ClaimHolderRow, error)
	ListByHolderNode(ctx context.Context, holderNodeID shared.UUID, tx Tx) ([]ClaimHolderRow, error)
	ListActiveByLockHolderID(ctx context.Context, lockHolderID shared.UUID, tx Tx) ([]ClaimHolderRow, error)
	Complete(ctx context.Context, id shared.UUID, state ClaimHolderState, tx Tx) error
	CompleteByLockHolderAndNode(ctx context.Context, lockHolderID, holderNodeID shared.UUID, state ClaimHolderState, tx Tx) error
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

// -------- Frames --------
//
// FrameStore is the persistence surface the frame engine (core/frame) talks
// to. Methods mirror the SQL operations in core/frame/{engine,producer}.go;
// the engine itself stays in core/frame/ and orchestrates these calls.
//
// Per spec §3.5.
//
// Frame state vocabulary mirrors the rimsky_frames.state column.
type FrameState string

const (
	FrameStateQueued    FrameState = "queued"
	FrameStateRunning   FrameState = "running"
	FrameStateCompleted FrameState = "completed"
	FrameStateFailed    FrameState = "failed"
)

// FrameMode mirrors the per-template `frame_resolution` setting.
type FrameMode string

const (
	FrameModeCoalesce    FrameMode = "coalesce"
	FrameModeSerialQueue FrameMode = "serial_queue"
)

// FramePending identifies a running frame whose nodes have all left
// stale/running, returned by ListRunningFramesNoPendingNodes.
type FramePending struct {
	FrameID    shared.UUID
	InstanceID shared.UUID
}

// FrameQueuedReady identifies a queued frame ready to be promoted to
// running, returned by ListQueuedFramesReadyToStart. SourceNodeIDs is the
// list of node IDs that originated the frame.
type FrameQueuedReady struct {
	FrameID       shared.UUID
	InstanceID    shared.UUID
	SourceNodeIDs []shared.UUID
}

// FrameStuck identifies a running frame past its timeout with no claimed
// dispatches and at least one stale/running node, returned by
// ListStuckRunningFrames.
type FrameStuck struct {
	FrameID        shared.UUID
	InstanceID     shared.UUID
	FrameTimeoutMs int64
}

// OrphanFrameDispatch identifies a dispatch row whose owning frame has
// already reached terminal state, returned by ListOrphanFrameDispatches.
type OrphanFrameDispatch struct {
	DispatchID shared.UUID
	ClaimedBy  string
	FrameID    shared.UUID
}

type FrameStore interface {
	// ---- Frame-end detection (engine.runFrameEndDetection) ----

	// ListRunningFramesNoPendingNodes returns running frames whose nodes
	// in the same (instance_id, frame_id) scope are all out of
	// stale/running.
	ListRunningFramesNoPendingNodes(ctx context.Context, tx Tx) ([]FramePending, error)

	// HasFailedNode returns true when any rimsky_nodes row for the given
	// (instanceID, frameID) is in state='failed'.
	HasFailedNode(ctx context.Context, instanceID, frameID shared.UUID, tx Tx) (bool, error)

	// MarkRunningFrameTerminal flips a running frame to its terminal state
	// (completed|failed) and stamps ended_at=now(). Returns transitioned=true
	// when the row moved (i.e., it was still 'running'); false if another
	// replica beat us to it.
	MarkRunningFrameTerminal(ctx context.Context, frameID shared.UUID, finalState FrameState, tx Tx) (transitioned bool, err error)

	// MarkInstanceTerminatedIfDone sets rimsky_instances.terminated_at=now()
	// when the terminal predicate holds (no queued/running frames, no
	// stale/running nodes for the instance) and terminated_at IS NULL.
	// Idempotent set-once.
	MarkInstanceTerminatedIfDone(ctx context.Context, instanceID shared.UUID, tx Tx) error

	// ---- Advance queued (engine.runAdvanceQueued) ----

	// ListQueuedFramesReadyToStart returns at most one queued frame per
	// instance — the oldest queued — for instances that have no currently-
	// running frame.
	ListQueuedFramesReadyToStart(ctx context.Context, tx Tx) ([]FrameQueuedReady, error)

	// PromoteQueuedFrameToRunning flips a queued frame to running and
	// stamps started_at=now(). Returns transitioned=true when exactly one
	// row moved.
	PromoteQueuedFrameToRunning(ctx context.Context, frameID shared.UUID, tx Tx) (transitioned bool, err error)

	// MarkSourceNodeStale flips a frame's source node to stale-with-frame_id.
	// Accepts the in-bounds states only: fresh, failed, or stale-with-NULL-
	// frame_id. Returns matched=true when exactly one row moved; false when
	// the node is out of bounds (e.g., already running under a different
	// frame).
	MarkSourceNodeStale(ctx context.Context, instanceID, nodeID, frameID shared.UUID, tx Tx) (matched bool, err error)

	// ---- Stuck frame reaper (engine.runReapStuckFrames) ----

	// ListStuckRunningFrames returns running frames past their timeout
	// with no claimed dispatches and at least one stale/running node.
	ListStuckRunningFrames(ctx context.Context, tx Tx) ([]FrameStuck, error)

	// FailAllPendingNodes flips every stale/running node for the instance
	// to state='failed' and stamps updated_at=now().
	FailAllPendingNodes(ctx context.Context, instanceID shared.UUID, tx Tx) error

	// ---- Orphan dispatch reaper (engine.runReapOrphanFrameDispatches) ----

	// ListOrphanFrameDispatches returns dispatch rows whose claim is
	// non-NULL but whose owning frame has reached terminal state.
	ListOrphanFrameDispatches(ctx context.Context, tx Tx) ([]OrphanFrameDispatch, error)

	// ---- Producer (frame.EnqueueOrCoalesce) ----

	// LookupFrameMode reads (frame_resolution, frame_timeout_ms) for the
	// instance's template. Returns ("", 0, sql.ErrNoRows) when the instance
	// is missing. Empty mode surfaces as a validation error in the caller.
	LookupFrameMode(ctx context.Context, instanceID shared.UUID, tx Tx) (mode FrameMode, frameTimeoutMs int64, err error)

	// EnqueueSerialFrame inserts a queued serial_queue frame with one
	// source node and returns the new frame_id.
	EnqueueSerialFrame(ctx context.Context, instanceID, sourceNodeID shared.UUID, frameTimeoutMs int64, tx Tx) (shared.UUID, error)

	// EnqueueCoalesceFrame inserts a queued coalesce frame, or appends the
	// source node to an existing pending coalesce row for the instance.
	// Returns the frame_id of the row that received the source.
	EnqueueCoalesceFrame(ctx context.Context, instanceID, sourceNodeID shared.UUID, frameTimeoutMs int64, tx Tx) (shared.UUID, error)
}

// -------- Store umbrella --------

type Store interface {
	Templates() TemplateStore
	TemplateTags() TemplateTagsStore
	Instances() InstanceStore
	StoreLifecycle() StoreLifecycleStore
	Nodes() NodeStore
	LockHolders() LockHoldersStore
	NodeAttributes() NodeAttributesStore
	ClaimHolders() ClaimHoldersStore
	Events() EventStore
	Schedules() ScheduleStore
	Supervisors() SupervisorStore
	Frames() FrameStore
	Transaction(ctx context.Context, fn func(ctx context.Context, tx Tx) error) error
}
