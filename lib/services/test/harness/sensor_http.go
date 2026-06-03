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

// sensorHTTPImage is the locally-built production HTTP-poll sensor
// image. Built by `make service-images`.
const sensorHTTPImage = "rimsky-sensor-http:latest"

// StartSensorHTTP brings up the production rimsky-sensor-http image on
// the given docker network with the given alias, returning the
// in-network endpoint a rimsky peer dials via harness.WithPublisher.
//
// Unlike StartFilesystemStore the sensor takes no config file — it is
// pure-env: RIMSKY_SENSOR_HTTP_PORT (the gRPC Publisher server port) and
// RIMSKY_ENDPOINT (where it POSTs messages). RIMSKY_ENDPOINT is set to
// rimsky's stable in-network alias `http://rimsky:8080`, which is known
// before rimsky comes up — so the sensor MUST be started on the network
// BEFORE BringUpRimsky (rimsky eager-dials its declared publishers for a
// Capabilities handshake at startup).
//
// The watched URL is per-subscription (resolved from the publisher
// spec's resolved_config.url at instance-create), NOT env — so this
// helper does not take a watched URL. Because the sensor must dial that
// URL from inside its own container, callers that point a subscription
// at a host-side httptest.Server pass the server's host port(s) in
// hostAccessPorts: each is exposed to the container via
// testcontainers.WithHostPortAccess, reachable inside the container at
// `host.testcontainers.internal:<port>`.
//
// Cleanup is registered via t.Cleanup. Fails hard (t.Fatal) when the
// image is missing — the harness never t.Skip's.
func StartSensorHTTP(ctx context.Context, t testing.TB, networkName, alias string, hostAccessPorts ...int) (endpoint string) {
	t.Helper()

	opts := []testcontainers.ContainerCustomizer{
		tcnet.WithNetworkName([]string{alias}, networkName),
		testcontainers.WithEnv(map[string]string{
			"RIMSKY_SENSOR_HTTP_PORT": "9082",
			// rimsky's stable in-network alias; known before rimsky is up.
			"RIMSKY_ENDPOINT": "http://rimsky:8080",
		}),
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
	t.Cleanup(func() {
		termCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = c.Terminate(termCtx)
	})

	// The publisher block renders `endpoint: %q` verbatim into rimsky.yml
	// (writePeerBlocks); the executor/claim-producer peers use a bare
	// `<alias>:<port>` or `grpc://<alias>:<port>`. The sensor registers a
	// plaintext gRPC Publisher server, so a bare host:port matches the
	// other gRPC peers' shape.
	return fmt.Sprintf("%s:9082", alias)
}
