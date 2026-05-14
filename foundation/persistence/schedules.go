// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package persistence

import (
	"context"
	"time"

	"github.com/fallguy/rimsky/foundation/shared"
)

// ScheduleRow mirrors a row of rimsky_schedules.
type ScheduleRow struct {
	NodeID      shared.UUID `json:"node_id"`
	CronExpr    string      `json:"cron_expr"`
	NextFireAt  time.Time   `json:"next_fire_at"`
	LastFiredAt *time.Time  `json:"last_fired_at,omitempty"`
}

// ScheduleRegisterInput is the per-row input for Register.
type ScheduleRegisterInput struct {
	NodeID     shared.UUID
	CronExpr   string
	NextFireAt time.Time
}

// ScheduleTable is the rimsky_schedules accessor.
type ScheduleTable interface {
	Register(ctx context.Context, in ScheduleRegisterInput, tx Tx) error
	DueBefore(ctx context.Context, cutoff time.Time, tx Tx) ([]ScheduleRow, error)
	RecordFired(ctx context.Context, nodeID shared.UUID, nextFireAt, firedAt time.Time, tx Tx) error
	ListAll(ctx context.Context, tx Tx) ([]ScheduleRow, error)
	ForceFire(ctx context.Context, nodeID shared.UUID, tx Tx) error
	// ListForObservability returns schedules matching the filter,
	// cursor-paginated by next_fire_at ASC. Used by the observability
	// /v1/observability/schedules endpoint (spec §1.2.2).
	ListForObservability(ctx context.Context, filter ScheduleListFilter, pag ListPagination, tx Tx) (PaginatedListResult[ScheduleRow], error)
}

// ScheduleListFilter is the observability browse filter for schedules.
type ScheduleListFilter struct {
	NodeID *shared.UUID
}
