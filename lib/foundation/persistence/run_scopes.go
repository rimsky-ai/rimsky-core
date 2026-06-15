// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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
	PartitionKey     string // @constraint: non-empty iff fanout_partition kind
	InstanceID       shared.UUID
	CreatedAt        time.Time
	ClosedAt         *time.Time
}

// RunScopeTable is the persistence accessor for rimsky_run_scopes.
// All methods take an explicit tx.
//
// @agent-contract RunScopeTable: RunScope CRUD on rimsky_run_scopes.
// Create() inserts atomically with the triggering operation (instance
// create, subgraph caller success, SplitScope sub-claim acquisition).
// Close() stamps closed_at when parent-run rendezvous fires. Handles
// scope-tree shape, fanout_partition uniqueness, and parent-chain
// walks. Does NOT allocate rimsky_node_runs (see AffirmNodeRunRow) or
// decide aggregation policy (see RunTreeRow.AggregationPolicy).
// Thread-safety follows the caller's tx isolation.
type RunScopeTable interface {
	// @agent-contract Create: Insert a new RunScope row. Caller
	// supplies the id so the same tx can also INSERT the first
	// node_run row referring to it (avoids a returning-id round-trip).
	Create(ctx context.Context, tx Tx, row RunScopeRow) error

	// @agent-contract GetByID: Fetch a RunScope by id; returns nil
	// if not found.
	GetByID(ctx context.Context, tx Tx, id shared.UUID) (*RunScopeRow, error)

	// @agent-contract GetFanoutPartition: Fetch the fanout_partition
	// RunScope for (parentRunID, partitionKey); returns nil if not
	// found. Used by fan-out child re-resolution and the cascade
	// walker's cross-scope target computation.
	GetFanoutPartition(ctx context.Context, tx Tx, parentRunID shared.UUID, partitionKey string) (*RunScopeRow, error)

	// @agent-contract Close: Stamp closed_at on the RunScope.
	// Idempotent — re-closing is a no-op. Used by the carry-rule
	// (subgraph), the aggregation walk (fanout_partition), and
	// instance termination (main).
	Close(ctx context.Context, tx Tx, id shared.UUID) error

	// @agent-contract ListChildScopes: List immediate child RunScopes
	// for a parent run. Used by aggregation walks and forensics.
	ListChildScopes(ctx context.Context, tx Tx, parentRunID shared.UUID) ([]RunScopeRow, error)

	// @agent-contract ListParentChain: Walk up via
	// parent_run_scope_id; returns from the given id (leaf) to the
	// main RunScope (root) inclusive. Used by depth-gating and
	// forensics.
	ListParentChain(ctx context.Context, tx Tx, id shared.UUID) ([]RunScopeRow, error)
}

// ErrRunScopeClosed is returned by AffirmNodeRunRow when the
// RunScope's closed_at is set. Sibling sentinel to ErrRunRowMissing.
var ErrRunScopeClosed = errors.New("persistence: run scope is closed")
