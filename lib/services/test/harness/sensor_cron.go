// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package harness

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcnet "github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
)

// sensorCronImage is the locally-built production cron sensor image.
// Built by `make service-images`.
const sensorCronImage = "rimsky-sensor-cron:latest"

// SensorCronHandle is the bring-up handle for a rimsky-sensor-cron peer
// container. Endpoint is the in-network gRPC endpoint (the value to pass
// to BringUpRimsky via WithPublisher). Restart terminates the live
// container and brings up a fresh one with identical env, so the test
// can exercise the restart-recovery path: durable state survives in the
// configured state DSN, but the in-memory watches are dropped on the
// terminate and rebuilt by the fresh container's AttachStateDB.
//
// The Postgres state DSN passed at construction time persists across
// Restart (the DSN points at a sibling Postgres container; that
// container is NOT torn down between sensor restarts), which is what
// the durability proof requires.
type SensorCronHandle struct {
	Endpoint string

	ctx         context.Context
	t           testing.TB
	networkName string
	alias       string
	stateDSN    string

	container testcontainers.Container
}

// StartSensorCron brings up the rimsky-sensor-cron image on the given
// docker network with the given alias, returning a handle. The state
// DSN is passed via RIMSKY_SENSOR_CRON_STATE_DSN; pass "" for the
// in-memory default.
//
// rimsky's stable in-network alias `http://rimsky:8080` is the message
// endpoint; the sensor's gRPC Publisher server listens on port 9081
// (matching dockerfiles/Dockerfile.sensor-cron's EXPOSE).
//
// Cleanup is registered via t.Cleanup; fails hard (t.Fatal) when the
// image is missing — the harness never t.Skip's.
func StartSensorCron(ctx context.Context, t testing.TB, networkName, alias, stateDSN string) *SensorCronHandle {
	t.Helper()
	h := &SensorCronHandle{
		ctx:         ctx,
		t:           t,
		networkName: networkName,
		alias:       alias,
		stateDSN:    stateDSN,
	}
	h.container = runSensorCronContainer(ctx, t, networkName, alias, stateDSN)
	h.Endpoint = fmt.Sprintf("%s:9081", alias)
	t.Cleanup(func() {
		if h.container == nil {
			return
		}
		termCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = h.container.Terminate(termCtx)
	})
	return h
}

// Stop terminates the live sensor-cron container without bringing a new
// one up. Used by restart-recovery tests to drop the process between
// Subscribe (persisted via state DSN) and the recovered fire.
func (h *SensorCronHandle) Stop(ctx context.Context) {
	h.t.Helper()
	if h.container == nil {
		return
	}
	termCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	_ = h.container.Terminate(termCtx)
	h.container = nil
}

// Restart brings up a fresh sensor-cron container with identical
// network / alias / state DSN, replacing any live container. The fresh
// container's AttachStateDB rebuilds in-memory watches from durable
// rows — recovering each subscription's ORIGINALLY-scheduled
// `next_fire_at` rather than recomputing it from wall-clock. The DSN
// points at a sibling Postgres container that survives the restart, so
// the durable state persists across the call.
func (h *SensorCronHandle) Restart(ctx context.Context) {
	h.t.Helper()
	h.Stop(ctx)
	h.container = runSensorCronContainer(ctx, h.t, h.networkName, h.alias, h.stateDSN)
}

// runSensorCronContainer starts one rimsky-sensor-cron container with
// the given env wiring. Shared by StartSensorCron and
// SensorCronHandle.Restart so the bring-up shape is identical on
// initial boot and after a restart.
func runSensorCronContainer(ctx context.Context, t testing.TB, networkName, alias, stateDSN string) testcontainers.Container {
	t.Helper()
	env := map[string]string{
		"RIMSKY_SENSOR_CRON_PORT": "9081",
		// rimsky's stable in-network alias; the sensor POSTs message
		// envelopes here.
		"RIMSKY_ENDPOINT": "http://rimsky:8080",
	}
	if stateDSN != "" {
		// Durability gate. When set, sensor-cron persists active
		// publisher-subscriptions + their next_fire_at watermarks to
		// the configured Postgres so a process restart resumes the
		// originally-scheduled window. Empty → in-memory default.
		env["RIMSKY_SENSOR_CRON_STATE_DSN"] = stateDSN
	}
	c, err := testcontainers.Run(ctx, sensorCronImage,
		tcnet.WithNetworkName([]string{alias}, networkName),
		testcontainers.WithEnv(env),
		testcontainers.WithExposedPorts("9081/tcp"),
		testcontainers.WithWaitStrategy(
			wait.ForListeningPort("9081/tcp").WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("harness: start sensor-cron: %v", err)
	}
	return c
}
