// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// sensors.go — F8. Sensor lifecycle helpers consumed by the control-api.
//
// Spec
// .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
// §Sensors / Service kind shape + Resync after sensor restart.
//
// @concept: sensor
//
// The control-api calls these helpers from the instance-create and
// instance-terminate flows; the supervisor calls `ResyncSensorWatches`
// at startup. Each helper is a thin facade over a `SensorRegistry`
// (operator-supplied) so the wire dependency stays out of the
// controlapi package.

package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/foundation/shared"
	"github.com/fallguy/rimsky/foundation/spec"
)

// SensorClient is the rimsky-side surface every sensor-service binding
// satisfies. Implementations live in `runtime/remote/sensor_client.go`
// (the gRPC client) or in test fixtures.
type SensorClient interface {
	Name() string

	// StartWatch begins a watch on the sensor service. The watch_id is
	// rimsky-generated UUIDv7; the sensor binds it internally.
	StartWatch(ctx context.Context, req StartWatchRequest) error

	// StopWatch stops a previously-started watch.
	StopWatch(ctx context.Context, watchID shared.UUID) error

	// ListWatches enumerates the watches the sensor currently has.
	// Used by `ResyncSensorWatches` to reconcile state after rimsky
	// (or the sensor) restarts.
	ListWatches(ctx context.Context) ([]ListedSensorWatch, error)
}

// StartWatchRequest is the rimsky-side payload for SensorClient.StartWatch.
// Mirrors the proto StartWatchRequest with `[]byte` config bytes per
// `@blessed-invariant 21` (config is opaque to rimsky once resolved).
type StartWatchRequest struct {
	WatchID        shared.UUID
	InstanceID     shared.UUID
	Kind           string
	ResolvedConfig []byte
}

// ListedSensorWatch is the rimsky-side projection of the proto
// WatchDescriptor returned by Sensor.ListWatches.
type ListedSensorWatch struct {
	WatchID    shared.UUID
	InstanceID shared.UUID
	Kind       string
}

// SensorRegistry resolves a sensor name (as declared in `rimsky.yml`)
// to the corresponding SensorClient. Returns ok=false when the named
// sensor is not configured on this process.
type SensorRegistry interface {
	Get(name string) (SensorClient, bool)
	// All returns every registered SensorClient. Used by the resync
	// sweeper, which fans out across the full set.
	All() []SensorClient
}

// StartWatchesForInstance walks the template's `sensors:` block, for
// each sensor: generates a watch_id, INSERTs the
// `table:rimsky_sensor_watches` row with `state = active`, and calls
// `SensorClient.StartWatch` on the registered sensor service. Per
// spec §Per-instance parameterization, sensor StartWatch failures
// leave the row at `state = failed` and log; they do NOT block
// instance creation (operator-recoverable via resync).
//
// `clock` and `logger` are explicit parameters per cold-read style.
func StartWatchesForInstance(
	ctx context.Context, deps SensorLifecycleDeps,
	instanceID shared.UUID, params map[string]any, sensors []spec.SensorSpec,
) error {
	if len(sensors) == 0 {
		return nil
	}
	now := deps.Clock.Now().UTC()
	for _, s := range sensors {
		watchID := shared.UUID(uuid.New())
		resolvedConfig, err := resolveSensorConfig(s.Config, params)
		if err != nil {
			deps.Logger.Warn("sensor.start.resolve_failed",
				"sensor_name", s.Name,
				"instance_id", instanceID.String(),
				"error", err.Error())
			continue
		}
		onObs, err := json.Marshal(s.OnObservation)
		if err != nil {
			deps.Logger.Warn("sensor.start.on_observation_marshal_failed",
				"sensor_name", s.Name,
				"instance_id", instanceID.String(),
				"error", err.Error())
			continue
		}
		row := persistence.SensorWatchRow{
			ID:             watchID,
			InstanceID:     instanceID,
			SensorName:     s.Name,
			Kind:           s.Kind,
			ResolvedConfig: resolvedConfig,
			OnObservation:  onObs,
			State:          persistence.SensorWatchStateActive,
			StartedAt:      &now,
		}
		// INSERT first so a startwatch failure still leaves an
		// auditable row. State flips to failed on RPC error below.
		if err := deps.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			return deps.Persist.SensorWatches().Insert(ctx, tx, row)
		}); err != nil {
			deps.Logger.Warn("sensor.start.row_insert_failed",
				"sensor_name", s.Name,
				"instance_id", instanceID.String(),
				"watch_id", watchID.String(),
				"error", err.Error())
			continue
		}
		client, ok := deps.Sensors.Get(s.Name)
		if !ok {
			deps.Logger.Warn("sensor.start.unknown_sensor",
				"sensor_name", s.Name,
				"instance_id", instanceID.String(),
				"watch_id", watchID.String())
			markWatchFailed(ctx, deps, watchID)
			continue
		}
		if err := client.StartWatch(ctx, StartWatchRequest{
			WatchID:        watchID,
			InstanceID:     instanceID,
			Kind:           s.Kind,
			ResolvedConfig: resolvedConfig,
		}); err != nil {
			deps.Logger.Warn("sensor.start.rpc_failed",
				"sensor_name", s.Name,
				"instance_id", instanceID.String(),
				"watch_id", watchID.String(),
				"error", err.Error())
			markWatchFailed(ctx, deps, watchID)
			continue
		}
	}
	return nil
}

