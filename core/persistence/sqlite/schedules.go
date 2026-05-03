// schedules.go — SQLite-backed persistence.ScheduleStore.
package sqlite

import (
	"context"
	"database/sql"
	"time"

	"github.com/fallguy/rimsky/core/persistence"
	"github.com/fallguy/rimsky/core/shared"
)

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
