// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package persistence

import (
	"context"
	"time"

	"github.com/fallguy/rimsky/modeling/shared"
)

// NodeAttributesRow mirrors a row of rimsky_node_attributes.
type NodeAttributesRow struct {
	NodeID     shared.UUID
	RunAttempt int
	Data       map[string]any
	UpdatedAt  time.Time
}

// NodeAttributesStore is the rimsky_node_attributes accessor.
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
