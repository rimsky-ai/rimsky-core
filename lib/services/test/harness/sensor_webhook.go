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

const sensorWebhookImage = "rimsky-sensor-webhook:latest"

type SensorWebhookHandle struct {
	GRPCEndpoint   string
	WebhookBaseURL string

	ctx            context.Context
	t              testing.TB
	networkName    string
	alias          string
	rimskyEndpoint string
	stateDSN       string

	container testcontainers.Container
}

func StartSensorWebhook(ctx context.Context, t testing.TB, networkName, alias, rimskyEndpoint string) *SensorWebhookHandle {
	return StartSensorWebhookWithState(ctx, t, networkName, alias, rimskyEndpoint, "")
}

func StartSensorWebhookWithState(ctx context.Context, t testing.TB, networkName, alias, rimskyEndpoint, stateDSN string) *SensorWebhookHandle {
	t.Helper()
	uniqueAlias := fmt.Sprintf("%s-%d", alias, nextAliasSuffix())
	h := &SensorWebhookHandle{
		ctx:            ctx,
		t:              t,
		networkName:    networkName,
		alias:          uniqueAlias,
		rimskyEndpoint: rimskyEndpoint,
		stateDSN:       stateDSN,
	}
	h.container, h.GRPCEndpoint, h.WebhookBaseURL = runSensorWebhookContainer(ctx, t, networkName, uniqueAlias, rimskyEndpoint, stateDSN)
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

func (h *SensorWebhookHandle) Stop(ctx context.Context) {
	h.t.Helper()
	if h.container == nil {
		return
	}
	termCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	_ = h.container.Terminate(termCtx)
	h.container = nil
}

func (h *SensorWebhookHandle) Restart(ctx context.Context) {
	h.t.Helper()
	h.Stop(ctx)
	h.container, h.GRPCEndpoint, h.WebhookBaseURL = runSensorWebhookContainer(
		ctx, h.t, h.networkName, h.alias, h.rimskyEndpoint, h.stateDSN)
}

func runSensorWebhookContainer(
	ctx context.Context, t testing.TB, networkName, alias, rimskyEndpoint, stateDSN string,
) (testcontainers.Container, string, string) {
	t.Helper()
	if rimskyEndpoint == "" {
		rimskyEndpoint = "http://rimsky:8080"
	}
	env := map[string]string{
		"RIMSKY_SENSOR_WEBHOOK_PORT":      "9084",
		"RIMSKY_SENSOR_WEBHOOK_HTTP_PORT": "9184",
		"RIMSKY_ENDPOINT":                 rimskyEndpoint,
	}
	if stateDSN != "" {
		env["RIMSKY_SENSOR_WEBHOOK_STATE_DSN"] = stateDSN
	}
	c, err := runWithRetry(ctx, sensorWebhookImage,
		tcnet.WithNetworkName([]string{alias}, networkName),
		testcontainers.WithEnv(env),
		testcontainers.WithExposedPorts("9084/tcp", "9184/tcp"),
		testcontainers.WithWaitStrategy(
			wait.ForListeningPort("9184/tcp").WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("harness: start sensor-webhook: %v", err)
	}
	hostIP, err := c.Host(ctx)
	if err != nil {
		t.Fatalf("harness: sensor-webhook host: %v", err)
	}
	mapped, err := c.MappedPort(ctx, "9184")
	if err != nil {
		t.Fatalf("harness: sensor-webhook mapped http port: %v", err)
	}
	return c, fmt.Sprintf("%s:9084", alias), fmt.Sprintf("http://%s:%s", hostIP, mapped.Port())
}