// StopWatchesForInstance walks active watches for the instance and
// calls `SensorClient.StopWatch` on each; sets `state = stopped`. On
// any per-watch failure, the loop continues so a single broken
// sensor cannot block instance termination.
func StopWatchesForInstance(
	ctx context.Context, deps SensorLifecycleDeps,
	instanceID shared.UUID,
) error {
	watches, err := deps.Persist.SensorWatches().ListByInstance(ctx, instanceID)
	if err != nil {
		return fmt.Errorf("StopWatchesForInstance: list: %w", err)
	}
	for _, w := range watches {
		if w.State != persistence.SensorWatchStateActive {
			continue
		}
		client, ok := deps.Sensors.Get(w.SensorName)
		if !ok {
			deps.Logger.Warn("sensor.stop.unknown_sensor",
				"sensor_name", w.SensorName,
				"instance_id", instanceID.String(),
				"watch_id", w.ID.String())
			markWatchStopped(ctx, deps, w.ID)
			continue
		}
		if err := client.StopWatch(ctx, w.ID); err != nil {
			deps.Logger.Warn("sensor.stop.rpc_failed",
				"sensor_name", w.SensorName,
				"instance_id", instanceID.String(),
				"watch_id", w.ID.String(),
				"error", err.Error())
			// Leave at active so resync can retry; do not flip to stopped.
			continue
		}
		markWatchStopped(ctx, deps, w.ID)
	}
	return nil
}

// ResyncSensorWatches is invoked at supervisor startup. For each
// configured sensor it:
//
//  1. Calls `Sensor.ListWatches()` to enumerate what the sensor sees.
//  2. Lists `table:rimsky_sensor_watches` rows in `state = active`
//     for that sensor name.
//  3. For watches rimsky expects but the sensor doesn't report,
//     re-issues `StartWatch` to restore.
//  4. For orphan watches the sensor reports but rimsky doesn't know
//     about, issues `StopWatch` and logs at WARN.
//
// Errors from individual sensors are logged and the sweep continues
// across the remaining set — one broken sensor cannot wedge the rest.
func ResyncSensorWatches(ctx context.Context, deps SensorLifecycleDeps) error {
	if deps.Sensors == nil {
		return nil
	}
	expected, err := deps.Persist.SensorWatches().ListByState(ctx, persistence.SensorWatchStateActive)
	if err != nil {
		return fmt.Errorf("ResyncSensorWatches: list active: %w", err)
	}
	expectedBySensor := map[string][]persistence.SensorWatchRow{}
	for _, w := range expected {
		expectedBySensor[w.SensorName] = append(expectedBySensor[w.SensorName], w)
	}
	for _, client := range deps.Sensors.All() {
		live, err := client.ListWatches(ctx)
		if err != nil {
			deps.Logger.Warn("sensor.resync.list_failed",
				"sensor_name", client.Name(),
				"error", err.Error())
			continue
		}
		liveSet := map[shared.UUID]struct{}{}
		for _, l := range live {
			liveSet[l.WatchID] = struct{}{}
		}
		// Rimsky-expected, sensor-missing → restart.
		for _, w := range expectedBySensor[client.Name()] {
			if _, ok := liveSet[w.ID]; ok {
				continue
			}
			if err := client.StartWatch(ctx, StartWatchRequest{
				WatchID:        w.ID,
				InstanceID:     w.InstanceID,
				Kind:           w.Kind,
				ResolvedConfig: w.ResolvedConfig,
			}); err != nil {
				deps.Logger.Warn("sensor.resync.restart_failed",
					"sensor_name", client.Name(),
					"watch_id", w.ID.String(),
					"error", err.Error())
			}
		}
		// Sensor-reported, rimsky-unknown → stop + log.
		expectedSet := map[shared.UUID]struct{}{}
		for _, w := range expectedBySensor[client.Name()] {
			expectedSet[w.ID] = struct{}{}
		}
		for _, l := range live {
			if _, ok := expectedSet[l.WatchID]; ok {
				continue
			}
			deps.Logger.Warn("sensor.resync.orphan_watch",
				"sensor_name", client.Name(),
				"watch_id", l.WatchID.String(),
				"instance_id", l.InstanceID.String(),
				"kind", l.Kind)
			if err := client.StopWatch(ctx, l.WatchID); err != nil {
				deps.Logger.Warn("sensor.resync.stop_orphan_failed",
					"sensor_name", client.Name(),
					"watch_id", l.WatchID.String(),
					"error", err.Error())
			}
		}
	}
	return nil
}

