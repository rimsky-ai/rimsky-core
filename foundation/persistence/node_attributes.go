// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package persistence

import (
	"context"
	"time"

	"github.com/fallguyconsulting/rimsky/foundation/shared"
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
// GetLatestByNode returns the most-recent attribute row for the
// given (node, run scope). Used by forensic / observability paths
// (control-api, lineage projections, agent dashboards). Returns
// (nil, nil) when no attribute row exists for that node in that
// RunScope.
//
// Under RunScope-first (per spec
// .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md),
// the lookup is scoped: under fan-out, multiple concurrent runs of
// the same node exist in different RunScopes; the caller picks the
// scope (typically via the dispatch context's RunScope or a forensic
// walk of the RunScope tree).
//
// Upsert replaces `data` outright; MergeDelta performs a SHALLOW JSONB
// merge and requires the row to exist (returns wrapped ErrNotFound when
// absent — both drivers wrap persistence.ErrNotFound, so callers may
// `errors.Is(err, persistence.ErrNotFound)` regardless of driver).
type NodeAttributeTable interface {
	GetByRun(ctx context.Context, runID shared.UUID, tx Tx) (*NodeAttributesRow, error)
	GetLatestByNode(ctx context.Context, nodeID shared.UUID, runScopeID shared.UUID, tx Tx) (*NodeAttributesRow, error)
	Upsert(ctx context.Context, runID, nodeID shared.UUID, data map[string]any, tx Tx) error
	MergeDelta(ctx context.Context, runID shared.UUID, delta map[string]any, tx Tx) error
}
