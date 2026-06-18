// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package persistence

import (
	"context"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

type EventRow struct {
	ID         int64          `json:"id"`
	InstanceID *shared.UUID   `json:"instance_id,omitempty"`
	NodeID     *shared.UUID   `json:"node_id,omitempty"`
	Kind       events.Kind    `json:"-"`
	KindRaw    string         `json:"kind"`
	Payload    map[string]any `json:"payload"`
	OccurredAt time.Time      `json:"occurred_at"`
}

type EventAppendInput struct {
	InstanceID *shared.UUID
	NodeID     *shared.UUID
	Kind       events.Kind
	Payload    map[string]any
	OccurredAt *time.Time
}

type EventListFilter struct {
	InstanceID *shared.UUID
	NodeID     *shared.UUID
	Kind       string
	KindIn []string
	Since  *time.Time
	Until  *time.Time

	KeyID          *string
	KeyName        *string
	ActionExact    *string
	ActionPrefix   *string
	ResponseStatus *int
	Mode           *string
	RequestPath    *string
}

type EventListResult struct {
	Events     []EventRow
	NextCursor string
}

type EventTable interface {
	Append(ctx context.Context, in EventAppendInput, tx Tx) error
	List(ctx context.Context, filter EventListFilter, pag ListPagination, tx Tx) (EventListResult, error)
	LastTerminalByNodes(ctx context.Context, nodeIDs []shared.UUID, tx Tx) (map[shared.UUID]EventRow, error)

	// @concept: event-log trace retention.
	DeleteOlderThan(ctx context.Context, cutoff time.Time) (int, error)
}