// SensorLifecycleDeps is the dependency capsule for the sensor
// lifecycle helpers. Struct-shaped so the call sites stay compact and
// the deps surface is easy to grep.
type SensorLifecycleDeps struct {
	Persist persistence.Tables
	Sensors SensorRegistry
	Clock   shared.Clock
	Logger  shared.Logger
}

// resolveSensorConfig applies `{{params.X}}` substitution to the
// sensor config blob. The implementation is intentionally minimal —
// the graph-side substitution layer (graph/attribute/substitution.go)
// is the canonical site for richer template grammars. Here we walk
// JSON leaves and resolve `{{params.<path>}}` references against the
// instance params map.
func resolveSensorConfig(raw []byte, params map[string]any) ([]byte, error) {
	if len(raw) == 0 {
		return raw, nil
	}
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("sensor config not valid JSON: %w", err)
	}
	resolved := walkSensorConfig(doc, params)
	out, err := json.Marshal(resolved)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func walkSensorConfig(v any, params map[string]any) any {
	switch val := v.(type) {
	case string:
		return resolveParamsLeaf(val, params)
	case map[string]any:
		out := make(map[string]any, len(val))
		for k, inner := range val {
			out[k] = walkSensorConfig(inner, params)
		}
		return out
	case []any:
		out := make([]any, len(val))
		for i, inner := range val {
			out[i] = walkSensorConfig(inner, params)
		}
		return out
	default:
		return val
	}
}

func resolveParamsLeaf(s string, params map[string]any) any {
	if len(s) < 4 {
		return s
	}
	if s[:2] != "{{" || s[len(s)-2:] != "}}" {
		return s
	}
	inner := s[2 : len(s)-2]
	const prefix = "params."
	if len(inner) <= len(prefix) || inner[:len(prefix)] != prefix {
		return s
	}
	path := inner[len(prefix):]
	if v, ok := lookupParam(params, path); ok {
		return v
	}
	return s
}

func lookupParam(params map[string]any, path string) (any, bool) {
	if params == nil {
		return nil, false
	}
	// dotted path
	cur := any(params)
	for _, seg := range splitDots(path) {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = m[seg]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

func splitDots(s string) []string {
	out := []string{}
	cur := ""
	for _, r := range s {
		if r == '.' {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

func markWatchFailed(ctx context.Context, deps SensorLifecycleDeps, watchID shared.UUID) {
	state := persistence.SensorWatchStateFailed
	_ = deps.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return deps.Persist.SensorWatches().Update(ctx, tx, watchID, persistence.SensorWatchUpdate{State: &state})
	})
}

func markWatchStopped(ctx context.Context, deps SensorLifecycleDeps, watchID shared.UUID) {
	state := persistence.SensorWatchStateStopped
	_ = deps.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return deps.Persist.SensorWatches().Update(ctx, tx, watchID, persistence.SensorWatchUpdate{State: &state})
	})
}

// keep time alive when the file compiles with paths excluded.
var _ = time.Time{}
var _ = errors.New
