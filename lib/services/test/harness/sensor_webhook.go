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
}

func StartSensorWebhook(ctx context.Context, t testing.TB, networkName, alias, rimskyEndpoint string) *SensorWebhookHandle {
	t.Helper()
	if rimskyEndpoint == "" {
		rimskyEndpoint = "http://rimsky:8080"
	}
	uniqueAlias := fmt.Sprintf("%s-%d", alias, nextAliasSuffix())
	env := map[string]string{
		"RIMSKY_SENSOR_WEBHOOK_PORT":      "9084",
		"RIMSKY_SENSOR_WEBHOOK_HTTP_PORT": "9184",
		"RIMSKY_ENDPOINT":                 rimskyEndpoint,
	}
	opts := []testcontainers.ContainerCustomizer{
		tcnet.WithNetworkName([]string{uniqueAlias}, networkName),
		testcontainers.WithEnv(env),
		testcontainers.WithExposedPorts("9084/tcp", "9184/tcp"),
		testcontainers.WithWaitStrategy(
			wait.ForListeningPort("9184/tcp").WithStartupTimeout(60 * time.Second),
		),
	}
	c, err := testcontainers.Run(ctx, sensorWebhookImage, opts...)
	if err != nil {
		t.Fatalf("harness: start sensor-webhook: %v", err)
	}
	t.Cleanup(func() {
		termCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = c.Terminate(termCtx)
	})

	hostIP, err := c.Host(ctx)
	if err != nil {
		t.Fatalf("harness: sensor-webhook host: %v", err)
	}
	mapped, err := c.MappedPort(ctx, "9184")
	if err != nil {
		t.Fatalf("harness: sensor-webhook mapped http port: %v", err)
	}
	return &SensorWebhookHandle{
		GRPCEndpoint:   fmt.Sprintf("%s:9084", uniqueAlias),
		WebhookBaseURL: fmt.Sprintf("http://%s:%s", hostIP, mapped.Port()),
	}
}
