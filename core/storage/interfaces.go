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
	Executor             string // empty = pure-cascade
	ScheduleCron         string // empty = no schedule
	State                shared.NodeState
	Dependencies         []shared.UUID
	ConcurrencyTags      []string
	CurrentErrorClass    string
	RetryCounter         int
	ActionIndex          int
	LastHeartbeatAt      *time.Time
	AssignedSupervisorID string
	KillRequested        bool
	CreatedAt            time.Time
	UpdatedAt            time.Time
}
type NodeCreateInput struct {
	ID              shared.UUID
	InstanceID      shared.UUID
	NodeType        string
	Executor        string
	ScheduleCron    string
	Dependencies    []shared.UUID
	ConcurrencyTags []string
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
	SetKillRequested(ctx context.Context, id shared.UUID, value bool, tx Tx) error
	ClearSupervisorAssignment(ctx context.Context, id shared.UUID, tx Tx) error
	DeleteByInstance(ctx context.Context, instanceID shared.UUID, tx Tx) error
}

// -------- Resources (registry + data) --------

type ResourceRow struct {
	ID                shared.UUID
	ResourcePath      []string
	OwnerNodeID       shared.UUID
	CurrentVersionID  *shared.UUID
	PreviousVersionID *shared.UUID
	KeepVersions      int
	CreatedAt         time.Time
}
type ResourceVersionRow struct {
	ID            shared.UUID
	ResourceID    shared.UUID
	ProducedBy    *shared.UUID
	Data          []byte // inline JSON bytes; nil for external
	DataRef       []byte // JSON-encoded ref for external; nil for inline
	ChangeSummary string
	CommittedAt   time.Time
}
type ResourceCreateInput struct {
	ResourcePath []string
	OwnerNodeID  shared.UUID
	KeepVersions int
}
type ResourceCommitInput struct {
	ProducedBy    shared.UUID
	Data          []byte // inline JSON bytes
	DataRef       []byte // external ref
	ChangeSummary string
}
type ResourceRegistry interface {
	Create(ctx context.Context, in ResourceCreateInput, tx Tx) (ResourceRow, error)
	Get(ctx context.Context, id shared.UUID, tx Tx) (*ResourceRow, error)
	ListByOwner(ctx context.Context, ownerNodeID shared.UUID, tx Tx) ([]ResourceRow, error)
	CommitVersion(ctx context.Context, resourceID shared.UUID, in ResourceCommitInput, tx Tx) (ResourceVersionRow, error)
	NoOpCommit(ctx context.Context, resourceID shared.UUID, tx Tx) error
	GCOldVersions(ctx context.Context, resourceID shared.UUID, keep int, tx Tx) (int, error)
	RestoreVersion(ctx context.Context, resourceID shared.UUID, target string, versionID shared.UUID, tx Tx) (ResourceVersionRow, error) // target: "previous" | "id"
	GetVersion(ctx context.Context, versionID shared.UUID, tx Tx) (*ResourceVersionRow, error)
	ListVersions(ctx context.Context, resourceID shared.UUID, tx Tx) ([]ResourceVersionRow, error)
	ListVersionsPaged(ctx context.Context, resourceID shared.UUID, pag ListPagination, tx Tx) (PaginatedListResult[ResourceVersionRow], error)
}

type ResourceDataStore interface {
	Read(ctx context.Context, version ResourceVersionRow, tx Tx) (any, error)
	Delete(ctx context.Context, version ResourceVersionRow, tx Tx) error
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
}

// -------- Supervisors --------

type SupervisorRow struct {
	ID                string
	AcceptedExecutors []string
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
	Resources() ResourceRegistry
	ResourceData() ResourceDataStore
	Events() EventStore
	Schedules() ScheduleStore
	Supervisors() SupervisorStore
	Transaction(ctx context.Context, fn func(ctx context.Context, tx Tx) error) error
}
