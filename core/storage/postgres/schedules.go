// ScheduleStore — port of rimsky/src/storage/postgres/timer-store.ts adapted
// for the spec §11.1 rename: rimsky_timers → rimsky_schedules, keyed on
// node_id (no separate target_cell_id or reason columns).
package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/storage"
)

type ScheduleStore struct {
	pool *pgxpool.Pool
}

var _ storage.ScheduleStore = (*ScheduleStore)(nil)

const scheduleCols = `node_id, cron_expr, next_fire_at, last_fired_at`

// Register upserts the schedule row for a node.
func (s *ScheduleStore) Register(ctx context.Context, in storage.ScheduleRegisterInput, tx storage.Tx) error {
	ex := q(tx, s.pool)
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
func (s *ScheduleStore) DueBefore(ctx context.Context, cutoff time.Time, tx storage.Tx) ([]storage.ScheduleRow, error) {
	ex := q(tx, s.pool)
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

func (s *ScheduleStore) RecordFired(ctx context.Context, nodeID shared.UUID, nextFireAt, firedAt time.Time, tx storage.Tx) error {
	ex := q(tx, s.pool)
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
func (s *ScheduleStore) ForceFire(ctx context.Context, nodeID shared.UUID, tx storage.Tx) error {
	ex := q(tx, s.pool)
	_, err := ex.Exec(ctx,
		`UPDATE rimsky_schedules
		   SET next_fire_at = now()
		 WHERE node_id = $1`,
		nodeID,
	)
	return err
}

func (s *ScheduleStore) ListAll(ctx context.Context, tx storage.Tx) ([]storage.ScheduleRow, error) {
	ex := q(tx, s.pool)
	rows, err := ex.Query(ctx,
		`SELECT `+scheduleCols+` FROM rimsky_schedules ORDER BY next_fire_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectSchedules(rows)
}

func collectSchedules(rows interface {
	Next() bool
	Scan(dst ...any) error
	Err() error
}) ([]storage.ScheduleRow, error) {
	var out []storage.ScheduleRow
	for rows.Next() {
		var (
			r         storage.ScheduleRow
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
