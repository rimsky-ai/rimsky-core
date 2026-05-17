// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Postgres impl of persistence.SensorWatchesTable — sensor lifecycle
// state per spec §Sensors / Per-instance parameterization.

package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/foundation/shared"
)

type sensorWatchesImpl tablesImpl

var _ persistence.SensorWatchesTable = (*sensorWatchesImpl)(nil)

// SensorWatches returns the postgres SensorWatchesTable impl.
func (s *tablesImpl) SensorWatches() persistence.SensorWatchesTable {
	return (*sensorWatchesImpl)(s)
}

func (b *sensorWatchesImpl) q(tx persistence.Tx) querier { return (*tablesImpl)(b).q(tx) }

const insertSensorWatchSQL = `
INSERT INTO rimsky_sensor_watches (
    id, instance_id, sensor_name, kind, resolved_config, on_observation,
    started_at, last_observed_at, state
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`

func (b *sensorWatchesImpl) Insert(ctx context.Context, tx persistence.Tx, row persistence.SensorWatchRow) error {
	if row.State == "" {
		row.State = persistence.SensorWatchStateActive
	}
	_, err := b.q(tx).Exec(ctx, insertSensorWatchSQL,
		row.ID, row.InstanceID, row.SensorName, row.Kind,
		row.ResolvedConfig, row.OnObservation, row.StartedAt,
		row.LastObservedAt, row.State)
	if err != nil {
		return fmt.Errorf("postgres.SensorWatches.Insert: %w", err)
	}
	return nil
}

func (b *sensorWatchesImpl) Update(ctx context.Context, tx persistence.Tx, id shared.UUID, upd persistence.SensorWatchUpdate) error {
	sets := []string{}
	args := []any{}
	if upd.State != nil {
		args = append(args, *upd.State)
		sets = append(sets, fmt.Sprintf("state = $%d", len(args)))
	}
	if upd.LastObservedAt != nil {
		args = append(args, *upd.LastObservedAt)
		sets = append(sets, fmt.Sprintf("last_observed_at = $%d", len(args)))
	}
	if upd.StartedAt != nil {
		args = append(args, *upd.StartedAt)
		sets = append(sets, fmt.Sprintf("started_at = $%d", len(args)))
	}
	if len(sets) == 0 {
		return nil
	}
	args = append(args, id)
	sql := fmt.Sprintf(`UPDATE rimsky_sensor_watches SET %s WHERE id = $%d`,
		strings.Join(sets, ", "), len(args))
	if _, err := b.q(tx).Exec(ctx, sql, args...); err != nil {
		return fmt.Errorf("postgres.SensorWatches.Update: %w", err)
	}
	return nil
}

const deleteSensorWatchSQL = `DELETE FROM rimsky_sensor_watches WHERE id = $1`

func (b *sensorWatchesImpl) Delete(ctx context.Context, tx persistence.Tx, id shared.UUID) error {
	if _, err := b.q(tx).Exec(ctx, deleteSensorWatchSQL, id); err != nil {
		return fmt.Errorf("postgres.SensorWatches.Delete: %w", err)
	}
	return nil
}

const listSensorWatchesByInstanceSQL = `
SELECT id, instance_id, sensor_name, kind, resolved_config, on_observation,
       started_at, last_observed_at, state
  FROM rimsky_sensor_watches
 WHERE instance_id = $1
 ORDER BY sensor_name ASC`

func (b *sensorWatchesImpl) ListByInstance(ctx context.Context, instanceID shared.UUID) ([]persistence.SensorWatchRow, error) {
	rows, err := (*tablesImpl)(b).pool.Query(ctx, listSensorWatchesByInstanceSQL, instanceID)
	if err != nil {
		return nil, fmt.Errorf("postgres.SensorWatches.ListByInstance: %w", err)
	}
	defer rows.Close()
	return collectSensorWatches(rows)
}

const listSensorWatchesByStateSQL = `
SELECT id, instance_id, sensor_name, kind, resolved_config, on_observation,
       started_at, last_observed_at, state
  FROM rimsky_sensor_watches
 WHERE state = $1`

func (b *sensorWatchesImpl) ListByState(ctx context.Context, state string) ([]persistence.SensorWatchRow, error) {
	rows, err := (*tablesImpl)(b).pool.Query(ctx, listSensorWatchesByStateSQL, state)
	if err != nil {
		return nil, fmt.Errorf("postgres.SensorWatches.ListByState: %w", err)
	}
	defer rows.Close()
	return collectSensorWatches(rows)
}

const getSensorWatchSQL = `
SELECT id, instance_id, sensor_name, kind, resolved_config, on_observation,
       started_at, last_observed_at, state
  FROM rimsky_sensor_watches
 WHERE id = $1`

func (b *sensorWatchesImpl) Get(ctx context.Context, id shared.UUID) (*persistence.SensorWatchRow, error) {
	rows, err := (*tablesImpl)(b).pool.Query(ctx, getSensorWatchSQL, id)
	if err != nil {
		return nil, fmt.Errorf("postgres.SensorWatches.Get: %w", err)
	}
	defer rows.Close()
	out, err := collectSensorWatches(rows)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, nil
	}
	return &out[0], nil
}

func collectSensorWatches(rows pgx.Rows) ([]persistence.SensorWatchRow, error) {
	out := []persistence.SensorWatchRow{}
	for rows.Next() {
		var w persistence.SensorWatchRow
		if err := rows.Scan(
			&w.ID, &w.InstanceID, &w.SensorName, &w.Kind,
			&w.ResolvedConfig, &w.OnObservation,
			&w.StartedAt, &w.LastObservedAt, &w.State,
		); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
