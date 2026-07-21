// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package persistence

import (
	"context"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

type NodeAttributesRow struct {
	NodeRunID shared.UUID
	NodeID    shared.UUID
	Data      map[string]any
	UpdatedAt time.Time
}

type NodeAttributeTable interface {
	GetByRun(ctx context.Context, runID shared.UUID, tx Tx) (*NodeAttributesRow, error)
	GetLatestByNode(ctx context.Context, nodeID shared.UUID, runScopeID shared.UUID, tx Tx) (*NodeAttributesRow, error)
	Upsert(ctx context.Context, runID, nodeID shared.UUID, data map[string]any, tx Tx) error
	MergeDelta(ctx context.Context, runID shared.UUID, delta map[string]any, tx Tx) error

	// @concept: cascade
	// @decision: mode-default-most-recent
	SetDispatchInputBag(ctx context.Context, runID, nodeID shared.UUID, bag map[string]any, tx Tx) error

	// @concept: cascade
	GetDispatchInputBag(ctx context.Context, runID shared.UUID, tx Tx) (map[string]any, error)

	// @concept: cascade
	// @decision: non-cascade-direct-to-stale
	// @story: resume-preserves-snapshot
	SnapshotBagForNewRun(ctx context.Context, newRunID, nodeID, runScopeID shared.UUID, tx Tx) error

	// @concept: cascade
	GetPriorRunData(ctx context.Context, runID shared.UUID, tx Tx) (map[string]any, error)
}
