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

func StartSensorHTTP(ctx context.Context, t testing.TB, networkName, alias, rimskyEndpoint string, hostAccessPorts ...int) (endpoint string) {
	t.Helper()
	uniqueAlias := fmt.Sprintf("%s-%d", alias, nextAliasSuffix())
	c := runSensorHTTPContainer(ctx, t, networkName, uniqueAlias, rimskyEndpoint, "", hostAccessPorts)
	t.Cleanup(func() {
		termCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = c.Terminate(termCtx)
	})

	return fmt.Sprintf("%s:9082", uniqueAlias)
}

type SensorHTTPHandle struct {
	Endpoint string

	t               testing.TB
	networkName     string
	alias           string
	rimskyEndpoint  string
	stateDSN        string
	hostAccessPorts []int

	container testcontainers.Container
}

func StartSensorHTTPHandle(ctx context.Context, t testing.TB, networkName, alias, rimskyEndpoint, stateDSN string, hostAccessPorts ...int) *SensorHTTPHandle {
	t.Helper()
	uniqueAlias := fmt.Sprintf("%s-%d", alias, nextAliasSuffix())
	h := &SensorHTTPHandle{
		t:               t,
		networkName:     networkName,
		alias:           uniqueAlias,
		rimskyEndpoint:  rimskyEndpoint,
		stateDSN:        stateDSN,
		hostAccessPorts: append([]int(nil), hostAccessPorts...),
	}
	h.container = runSensorHTTPContainer(ctx, t, networkName, uniqueAlias, rimskyEndpoint, stateDSN, h.hostAccessPorts)
	h.Endpoint = fmt.Sprintf("%s:9082", uniqueAlias)
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
	h.container = runSensorHTTPContainer(ctx, h.t, h.networkName, h.alias, h.rimskyEndpoint, h.stateDSN, h.hostAccessPorts)
}

func runSensorHTTPContainer(ctx context.Context, t testing.TB, networkName, alias, rimskyEndpoint, stateDSN string, hostAccessPorts []int) testcontainers.Container {
	t.Helper()
	if rimskyEndpoint == "" {
		rimskyEndpoint = "http://rimsky:8080"
	}
	env := map[string]string{
		"RIMSKY_SENSOR_HTTP_PORT":             "9082",
		"RIMSKY_ENDPOINT":                     rimskyEndpoint,
		"RIMSKY_SENSOR_HTTP_EGRESS_ALLOWLIST": "127.0.0.0/8,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,169.254.0.0/16,::1/128,fc00::/7,fe80::/10",
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
	c, err := runWithRetry(ctx, sensorHTTPImage, opts...)
	if err != nil {
		t.Fatalf("harness: start sensor-http: %v", err)
	}
	return c
}
