// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// ScheduleStore — port of rimsky/src/storage/postgres/timer-store.ts adapted
// for the spec §11.1 rename: rimsky_timers → rimsky_schedules, keyed on
// node_id (no separate target_cell_id or reason columns).
package postgres

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/modeling/shared"
)

// scheduleCursor encodes (next_fire_at, node_id) so duplicate
// next_fire_at values don't drop rows at page boundaries. ASC sort
// pairs with a strict-tuple comparator: (next_fire_at, node_id) > ($1, $2).
type scheduleCursor struct {
	N time.Time `json:"n"`
	I string    `json:"i"`
}

func encodeScheduleCursor(nextFire time.Time, nodeID shared.UUID) string {
	c := scheduleCursor{N: nextFire.UTC(), I: nodeID.String()}
	b, _ := json.Marshal(c)
	return base64.StdEncoding.EncodeToString(b)
}

func decodeScheduleCursor(s string) (time.Time, string, error) {
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return time.Time{}, "", err
	}
	var c scheduleCursor
	if err := json.Unmarshal(raw, &c); err != nil {
		return time.Time{}, "", err
	}
	return c.N, c.I, nil
}

const scheduleCols = `node_id, cron_expr, next_fire_at, last_fired_at`

// Register upserts the schedule row for a node.
func (s *schedulesImpl) Register(ctx context.Context, in persistence.ScheduleRegisterInput, tx persistence.Tx) error {
	ex := s.q(tx)
	_, err := ex.Exec(ctx,
		`INSERT INTO rimsky_schedules (node_id, cron_expr, next_fire_at)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (node_id) DO UPDATE
		   SET cron_expr = EXCLUDED.cron_expr,
		       next_fire_at = EXCLUDED.next_fire_at`,
		in.NodeID, in.CronExpr, in.NextFireAt,
	)
	return err
}

// DueBefore returns all schedules with next_fire_at <= cutoff. Uses
// FOR UPDATE SKIP LOCKED so two concurrent schedulers don't double-fire.
// Caller must follow up with RecordFired in the same transaction to advance
// next_fire_at before commit.
func (s *schedulesImpl) DueBefore(ctx context.Context, cutoff time.Time, tx persistence.Tx) ([]persistence.ScheduleRow, error) {
	ex := s.q(tx)
	rows, err := ex.Query(ctx,
		`SELECT `+scheduleCols+` FROM rimsky_schedules
		 WHERE next_fire_at <= $1
		 ORDER BY next_fire_at ASC
		 FOR UPDATE SKIP LOCKED`, cutoff,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectSchedules(rows)
}

func (s *schedulesImpl) RecordFired(ctx context.Context, nodeID shared.UUID, nextFireAt, firedAt time.Time, tx persistence.Tx) error {
	ex := s.q(tx)
	_, err := ex.Exec(ctx,
		`UPDATE rimsky_schedules
		   SET next_fire_at = $2,
		       last_fired_at = $3
		 WHERE node_id = $1`,
		nodeID, nextFireAt, firedAt,
	)
	return err
}

// ForceFire bumps the schedule row's next_fire_at to now() so the scheduler's
// next tick picks the node up. Returns nil even when no row matches node_id;
// route handlers that care about presence load the node row first.
func (s *schedulesImpl) ForceFire(ctx context.Context, nodeID shared.UUID, tx persistence.Tx) error {
	ex := s.q(tx)
	_, err := ex.Exec(ctx,
		`UPDATE rimsky_schedules
		   SET next_fire_at = now()
		 WHERE node_id = $1`,
		nodeID,
	)
	return err
}

func (s *schedulesImpl) ListAll(ctx context.Context, tx persistence.Tx) ([]persistence.ScheduleRow, error) {
	ex := s.q(tx)
	rows, err := ex.Query(ctx,
		`SELECT `+scheduleCols+` FROM rimsky_schedules ORDER BY next_fire_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectSchedules(rows)
}

// ListForObservability returns schedules matching filter, cursor-paginated
// by (next_fire_at ASC, node_id ASC). The cursor encodes both fields
// so dense scheduling (multiple nodes sharing a next_fire_at) doesn't
// drop rows at page boundaries; the predicate is the strict tuple
// comparison (next_fire_at, node_id) > ($cursor_t, $cursor_id).
func (s *schedulesImpl) ListForObservability(ctx context.Context, filter persistence.ScheduleListFilter, pag persistence.ListPagination, tx persistence.Tx) (persistence.PaginatedListResult[persistence.ScheduleRow], error) {
	limit := pag.Limit
	if limit <= 0 {
		limit = 50
	}
	var nodeArg any
	var cursorTimeArg, cursorIDArg any
	if filter.NodeID != nil {
		nodeArg = *filter.NodeID
	}
	if pag.Cursor != "" {
		t, id, err := decodeScheduleCursor(pag.Cursor)
		if err != nil {
			return persistence.PaginatedListResult[persistence.ScheduleRow]{}, fmt.Errorf("schedules.list: bad cursor: %w", err)
		}
		cursorTimeArg = t
		cursorIDArg = id
	}
	rows, err := s.q(tx).Query(ctx,
		`SELECT `+scheduleCols+`
		   FROM rimsky_schedules
		  WHERE ($1::uuid IS NULL OR node_id = $1)
		    AND ($2::timestamptz IS NULL OR (next_fire_at, node_id) > ($2, $3::uuid))
		  ORDER BY next_fire_at ASC, node_id ASC
		  LIMIT $4`,
		nodeArg, cursorTimeArg, cursorIDArg, limit,
	)
	if err != nil {
		return persistence.PaginatedListResult[persistence.ScheduleRow]{}, fmt.Errorf("schedules.list: %w", err)
	}
	defer rows.Close()
	out, err := collectSchedules(rows)
	if err != nil {
		return persistence.PaginatedListResult[persistence.ScheduleRow]{}, err
	}
	var nextCursor string
	if len(out) == limit && len(out) > 0 {
		last := out[len(out)-1]
		nextCursor = encodeScheduleCursor(last.NextFireAt, last.NodeID)
	}
	return persistence.PaginatedListResult[persistence.ScheduleRow]{Rows: out, NextCursor: nextCursor}, nil
}

func collectSchedules(rows interface {
	Next() bool
	Scan(dst ...any) error
	Err() error
}) ([]persistence.ScheduleRow, error) {
	var out []persistence.ScheduleRow
	for rows.Next() {
		var (
			r         persistence.ScheduleRow
			lastFired *time.Time
		)
		if err := rows.Scan(&r.NodeID, &r.CronExpr, &r.NextFireAt, &lastFired); err != nil {
			return nil, err
		}
		r.LastFiredAt = lastFired
		out = append(out, r)
	}
	return out, rows.Err()
}
