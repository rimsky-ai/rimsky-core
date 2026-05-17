// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// SQLite impl of persistence.SensorWatchesTable — mirror of the
// postgres impl. SQLite is dev-only.

package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/foundation/shared"
)

type sensorWatchesImpl tablesImpl

var _ persistence.SensorWatchesTable = (*sensorWatchesImpl)(nil)

func (s *tablesImpl) SensorWatches() persistence.SensorWatchesTable {
	return (*sensorWatchesImpl)(s)
}

func (b *sensorWatchesImpl) q(tx persistence.Tx) querier { return (*tablesImpl)(b).q(tx) }

const sqliteInsertSensorWatchSQL = `
INSERT INTO rimsky_sensor_watches (
    id, instance_id, sensor_name, kind, resolved_config, on_observation,
    started_at, last_observed_at, state
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`

func (b *sensorWatchesImpl) Insert(ctx context.Context, tx persistence.Tx, row persistence.SensorWatchRow) error {
	if row.State == "" {
		row.State = persistence.SensorWatchStateActive
	}
	_, err := b.q(tx).ExecContext(ctx, sqliteInsertSensorWatchSQL,
		row.ID.String(), row.InstanceID.String(), row.SensorName, row.Kind,
		row.ResolvedConfig, row.OnObservation, row.StartedAt,
		row.LastObservedAt, row.State)
	if err != nil {
		return fmt.Errorf("sqlite.SensorWatches.Insert: %w", err)
	}
	return nil
}

func (b *sensorWatchesImpl) Update(ctx context.Context, tx persistence.Tx, id shared.UUID, upd persistence.SensorWatchUpdate) error {
	sets := []string{}
	args := []any{}
	if upd.State != nil {
		args = append(args, *upd.State)
		sets = append(sets, "state = ?")
	}
	if upd.LastObservedAt != nil {
		args = append(args, *upd.LastObservedAt)
		sets = append(sets, "last_observed_at = ?")
	}
	if upd.StartedAt != nil {
		args = append(args, *upd.StartedAt)
		sets = append(sets, "started_at = ?")
	}
	if len(sets) == 0 {
		return nil
	}
	args = append(args, id.String())
	sql := fmt.Sprintf(`UPDATE rimsky_sensor_watches SET %s WHERE id = ?`,
		strings.Join(sets, ", "))
	if _, err := b.q(tx).ExecContext(ctx, sql, args...); err != nil {
		return fmt.Errorf("sqlite.SensorWatches.Update: %w", err)
	}
	return nil
}

const sqliteDeleteSensorWatchSQL = `DELETE FROM rimsky_sensor_watches WHERE id = ?`

func (b *sensorWatchesImpl) Delete(ctx context.Context, tx persistence.Tx, id shared.UUID) error {
	if _, err := b.q(tx).ExecContext(ctx, sqliteDeleteSensorWatchSQL, id.String()); err != nil {
		return fmt.Errorf("sqlite.SensorWatches.Delete: %w", err)
	}
	return nil
}

const sqliteListSensorWatchesByInstanceSQL = `
SELECT id, instance_id, sensor_name, kind, resolved_config, on_observation,
       started_at, last_observed_at, state
  FROM rimsky_sensor_watches
 WHERE instance_id = ?
 ORDER BY sensor_name ASC`

func (b *sensorWatchesImpl) ListByInstance(ctx context.Context, instanceID shared.UUID) ([]persistence.SensorWatchRow, error) {
	rows, err := (*tablesImpl)(b).db.QueryContext(ctx, sqliteListSensorWatchesByInstanceSQL, instanceID.String())
	if err != nil {
		return nil, fmt.Errorf("sqlite.SensorWatches.ListByInstance: %w", err)
	}
	defer rows.Close()
	return scanSensorWatches(rows)
}

const sqliteListSensorWatchesByStateSQL = `
SELECT id, instance_id, sensor_name, kind, resolved_config, on_observation,
       started_at, last_observed_at, state
  FROM rimsky_sensor_watches
 WHERE state = ?`

func (b *sensorWatchesImpl) ListByState(ctx context.Context, state string) ([]persistence.SensorWatchRow, error) {
	rows, err := (*tablesImpl)(b).db.QueryContext(ctx, sqliteListSensorWatchesByStateSQL, state)
	if err != nil {
		return nil, fmt.Errorf("sqlite.SensorWatches.ListByState: %w", err)
	}
	defer rows.Close()
	return scanSensorWatches(rows)
}

const sqliteGetSensorWatchSQL = `
SELECT id, instance_id, sensor_name, kind, resolved_config, on_observation,
       started_at, last_observed_at, state
  FROM rimsky_sensor_watches
 WHERE id = ?`

func (b *sensorWatchesImpl) Get(ctx context.Context, id shared.UUID) (*persistence.SensorWatchRow, error) {
	rows, err := (*tablesImpl)(b).db.QueryContext(ctx, sqliteGetSensorWatchSQL, id.String())
	if err != nil {
		return nil, fmt.Errorf("sqlite.SensorWatches.Get: %w", err)
	}
	defer rows.Close()
	out, err := scanSensorWatches(rows)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, nil
	}
	return &out[0], nil
}

func scanSensorWatches(rows *sql.Rows) ([]persistence.SensorWatchRow, error) {
	out := []persistence.SensorWatchRow{}
	for rows.Next() {
		var w persistence.SensorWatchRow
		var idStr, instanceStr string
		var startedAt sql.NullTime
		var lastObservedAt sql.NullTime
		if err := rows.Scan(
			&idStr, &instanceStr, &w.SensorName, &w.Kind,
			&w.ResolvedConfig, &w.OnObservation,
			&startedAt, &lastObservedAt, &w.State,
		); err != nil {
			return nil, err
		}
		if u, err := uuid.Parse(idStr); err == nil {
			w.ID = u
		}
		if u, err := uuid.Parse(instanceStr); err == nil {
			w.InstanceID = u
		}
		if startedAt.Valid {
			t := startedAt.Time
			w.StartedAt = &t
		}
		if lastObservedAt.Valid {
			t := lastObservedAt.Time
			w.LastObservedAt = &t
		}
		out = append(out, w)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
