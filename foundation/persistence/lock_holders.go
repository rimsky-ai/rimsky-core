package persistence

import (
	"context"
	"encoding/json"
	"time"

	"github.com/fallguy/rimsky/foundation/cascade"
	"github.com/fallguy/rimsky/modeling/node"
	"github.com/fallguy/rimsky/modeling/shared"
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
// Per docs/history/2026-05-01-control-plane-and-store-lifecycle-design.md
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

// TemplateRow mirrors a row of rimsky_templates. JSON tags are
// snake_case per the observability spec §1.2, which the dashboard
// renders directly.
type TemplateRow struct {
	ID           string            `json:"id"`
	Spec         node.TemplateSpec `json:"spec"`
	State        TemplateState     `json:"state"`
	RegisteredAt time.Time         `json:"registered_at"`
	Source       string            `json:"source"` // "direct" | future package-manager values
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
	// Tag, when non-empty, restricts to templates carrying the given
	// tag in rimsky_template_tags. Used by the observability /v1/
	// observability/templates?tag=… browse filter (spec §1.2.2).
	Tag string
}

// -------- Template tags --------

type TemplateTagRow struct {
	Tag        string    `json:"tag"`
	TemplateID string    `json:"template_id"` // hash
	UpdatedAt  time.Time `json:"updated_at"`
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
	ID           shared.UUID    `json:"id"`
	TemplateHash string         `json:"template_hash"` // FK to rimsky_templates.id
	InstanceKey  *string        `json:"instance_key"`  // nullable
	Params       map[string]any `json:"params"`
	CreatedAt    time.Time      `json:"created_at"`
	TerminatedAt *time.Time     `json:"terminated_at"` // nullable; set at terminal-state detection
}
type InstanceStore interface {
	Create(ctx context.Context, args InstanceCreateInput, tx Tx) (InstanceRow, error)
	Get(ctx context.Context, id shared.UUID, tx Tx) (*InstanceRow, error)
	GetByInstanceKey(ctx context.Context, templateHash string, instanceKey string, tx Tx) (*InstanceRow, error)
	// FindAnyByInstanceKey looks up an instance by instance_key alone
	// (no template hash). Used by the control-api's `idOrKey` URL
	// resolver. Returns (nil, nil) when no row matches.
	FindAnyByInstanceKey(ctx context.Context, instanceKey string, tx Tx) (*InstanceRow, error)
	List(ctx context.Context, filter InstanceListFilter, pag ListPagination, tx Tx) (PaginatedListResult[InstanceRow], error)
	Delete(ctx context.Context, id shared.UUID, tx Tx) error
	MarkTerminated(ctx context.Context, id shared.UUID, tx Tx) error
	CountActiveByTemplate(ctx context.Context, templateHash string, tx Tx) (int, error)
	ListTerminatedWithLifecycleRows(ctx context.Context, limit int, tx Tx) ([]InstanceRow, error)
	// CountByActive returns (active, terminated) instance counts for the
	// system summary endpoint. Active = TerminatedAt IS NULL.
	CountByActive(ctx context.Context, tx Tx) (active int, terminated int, err error)
}
type InstanceCreateInput struct {
	ID           shared.UUID
	TemplateHash string
	InstanceKey  *string // nullable
	Params       map[string]any
}
type InstanceListFilter struct {
	TemplateHash string
	// Active, when non-nil, filters by terminated_at. Active=true →
	// terminated_at IS NULL; Active=false → terminated_at IS NOT NULL.
	Active *bool
}

// -------- Store lifecycle bookkeeping --------

type LifecycleIdempotencyScopeKind string

const (
	LifecycleIdempotencyScopeTemplate LifecycleIdempotencyScopeKind = "template"
	LifecycleIdempotencyScopeInstance LifecycleIdempotencyScopeKind = "instance"
)

type LifecycleIdempotencyState string

const (
	LifecycleIdempotencyStateRegistered LifecycleIdempotencyState = "registered"
	LifecycleIdempotencyStateDeployed   LifecycleIdempotencyState = "deployed"
	LifecycleIdempotencyStateUndeployed LifecycleIdempotencyState = "undeployed"
	LifecycleIdempotencyStateCreated    LifecycleIdempotencyState = "created"
)

type LifecycleIdempotencyRow struct {
	StoreRegistrationName string                        `json:"store_registration_name"`
	ScopeKind             LifecycleIdempotencyScopeKind `json:"scope_kind"`
	ScopeID               string                        `json:"scope_id"`
	State                 LifecycleIdempotencyState     `json:"state"`
	LastEventAt           time.Time                     `json:"last_event_at"`
}

type LifecycleIdempotencyStore interface {
	Get(ctx context.Context, storeName string, scopeKind LifecycleIdempotencyScopeKind, scopeID string, tx Tx) (*LifecycleIdempotencyRow, error)
	Upsert(ctx context.Context, row LifecycleIdempotencyRow, tx Tx) error
	Delete(ctx context.Context, storeName string, scopeKind LifecycleIdempotencyScopeKind, scopeID string, tx Tx) error
	DeleteByScope(ctx context.Context, scopeKind LifecycleIdempotencyScopeKind, scopeID string, tx Tx) error
	ListByScope(ctx context.Context, scopeKind LifecycleIdempotencyScopeKind, scopeID string, tx Tx) ([]LifecycleIdempotencyRow, error)
	// ListByStore returns every lifecycle row for a given store
	// registration (across all scopes). Used by the observability
	// per-store detail endpoint.
	ListByStore(ctx context.Context, storeName string, tx Tx) ([]LifecycleIdempotencyRow, error)
}

// -------- Nodes --------

type NodeRow struct {
	ID                   shared.UUID      `json:"id"`
	InstanceID           shared.UUID      `json:"instance_id"`
	NodeType             string           `json:"node_type"`
	Executor             string           `json:"executor"`
	ScheduleCron         string           `json:"schedule_cron"`
	State                shared.NodeState `json:"state"`
	Dependencies         []shared.UUID    `json:"dependencies"`
	CurrentErrorClass    string           `json:"current_error_class,omitempty"`
	RetryCounter         int              `json:"retry_counter"`
	ActionIndex          int              `json:"action_index"`
	LastHeartbeatAt      *time.Time       `json:"last_heartbeat_at,omitempty"`
	AssignedSupervisorID string           `json:"assigned_supervisor_id,omitempty"`
	FrameID              *shared.UUID     `json:"frame_id,omitempty"`
	CreatedAt            time.Time        `json:"created_at"`
	UpdatedAt            time.Time        `json:"updated_at"`
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
	UpdateState(ctx context.Context, id shared.UUID, state shared.NodeState, reason cascade.TransitionReason, tx Tx) error
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
	LockKindNamed LockKind = "named"
	LockKindScope LockKind = "scope"
)

type LockHolderRow struct {
	ID        shared.UUID     `json:"claim_id"`
	LockKind  LockKind        `json:"lock_kind"`
	LockName  *string         `json:"lock_name,omitempty"`
	StoreName *string         `json:"store_name,omitempty"`
	ScopeData json.RawMessage `json:"scope_data,omitempty"`
	// Address carries `json:"-"` so the observability handlers can
	// pass *LockHolderRow to writeJSON without leaking store-supplied
	// claim address bytes (spec §1.3 / blessed-invariant 20). Scope
	// is exposed because operators legitimately need to see what
	// scope a claim covers; address is opaque to rimsky and meant
	// for the store/executor only.
	Address            json.RawMessage `json:"-"`
	Intent             *string         `json:"intent,omitempty"`
	HolderSupervisorID string          `json:"holder_supervisor_id"`
	HolderNodeID       shared.UUID     `json:"holder_node_id"`
	ClaimedAt          time.Time       `json:"claimed_at"`
	LastHeartbeatAt    time.Time       `json:"last_heartbeat_at"`
	ExpiresAt          time.Time       `json:"expires_at"`
	FrameID            *shared.UUID    `json:"frame_id,omitempty"`
	// RealizedWriteSemantics is the per-claim semantics returned by
	// ClaimProducer.Open. Persisted on the lock-holder row so the
	// scope-conflict check (foundation/integration/runner_acquire.go::
	// evaluateScopeConflict) can apply ModeCoexists without re-dialing
	// the producer. Empty for named-lock rows.
	RealizedWriteSemantics string `json:"realized_write_semantics,omitempty"`
}

type LockHolderInsertInput struct {
	ID                     shared.UUID
	LockKind               LockKind
	LockName               *string
	StoreName              *string
	ScopeData              json.RawMessage
	Address                json.RawMessage
	Intent                 *string
	HolderSupervisorID     string
	HolderNodeID           shared.UUID
	ExpiresAt              time.Time
	FrameID                *shared.UUID
	RealizedWriteSemantics string // empty for named-lock rows
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

	// ListByStoreScope returns all lock-holder rows for a given store
	// name. The supervisor uses this for the in-Go scope-conflict check
	// inside the acquisition tx (byte-equal on scope_data per spec §7.7).
	ListByStoreScope(ctx context.Context, storeName string, tx Tx) ([]LockHolderRow, error)

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

	// UpdateScope writes a new scope_data to a scope-kind row,
	// claimant-guarded on supervisorID. Used by the supervisor's
	// acquireClaim path when the store-chosen scope differs from the
	// substituted-selector scope the supervisor wrote at INSERT time.
	UpdateScope(ctx context.Context, id shared.UUID, supervisorID string, scope json.RawMessage, tx Tx) error

	// UpdateRealizedWriteSemantics writes the per-claim ClaimProducer-
	// declared realized_write_semantics on a scope-kind row,
	// claimant-guarded on supervisorID. Called after ClaimProducer.Open
	// returns; the value is then consumed by the in-Go scope-conflict
	// check (foundation/integration/runner_acquire.go::evaluateScopeConflict)
	// without re-dialing the producer.
	UpdateRealizedWriteSemantics(ctx context.Context, id shared.UUID, supervisorID string, ws string, tx Tx) error

	// ListForObservability returns rows matching the filter, paginated
	// by claimed_at DESC. Used by the observability /v1/observability/
	// lock-holders browse endpoint (spec §1.2.4).
	ListForObservability(ctx context.Context, filter LockHolderListFilter, pag ListPagination, tx Tx) (PaginatedListResult[LockHolderRow], error)

	// GetByFrameAndNode returns the lock-holder row whose holder_node_id
	// equals nodeID and frame_id equals frameID. Used by the
	// observability /v1/observability/dispatches/{id} endpoint to
	// resolve dispatch → claim_id without a full per-node scan. Returns
	// (nil, nil) when no matching row exists.
	GetByFrameAndNode(ctx context.Context, nodeID shared.UUID, frameID shared.UUID, tx Tx) (*LockHolderRow, error)
}

// LockHolderListFilter is the observability browse filter for the
// rimsky_lock_holders endpoint (spec §1.2.4).
type LockHolderListFilter struct {
	StoreName        string
	HolderNodeID     *shared.UUID
	HolderSupervisor string
	InstanceID       *shared.UUID
	NodeType         string
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
	ID           shared.UUID      `json:"id"`
	LockHolderID shared.UUID      `json:"lock_holder_id"`
	HolderNodeID shared.UUID      `json:"holder_node_id"`
	State        ClaimHolderState `json:"state"`
	CompletedAt  *time.Time       `json:"completed_at,omitempty"`
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
	ID         int64          `json:"id"`
	InstanceID *shared.UUID   `json:"instance_id,omitempty"`
	NodeID     *shared.UUID   `json:"node_id,omitempty"`
	Kind       string         `json:"kind"`
	Payload    map[string]any `json:"payload"`
	OccurredAt time.Time      `json:"occurred_at"`
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
	// KindIn restricts to events whose Kind is in this list. Empty = no
	// filter. Combined with Kind via AND when both are set.
	KindIn []string
	Since  *time.Time
	Until  *time.Time
}
type EventListResult struct {
	Events     []EventRow
	NextCursor string
}
type EventStore interface {
	Append(ctx context.Context, in EventAppendInput, tx Tx) error
	List(ctx context.Context, filter EventListFilter, pag ListPagination, tx Tx) (EventListResult, error)
	// LastTerminalByNodes returns the most-recent dispatch-terminal event
	// (kind in {work_completed, error}) per node id. Used by the
	// observability cascade-graph projection to avoid an N+1 List per
	// node. Nodes with no matching event are absent from the map.
	LastTerminalByNodes(ctx context.Context, nodeIDs []shared.UUID, tx Tx) (map[shared.UUID]EventRow, error)
}

// -------- Schedules --------

type ScheduleRow struct {
	NodeID      shared.UUID `json:"node_id"`
	CronExpr    string      `json:"cron_expr"`
	NextFireAt  time.Time   `json:"next_fire_at"`
	LastFiredAt *time.Time  `json:"last_fired_at,omitempty"`
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
	// ListForObservability returns schedules matching the filter,
	// cursor-paginated by next_fire_at ASC. Used by the observability
	// /v1/observability/schedules endpoint (spec §1.2.2).
	ListForObservability(ctx context.Context, filter ScheduleListFilter, pag ListPagination, tx Tx) (PaginatedListResult[ScheduleRow], error)
}

// ScheduleListFilter is the observability browse filter for schedules.
type ScheduleListFilter struct {
	NodeID *shared.UUID
}

// -------- Supervisors --------

type SupervisorRow struct {
	ID                string    `json:"id"`
	AcceptedExecutors []string  `json:"accepted_executors"`
	AcceptedStores    []string  `json:"accepted_stores"`
	Concurrency       int       `json:"concurrency"`
	CallbackHost      string    `json:"callback_host"`
	CallbackPort      int       `json:"callback_port"`
	LastHeartbeatAt   time.Time `json:"last_heartbeat_at"`
	ActiveNodeCount   int       `json:"active_node_count"`
	RegisteredAt      time.Time `json:"registered_at"`
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

// FrameRow is the observability projection of one rimsky_frames row.
// Used by the observability frames endpoint.
type FrameRow struct {
	FrameID        shared.UUID `json:"frame_id"`
	InstanceID     shared.UUID `json:"instance_id"`
	State          FrameState  `json:"state"`
	Mode           FrameMode   `json:"mode"`
	StartedAt      *time.Time  `json:"started_at,omitempty"`
	EndedAt        *time.Time  `json:"ended_at,omitempty"`
	FrameTimeoutMs int64       `json:"frame_timeout_ms"`
}

// FrameListFilter is the observability browse filter.
type FrameListFilter struct {
	InstanceID *shared.UUID
	State      FrameState
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

	// ---- Observability ----

	// ListForObservability returns frames matching filter, cursor-paginated
	// by created_at DESC. Used by the observability /v1/observability/frames
	// endpoint.
	ListForObservability(ctx context.Context, filter FrameListFilter, pag ListPagination, tx Tx) (PaginatedListResult[FrameRow], error)

	// GetForObservability returns one frame by id. Returns (nil, nil) when
	// the row does not exist.
	GetForObservability(ctx context.Context, frameID shared.UUID, tx Tx) (*FrameRow, error)
}

// -------- Store umbrella --------

type Store interface {
	Templates() TemplateStore
	TemplateTags() TemplateTagsStore
	Instances() InstanceStore
	LifecycleIdempotency() LifecycleIdempotencyStore
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
