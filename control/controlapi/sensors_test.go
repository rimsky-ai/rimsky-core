// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// sensors_test.go — F3 + F8 integration tests using a fake
// SensorRegistry. Covers:
//
//   - POST /sensors/{watch_id}/observations (F3) — enqueues a message
//     under the sensor's name; updates last_observed_at.
//   - Sensor lifecycle on instance create (F8) — StartWatch fires per
//     template `sensors:` entry; row INSERTed with state=active.

package controlapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/foundation/locks"
	"github.com/fallguy/rimsky/foundation/locks/storetest"
	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/foundation/shared"
	"github.com/fallguy/rimsky/internal/pgtest"
	"github.com/fallguy/rimsky/runtime"
)

// fakeSensor captures StartWatch / StopWatch / ListWatches calls so
// tests can assert on the lifecycle.
type fakeSensor struct {
	name string
	mu   sync.Mutex
	live map[shared.UUID]runtime.ListedSensorWatch
	// startCalls / stopCalls capture the call sequence for ordering
	// assertions.
	startCalls []shared.UUID
	stopCalls  []shared.UUID
}

func newFakeSensor(name string) *fakeSensor {
	return &fakeSensor{
		name: name,
		live: map[shared.UUID]runtime.ListedSensorWatch{},
	}
}

func (s *fakeSensor) Name() string { return s.name }

func (s *fakeSensor) StartWatch(_ context.Context, req runtime.StartWatchRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.startCalls = append(s.startCalls, req.WatchID)
	s.live[req.WatchID] = runtime.ListedSensorWatch{
		WatchID:    req.WatchID,
		InstanceID: req.InstanceID,
		Kind:       req.Kind,
	}
	return nil
}

func (s *fakeSensor) StopWatch(_ context.Context, id shared.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopCalls = append(s.stopCalls, id)
	delete(s.live, id)
	return nil
}

func (s *fakeSensor) ListWatches(_ context.Context) ([]runtime.ListedSensorWatch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]runtime.ListedSensorWatch, 0, len(s.live))
	for _, w := range s.live {
		out = append(out, w)
	}
	return out, nil
}

// fakeSensorRegistry is a trivial in-memory SensorRegistry for tests.
type fakeSensorRegistry struct {
	byName map[string]runtime.SensorClient
}

func newFakeSensorRegistry(sensors ...runtime.SensorClient) *fakeSensorRegistry {
	r := &fakeSensorRegistry{byName: map[string]runtime.SensorClient{}}
	for _, s := range sensors {
		r.byName[s.Name()] = s
	}
	return r
}

func (r *fakeSensorRegistry) Get(name string) (runtime.SensorClient, bool) {
	c, ok := r.byName[name]
	return c, ok
}

func (r *fakeSensorRegistry) All() []runtime.SensorClient {
	out := make([]runtime.SensorClient, 0, len(r.byName))
	for _, c := range r.byName {
		out = append(out, c)
	}
	return out
}

// sensorHarness extends the standard harness with a fake sensor
// registry wired into AppDeps. Returns the underlying *fakeSensor so
// tests can assert on captured lifecycle calls.
type sensorHarness struct {
	*harness
	sensor *fakeSensor
}

func newSensorHarness(t *testing.T) (*sensorHarness, func()) {
	t.Helper()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)

	reg := locks.NewRegistry()
	contentFake := storetest.NewFake("content", locks.Capabilities{WriteSemanticsAllowed: []locks.WriteSemantics{locks.WriteSemanticsSync}})
	topicsFake := storetest.NewFake("topics-ring", locks.Capabilities{WriteSemanticsAllowed: []locks.WriteSemantics{locks.WriteSemanticsSync}})
	reg.Add("content", contentFake)
	reg.Add("topics-ring", topicsFake)
	lcReg := locks.NewLifecycleRegistry()
	lcReg.Add("content", contentFake)
	lcReg.Add("topics-ring", topicsFake)

	sensor := newFakeSensor("sensor-cron")
	capLog := shared.NewCapturingLogger()
	app := NewApp(AppDeps{
		Persist:       d.Tables(),
		Queue:         d.Queue(),
		Clock:         shared.SystemClock{},
		Logger:        capLog,
		Stores:        reg,
		LifecycleSubs: lcReg,
		NamedLocks: locks.NamedLocksConfig{
			Locks: map[string]locks.NamedLockConfig{
				"topics-ring:concurrent": {Limit: 5},
			},
		},
		Executors: map[string]ExecutorEntry{
			"worker": {Transport: "grpc", Endpoint: "localhost:0"},
		},
		Sensors: newFakeSensorRegistry(sensor),
	})
	srv := httptest.NewServer(app)
	h := &harness{srv: srv, driver: d, persist: d.Tables(), stores: reg, logger: capLog}
	sh := &sensorHarness{harness: h, sensor: sensor}
	return sh, func() {
		srv.Close()
	}
}

// templateWithSensors returns a wrapped POST /templates body declaring
// one sensor under the `sensor-cron` name.
func templateWithSensors(name string) map[string]any {
	return map[string]any{
		"spec": map[string]any{
			"name":                  name,
			"version":               "v1",
			"frame_resolution_mode": "serial_queue",
			"nodes": []map[string]any{
				{"type": "scheduled-fire", "executor": "worker"},
			},
			"sensors": []map[string]any{
				{
					"name": "sensor-cron",
					"kind": "cron",
					"config": map[string]any{
						"cron":         "*/5 * * * *",
						"missed_fires": "drop",
					},
					"on_observation": map[string]any{
						"target_node":  "scheduled-fire",
						"message_kind": "invalidate",
						"payload_template": map[string]any{
							"fired_at": "{{observation.fired_at}}",
						},
					},
				},
			},
		},
	}
}

