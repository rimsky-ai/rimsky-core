// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package persistence

import (
	"context"
	"time"

	"github.com/fallguy/rimsky/modeling/shared"
)

// EventRow mirrors a row of rimsky_events.
type EventRow struct {
	ID         int64          `json:"id"`
	InstanceID *shared.UUID   `json:"instance_id,omitempty"`
	NodeID     *shared.UUID   `json:"node_id,omitempty"`
	Kind       string         `json:"kind"`
	Payload    map[string]any `json:"payload"`
	OccurredAt time.Time      `json:"occurred_at"`
}

// EventAppendInput is the per-row input for Append.
type EventAppendInput struct {
	InstanceID *shared.UUID
	NodeID     *shared.UUID
	Kind       string
	Payload    map[string]any
	OccurredAt *time.Time // nil → server NOW()
}

// EventListFilter is the observability/list filter.
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

// EventListResult wraps a row slice with the next-cursor.
type EventListResult struct {
	Events     []EventRow
	NextCursor string
}

// EventStore is the rimsky_events accessor.
type EventStore interface {
	Append(ctx context.Context, in EventAppendInput, tx Tx) error
	List(ctx context.Context, filter EventListFilter, pag ListPagination, tx Tx) (EventListResult, error)
	// LastTerminalByNodes returns the most-recent dispatch-terminal event
	// (kind in {work_completed, error}) per node id. Used by the
	// observability cascade-graph projection to avoid an N+1 List per
	// node. Nodes with no matching event are absent from the map.
	LastTerminalByNodes(ctx context.Context, nodeIDs []shared.UUID, tx Tx) (map[shared.UUID]EventRow, error)
}
