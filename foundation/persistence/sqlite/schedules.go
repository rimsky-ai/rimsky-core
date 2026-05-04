// schedules.go — SQLite-backed persistence.ScheduleStore.
package sqlite

import (
	"context"
	"database/sql"
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
// Mirrors the postgres-side encoder so cursors round-trip across drivers.
//
//	@source: core/persistence/postgres/schedules.go:scheduleCursor
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

func (s *schedulesImpl) Register(ctx context.Context, in persistence.ScheduleRegisterInput, tx persistence.Tx) error {
	_, err := s.q(tx).ExecContext(ctx,
		`INSERT INTO rimsky_schedules (node_id, cron_expr, next_fire_at)
		 VALUES (?, ?, ?)
		 ON CONFLICT(node_id) DO UPDATE
		   SET cron_expr = excluded.cron_expr,
		       next_fire_at = excluded.next_fire_at`,
		in.NodeID.String(), in.CronExpr, formatTime(in.NextFireAt),
	)
	return err
}

// DueBefore omits FOR UPDATE SKIP LOCKED — under SQLite the surrounding
// BEGIN IMMEDIATE writer-slot hold serialises any concurrent scheduler
// tick, so there's no contention to skip.
func (s *schedulesImpl) DueBefore(ctx context.Context, cutoff time.Time, tx persistence.Tx) ([]persistence.ScheduleRow, error) {
	rows, err := s.q(tx).QueryContext(ctx,
		`SELECT `+scheduleCols+` FROM rimsky_schedules
		 WHERE next_fire_at <= ?
		 ORDER BY next_fire_at ASC`, formatTime(cutoff),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectSchedules(rows)
}

func (s *schedulesImpl) RecordFired(ctx context.Context, nodeID shared.UUID, nextFireAt, firedAt time.Time, tx persistence.Tx) error {
	_, err := s.q(tx).ExecContext(ctx,
		`UPDATE rimsky_schedules
		   SET next_fire_at = ?,
		       last_fired_at = ?
		 WHERE node_id = ?`,
		formatTime(nextFireAt), formatTime(firedAt), nodeID.String(),
	)
	return err
}

func (s *schedulesImpl) ForceFire(ctx context.Context, nodeID shared.UUID, tx persistence.Tx) error {
	_, err := s.q(tx).ExecContext(ctx,
		`UPDATE rimsky_schedules
		   SET next_fire_at = ?
		 WHERE node_id = ?`,
		nowUTC(), nodeID.String(),
	)
	return err
}

func (s *schedulesImpl) ListAll(ctx context.Context, tx persistence.Tx) ([]persistence.ScheduleRow, error) {
	rows, err := s.q(tx).QueryContext(ctx,
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
		nodeArg = filter.NodeID.String()
	}
	if pag.Cursor != "" {
		t, id, err := decodeScheduleCursor(pag.Cursor)
		if err != nil {
			return persistence.PaginatedListResult[persistence.ScheduleRow]{}, fmt.Errorf("schedules.list: bad cursor: %w", err)
		}
		cursorTimeArg = formatTime(t)
		cursorIDArg = id
	}
	rows, err := s.q(tx).QueryContext(ctx,
		`SELECT `+scheduleCols+`
		   FROM rimsky_schedules
		  WHERE (? IS NULL OR node_id = ?)
		    AND (? IS NULL OR (next_fire_at, node_id) > (?, ?))
		  ORDER BY next_fire_at ASC, node_id ASC
		  LIMIT ?`,
		nodeArg, nodeArg, cursorTimeArg, cursorTimeArg, cursorIDArg, limit,
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

func collectSchedules(rows *sql.Rows) ([]persistence.ScheduleRow, error) {
	var out []persistence.ScheduleRow
	for rows.Next() {
		r, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func scanSchedule(sc scannable) (persistence.ScheduleRow, error) {
	var (
		nodeIDStr      string
		cronExpr       string
		nextFireAtStr  string
		lastFiredAtStr sql.NullString
	)
	if err := sc.Scan(&nodeIDStr, &cronExpr, &nextFireAtStr, &lastFiredAtStr); err != nil {
		return persistence.ScheduleRow{}, err
	}
	id, err := parseUUID(nodeIDStr)
	if err != nil {
		return persistence.ScheduleRow{}, err
	}
	nextFireAt, err := parseTime(nextFireAtStr)
	if err != nil {
		return persistence.ScheduleRow{}, err
	}
	out := persistence.ScheduleRow{
		NodeID:     id,
		CronExpr:   cronExpr,
		NextFireAt: nextFireAt,
	}
	if lastFiredAtStr.Valid {
		t, err := parseTime(lastFiredAtStr.String)
		if err != nil {
			return persistence.ScheduleRow{}, err
		}
		out.LastFiredAt = &t
	}
	return out, nil
}
