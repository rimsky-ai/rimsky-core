// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package persistence

import (
	"context"
	"encoding/json"
	"time"

	"github.com/fallguy/rimsky/foundation/shared"
)

// SensorWatchState values for col:rimsky_sensor_watches.state.
const (
	SensorWatchStateActive  = "active"
	SensorWatchStateFailed  = "failed"
	SensorWatchStateStopped = "stopped"
)

// SensorWatchRow is the per-row representation of
// table:rimsky_sensor_watches. One row per (instance, sensor) declared
// in the template's `sensors:` block; lifecycle managed by
// control-api's instance-create / instance-terminate paths.
type SensorWatchRow struct {
	ID             shared.UUID
	InstanceID     shared.UUID
	SensorName     string
	Kind           string
	ResolvedConfig json.RawMessage
	OnObservation  json.RawMessage
	StartedAt      *time.Time
	LastObservedAt *time.Time
	State          string
}

// SensorWatchUpdate is the partial-update payload for
// SensorWatchesTable.Update.
type SensorWatchUpdate struct {
	State          *string
	LastObservedAt *time.Time
	StartedAt      *time.Time
}

// SensorWatchesTable is the per-row-type Table accessor for
// table:rimsky_sensor_watches.
type SensorWatchesTable interface {
	// Insert creates a new watch row. Called by control-api at instance
	// create after the sensor's StartWatch RPC returns OK.
	Insert(ctx context.Context, tx Tx, row SensorWatchRow) error

	// Update applies a partial update to a watch row.
	Update(ctx context.Context, tx Tx, id shared.UUID, upd SensorWatchUpdate) error

	// Delete removes a watch row. Called at instance termination after
	// StopWatch returns OK.
	Delete(ctx context.Context, tx Tx, id shared.UUID) error

	// ListByInstance returns all watches for an instance.
	ListByInstance(ctx context.Context, instanceID shared.UUID) ([]SensorWatchRow, error)

	// ListByState returns watches in a given state across all
	// instances. Used by the resync sweeper.
	ListByState(ctx context.Context, state string) ([]SensorWatchRow, error)

	// Get returns one watch by id, or nil when absent.
	Get(ctx context.Context, id shared.UUID) (*SensorWatchRow, error)
}
