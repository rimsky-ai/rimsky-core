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
	ParentRunID      *shared.UUID
	GraphName        string
	PartitionKey     string
	InstanceID       shared.UUID
	CreatedAt        time.Time
	ClosedAt         *time.Time
}

type RunScopeTable interface {
	Create(ctx context.Context, tx Tx, row RunScopeRow) error

	GetByID(ctx context.Context, tx Tx, id shared.UUID) (*RunScopeRow, error)

	GetFanoutPartition(ctx context.Context, tx Tx, parentRunID shared.UUID, partitionKey string) (*RunScopeRow, error)

	Close(ctx context.Context, tx Tx, id shared.UUID) error

	ListChildScopes(ctx context.Context, tx Tx, parentRunID shared.UUID) ([]RunScopeRow, error)

	ListParentChain(ctx context.Context, tx Tx, id shared.UUID) ([]RunScopeRow, error)
}

var ErrRunScopeClosed = errors.New("persistence: run scope is closed")
