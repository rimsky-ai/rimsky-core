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

type RunScopeRow struct {
	ID               shared.UUID
	ParentRunScopeID *shared.UUID
	ParentNodeRunID  *shared.UUID
	GraphName        string
	PartitionKey     string
	InstanceID       shared.UUID
	CreatedAt        time.Time
	ClosedAt         *time.Time
}

type RunScopeTable interface {
	Create(ctx context.Context, row RunScopeRow, tx Tx) error

	GetByID(ctx context.Context, id shared.UUID, tx Tx) (*RunScopeRow, error)

	GetFanoutPartition(ctx context.Context, parentNodeRunID shared.UUID, partitionKey string, tx Tx) (*RunScopeRow, error)

	Close(ctx context.Context, id shared.UUID, tx Tx) error

	ListParentChain(ctx context.Context, id shared.UUID, tx Tx) ([]RunScopeRow, error)

	ListTreeDeepestFirst(ctx context.Context, rootRunScopeID shared.UUID, tx Tx) ([]RunScopeRow, error)
}

var ErrRunScopeClosed = errors.New("persistence: run scope is closed")