// TestSensorLifecycle_StartOnCreateStopOnDelete confirms that
// instance create + delete fire `Sensor.StartWatch` / `StopWatch` per
// the template's `sensors:` block, and the watch row lands in the
// expected state.
func TestSensorLifecycle_StartOnCreateStopOnDelete(t *testing.T) {
	t.Parallel()
	sh, teardown := newSensorHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	tplBody := templateWithSensors("sensor-lc-" + uuid.NewString())
	_, out := sh.httpJSON(t, "POST", "/templates", tplBody)
	tplID, _ := out["template_id"].(string)
	require.NotEmpty(t, tplID)
	deployStatus, _ := sh.httpJSON(t, "POST", "/templates/"+tplID+"/deploy", map[string]any{})
	require.Equal(t, http.StatusOK, deployStatus)
	status, out := sh.httpJSON(t, "POST", "/instances", map[string]any{
		"template":     tplID,
		"instance_key": "sensor-lc-ck-" + uuid.NewString(),
		"params":       map[string]any{"region": "us-east"},
	})
	require.Equal(t, http.StatusCreated, status, out)
	instID, _ := out["instance_id"].(string)
	instUUID, err := uuid.Parse(instID)
	require.NoError(t, err)

	// One StartWatch call landed.
	require.Equal(t, 1, len(sh.sensor.startCalls), "expected one StartWatch call")

	// Watch row persisted with state=active.
	watches, err := sh.persist.SensorWatches().ListByInstance(ctx, shared.UUID(instUUID))
	require.NoError(t, err)
	require.Equal(t, 1, len(watches))
	require.Equal(t, persistence.SensorWatchStateActive, watches[0].State)
	require.Equal(t, "sensor-cron", watches[0].SensorName)

	// Mark terminated + delete → StopWatch fires.
	pgtest.ExecForTest(ctx, t, sh.driver,
		`UPDATE rimsky_instances SET terminated_at = now() WHERE id = $1`, instID)
	status, _ = sh.httpJSON(t, "DELETE", "/instances/"+instID, nil)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, 1, len(sh.sensor.stopCalls), "expected one StopWatch call")
}

// TestSensorObservation_EnqueuesMessage confirms F3:
// POST /sensors/{watch_id}/observations enqueues a message envelope
// keyed by the sensor + on_observation config.
func TestSensorObservation_EnqueuesMessage(t *testing.T) {
	t.Parallel()
	sh, teardown := newSensorHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	tplBody := templateWithSensors("sensor-obs-" + uuid.NewString())
	_, out := sh.httpJSON(t, "POST", "/templates", tplBody)
	tplID, _ := out["template_id"].(string)
	deployStatus, _ := sh.httpJSON(t, "POST", "/templates/"+tplID+"/deploy", map[string]any{})
	require.Equal(t, http.StatusOK, deployStatus)
	status, out := sh.httpJSON(t, "POST", "/instances", map[string]any{
		"template":     tplID,
		"instance_key": "sensor-obs-ck-" + uuid.NewString(),
	})
	require.Equal(t, http.StatusCreated, status, out)
	instID, _ := out["instance_id"].(string)
	instUUID, err := uuid.Parse(instID)
	require.NoError(t, err)

	watches, err := sh.persist.SensorWatches().ListByInstance(ctx, shared.UUID(instUUID))
	require.NoError(t, err)
	require.Equal(t, 1, len(watches))
	watchID := watches[0].ID

	// Push an observation.
	body := map[string]any{
		"observation": map[string]any{
			"fired_at": "2026-05-15T12:00:00Z",
		},
	}
	status, out = sh.httpJSON(t, "POST", fmt.Sprintf("/sensors/%s/observations", watchID.String()), body)
	require.Equal(t, http.StatusCreated, status, out)
	msgID, _ := out["message_id"].(string)
	require.NotEmpty(t, msgID)

	// Verify message persisted with sender=sensor-cron + target=scheduled-fire.
	mid, err := uuid.Parse(msgID)
	require.NoError(t, err)
	row, err := sh.persist.Messages().Get(ctx, shared.UUID(mid))
	require.NoError(t, err)
	require.NotNil(t, row)
	require.Equal(t, "invalidate", row.Kind)
	require.Equal(t, "sensor-cron", row.Sender)
	require.Equal(t, "sensor", row.SenderKind)
	require.Equal(t, "scheduled-fire", row.Target)

	// last_observed_at advanced.
	updated, err := sh.persist.SensorWatches().Get(ctx, watchID)
	require.NoError(t, err)
	require.NotNil(t, updated)
	require.NotNil(t, updated.LastObservedAt)

	// Payload carries the substituted field.
	var payload map[string]any
	require.NoError(t, json.Unmarshal(row.Payload, &payload))
	require.Equal(t, "2026-05-15T12:00:00Z", payload["fired_at"])
}

// TestSensorObservation_UnknownWatchReturns404 — pushing an
// observation against an unknown watch_id returns 404.
func TestSensorObservation_UnknownWatchReturns404(t *testing.T) {
	t.Parallel()
	sh, teardown := newSensorHarness(t)
	t.Cleanup(teardown)

	status, _ := sh.httpJSON(t, "POST",
		fmt.Sprintf("/sensors/%s/observations", uuid.NewString()),
		map[string]any{"observation": map[string]any{}})
	require.Equal(t, http.StatusNotFound, status)
}

// keep io import alive when build tags exclude paths above.
var _ = io.EOF
var _ = bytes.NewReader
