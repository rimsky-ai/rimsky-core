// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package persistence

import (
	"context"
	"time"

	"github.com/fallguy/rimsky/foundation/shared"
)

// NodeAttributesRow mirrors a row of rimsky_node_attributes (post-2026-05-20
// per-run keying).
type NodeAttributesRow struct {
	NodeRunID shared.UUID
	NodeID    shared.UUID // denormalized for forensic queries
	Data      map[string]any
	UpdatedAt time.Time
}

// NodeAttributeTable is the rimsky_node_attributes accessor.
//
// GetByRun returns (nil, nil) when the row is absent — absence is a
// normal lifecycle state (the row is created lazily on first dispatch).
//
// GetLatestByNode returns the most-recent run's attribute row for the
// given node, used by forensic / observability paths (control-api,
// lineage projections, agent dashboards). Returns (nil, nil) when the
// node has no runs.
//
// Upsert replaces `data` outright; MergeDelta performs a SHALLOW JSONB
// merge and requires the row to exist (returns wrapped ErrNotFound when
// absent — both drivers wrap persistence.ErrNotFound, so callers may
// `errors.Is(err, persistence.ErrNotFound)` regardless of driver).
type NodeAttributeTable interface {
	GetByRun(ctx context.Context, runID shared.UUID, tx Tx) (*NodeAttributesRow, error)
	GetLatestByNode(ctx context.Context, nodeID shared.UUID, tx Tx) (*NodeAttributesRow, error)
	Upsert(ctx context.Context, runID, nodeID shared.UUID, data map[string]any, tx Tx) error
	MergeDelta(ctx context.Context, runID shared.UUID, delta map[string]any, tx Tx) error
}
