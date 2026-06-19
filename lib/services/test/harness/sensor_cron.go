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

const sensorCronImage = "rimsky-sensor-cron:latest"

type SensorCronHandle struct {
	Endpoint string

	ctx            context.Context
	t              testing.TB
	networkName    string
	alias          string
	rimskyEndpoint string
	stateDSN       string

	container testcontainers.Container
}

func StartSensorCron(ctx context.Context, t testing.TB, networkName, alias, rimskyEndpoint, stateDSN string) *SensorCronHandle {
	t.Helper()
	uniqueAlias := fmt.Sprintf("%s-%d", alias, nextAliasSuffix())
	h := &SensorCronHandle{
		ctx:            ctx,
		t:              t,
		networkName:    networkName,
		alias:          uniqueAlias,
		rimskyEndpoint: rimskyEndpoint,
		stateDSN:       stateDSN,
	}
	h.container = runSensorCronContainer(ctx, t, networkName, uniqueAlias, rimskyEndpoint, stateDSN)
	h.Endpoint = fmt.Sprintf("%s:9081", uniqueAlias)
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

func (h *SensorCronHandle) Restart(ctx context.Context) {
	h.t.Helper()
	h.Stop(ctx)
	h.container = runSensorCronContainer(ctx, h.t, h.networkName, h.alias, h.rimskyEndpoint, h.stateDSN)
}

func runSensorCronContainer(ctx context.Context, t testing.TB, networkName, alias, rimskyEndpoint, stateDSN string) testcontainers.Container {
	t.Helper()
	if rimskyEndpoint == "" {
		rimskyEndpoint = "http://rimsky:8080"
	}
	env := map[string]string{
		"RIMSKY_SENSOR_CRON_PORT": "9081",
		"RIMSKY_ENDPOINT":         rimskyEndpoint,
	}
	if stateDSN != "" {
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
