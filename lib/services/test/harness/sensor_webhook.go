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

// sensorWebhookImage is the locally-built production webhook sensor
// image. Built by `make service-images`.
const sensorWebhookImage = "rimsky-sensor-webhook:latest"

// SensorWebhookHandle exposes the bring-up handles a cross-stack test
// needs: GRPCEndpoint is the in-network endpoint a rimsky peer dials
// (via harness.WithPublisher) for the Publisher gRPC protocol; the
// WebhookBaseURL is the HOST-side mapped URL the test can POST inbound
// webhooks to (the sensor's inbound HTTP listener). Both must be
// surfaced — the gRPC port is private to the docker network and not
// reachable from the host process, and the inbound HTTP port is what
// makes the STORY-sensor-webhook acceptance gate observable end to end.
type SensorWebhookHandle struct {
	GRPCEndpoint   string
	WebhookBaseURL string
}

// StartSensorWebhook brings up the production rimsky-sensor-webhook
// image on the given docker network with the given alias, returning a
// handle carrying (a) the in-network gRPC endpoint a rimsky peer dials
// and (b) the host-mapped inbound-webhook base URL the test posts to.
//
// The sensor is pure-env: RIMSKY_SENSOR_WEBHOOK_PORT (gRPC server,
// 9084) and RIMSKY_SENSOR_WEBHOOK_HTTP_PORT (inbound webhook listener,
// 9184) and RIMSKY_ENDPOINT (where it POSTs persisted messages). The
// gRPC endpoint uses the in-network alias `host:port` shape the other
// gRPC peers also use (executor_stub / claim_producer / sensor-http).
//
// The webhook HTTP port (9184) is published to the host so the test
// process can drive real inbound POSTs against the live sensor; the
// gRPC port stays in-network only (rimsky dials it from inside the
// docker network at the alias).
//
// rimsky's eager-Dial of declared publishers at startup means this
// helper MUST be called BEFORE BringUpRimsky.
//
// Cleanup is registered via t.Cleanup. Fails hard (t.Fatal) when the
// image is missing — the harness never t.Skip's.
func StartSensorWebhook(ctx context.Context, t testing.TB, networkName, alias string) *SensorWebhookHandle {
	t.Helper()
	env := map[string]string{
		"RIMSKY_SENSOR_WEBHOOK_PORT":      "9084",
		"RIMSKY_SENSOR_WEBHOOK_HTTP_PORT": "9184",
		// @constraint: rimsky's stable in-network alias; known before rimsky is up.
		"RIMSKY_ENDPOINT": "http://rimsky:8080",
	}
	opts := []testcontainers.ContainerCustomizer{
		tcnet.WithNetworkName([]string{alias}, networkName),
		testcontainers.WithEnv(env),
		// @constraint: both ports exposed — 9084 for in-network gRPC (rimsky's
		// Subscribe handshake), 9184 for inbound webhook (host-side test POSTs
		// via the mapped port).
		testcontainers.WithExposedPorts("9084/tcp", "9184/tcp"),
		testcontainers.WithWaitStrategy(
			// @deliberate: wait on the inbound-webhook port — `/health` is on
			// the chi router there, and the gRPC server starts right after on
			// the same goroutine boundary, so listening on 9184 is a sufficient
			// readiness signal for both ports.
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
		// @constraint: bare host:port (no scheme) matches the executor /
		// claim-producer / sensor-http peers' YAML rendering — the value is
		// passed to BringUpRimsky via WithPublisher.
		GRPCEndpoint: fmt.Sprintf("%s:9084", alias),
		// @constraint: docker assigns a random high port for 9184; the test
		// process reaches the sensor's inbound HTTP listener only via Host()
		// + MappedPort(), never via the in-network alias.
		WebhookBaseURL: fmt.Sprintf("http://%s:%s", hostIP, mapped.Port()),
	}
}
