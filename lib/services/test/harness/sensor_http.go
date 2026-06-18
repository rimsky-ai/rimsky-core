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

const sensorHTTPImage = "rimsky-sensor-http:latest"

func StartSensorHTTP(ctx context.Context, t testing.TB, networkName, alias string, hostAccessPorts ...int) (endpoint string) {
	t.Helper()
	c := runSensorHTTPContainer(ctx, t, networkName, alias, "", hostAccessPorts)
	t.Cleanup(func() {
		termCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = c.Terminate(termCtx)
	})

	return fmt.Sprintf("%s:9082", alias)
}

type SensorHTTPHandle struct {
	Endpoint string

	t               testing.TB
	networkName     string
	alias           string
	stateDSN        string
	hostAccessPorts []int

	container testcontainers.Container
}

func StartSensorHTTPHandle(ctx context.Context, t testing.TB, networkName, alias, stateDSN string, hostAccessPorts ...int) *SensorHTTPHandle {
	t.Helper()
	h := &SensorHTTPHandle{
		t:               t,
		networkName:     networkName,
		alias:           alias,
		stateDSN:        stateDSN,
		hostAccessPorts: append([]int(nil), hostAccessPorts...),
	}
	h.container = runSensorHTTPContainer(ctx, t, networkName, alias, stateDSN, h.hostAccessPorts)
	h.Endpoint = fmt.Sprintf("%s:9082", alias)
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

func (h *SensorHTTPHandle) Stop(ctx context.Context) {
	h.t.Helper()
	if h.container == nil {
		return
	}
	termCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	_ = h.container.Terminate(termCtx)
	h.container = nil
}

func (h *SensorHTTPHandle) Restart(ctx context.Context) {
	h.t.Helper()
	h.Stop(ctx)
	h.container = runSensorHTTPContainer(ctx, h.t, h.networkName, h.alias, h.stateDSN, h.hostAccessPorts)
}

func runSensorHTTPContainer(ctx context.Context, t testing.TB, networkName, alias, stateDSN string, hostAccessPorts []int) testcontainers.Container {
	t.Helper()
	env := map[string]string{
		"RIMSKY_SENSOR_HTTP_PORT": "9082",
		"RIMSKY_ENDPOINT":         "http://rimsky:8080",
	}
	if stateDSN != "" {
		env["RIMSKY_SENSOR_HTTP_STATE_DSN"] = stateDSN
	}
	opts := []testcontainers.ContainerCustomizer{
		tcnet.WithNetworkName([]string{alias}, networkName),
		testcontainers.WithEnv(env),
		testcontainers.WithExposedPorts("9082/tcp"),
		testcontainers.WithWaitStrategy(
			wait.ForListeningPort("9082/tcp").WithStartupTimeout(60 * time.Second),
		),
	}
	if len(hostAccessPorts) > 0 {
		opts = append(opts, testcontainers.WithHostPortAccess(hostAccessPorts...))
	}
	c, err := testcontainers.Run(ctx, sensorHTTPImage, opts...)
	if err != nil {
		t.Fatalf("harness: start sensor-http: %v", err)
	}
	return c
}
