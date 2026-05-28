// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// run_scopes.go is the persistence accessor for rimsky_run_scopes,
// the table backing concept:run-scope. Hosts the per-graph
// instantiation tree (main / subgraph / fanout_partition).
//
// @concept: run-scope
package persistence

import (
	"context"
	"errors"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

// RunScopeRow projects one rimsky_run_scopes row. ParentRunScopeID
// and ParentRunID are nil only for the main RunScope; the table's
// CHECK constraint enforces both-or-neither.
type RunScopeRow struct {
	ID               shared.UUID
	ParentRunScopeID *shared.UUID
	ParentRunID      *shared.UUID
	GraphName        string
	PartitionKey     string // non-empty iff fanout_partition kind
	InstanceID       shared.UUID
	CreatedAt        time.Time
	ClosedAt         *time.Time
}

// RunScopeTable is the persistence accessor for rimsky_run_scopes.
// All methods take an explicit tx.
//
// @agent-contract:
//
//	what:        RunScope CRUD on rimsky_run_scopes.
//	how to use:  Create() inserts atomically with the triggering
//	             operation (instance create, subgraph caller success,
//	             SplitScope sub-claim acquisition). Close() stamps
//	             closed_at when parent-run rendezvous fires.
//	handles:     scope tree shape, fanout_partition uniqueness,
//	             parent-chain walks.
//	does NOT:    rimsky_node_runs allocation (see AffirmNodeRunRow);
//	             aggregation policy (see RunTreeRow.AggregationPolicy).
//	threadsafe:  caller's tx isolation.
type RunScopeTable interface {
	// Create inserts a new RunScope row. The caller supplies the id
	// so the same tx can also INSERT the first node_run row
	// referring to it (avoids a returning-id round-trip).
	Create(ctx context.Context, tx Tx, row RunScopeRow) error

	// GetByID returns the RunScope by id, or nil if not found.
	GetByID(ctx context.Context, tx Tx, id shared.UUID) (*RunScopeRow, error)

	// GetFanoutPartition returns the fanout_partition RunScope for
	// (parentRunID, partitionKey), or nil if not found. Used by
	// fan-out child re-resolution and by the cascade walker when
	// computing cross-scope targets.
	GetFanoutPartition(ctx context.Context, tx Tx, parentRunID shared.UUID, partitionKey string) (*RunScopeRow, error)

	// Close stamps closed_at on the RunScope. Called by carry-rule
	// (subgraph), aggregation walk (fanout_partition), and instance
	// termination (main). Idempotent: re-closing is a no-op.
	Close(ctx context.Context, tx Tx, id shared.UUID) error

	// ListChildScopes returns immediate child RunScopes for a parent
	// run. Used by aggregation walks and forensics.
	ListChildScopes(ctx context.Context, tx Tx, parentRunID shared.UUID) ([]RunScopeRow, error)

	// ListParentChain walks up via parent_run_scope_id; returns
	// from the given id (leaf) to the main RunScope (root)
	// inclusive. Used by depth-gating and forensics.
	ListParentChain(ctx context.Context, tx Tx, id shared.UUID) ([]RunScopeRow, error)
}

// ErrRunScopeClosed is returned by AffirmNodeRunRow when the
// RunScope's closed_at is set. Sibling sentinel to ErrRunRowMissing.
var ErrRunScopeClosed = errors.New("persistence: run scope is closed")
