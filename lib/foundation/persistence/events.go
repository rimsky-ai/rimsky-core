// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package persistence

import (
	"context"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
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
//
// The KeyID / KeyName / ActionExact / ActionPrefix / ResponseStatus /
// Mode / RequestPath fields filter on keys inside the JSONB `payload`
// column. They are only meaningful for `auth.*` kinds (the auth audit
// rows, whose payload carries those keys) and back the GET /audit read
// surface; on non-auth event kinds the payload lacks those keys so a
// non-nil filter simply matches nothing. A nil pointer is "no filter"
// — it must never exclude a row.
type EventListFilter struct {
	InstanceID *shared.UUID
	NodeID     *shared.UUID
	Kind       string
	// KindIn restricts to events whose Kind is in this list. Empty = no
	// filter. Combined with Kind via AND when both are set.
	KindIn []string
	Since  *time.Time
	Until  *time.Time

	// Auth-payload filters (JSONB payload keys; meaningful for auth.*
	// kinds). Each is AND-composed; a nil pointer is a no-op.
	KeyID          *string // payload->>'key_id'
	KeyName        *string // payload->>'key_name'
	ActionExact    *string // payload->>'action' = ?
	ActionPrefix   *string // payload->>'action' LIKE ? || '%'
	ResponseStatus *int    // payload->>'response_status' = ?
	Mode           *string // payload->>'mode'
	RequestPath    *string // payload->>'request_path' (the audit "target")
}

// EventListResult wraps a row slice with the next-cursor.
type EventListResult struct {
	Events     []EventRow
	NextCursor string
}

// EventTable is the rimsky_events accessor.
type EventTable interface {
	Append(ctx context.Context, in EventAppendInput, tx Tx) error
	List(ctx context.Context, filter EventListFilter, pag ListPagination, tx Tx) (EventListResult, error)
	// LastTerminalByNodes returns the most-recent dispatch-terminal event
	// (kind in {work_completed, error}) per node id. Used by the
	// observability cascade-graph projection to avoid an N+1 List per
	// node. Nodes with no matching event are absent from the map.
	LastTerminalByNodes(ctx context.Context, nodeIDs []shared.UUID, tx Tx) (map[shared.UUID]EventRow, error)
}
