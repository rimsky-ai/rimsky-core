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
	c := runSensorHTTPContainer(ctx, t, networkName, alias, "", hostAccessPorts)
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

// SensorHTTPHandle is the restart-capable bring-up handle for a
// rimsky-sensor-http peer container. Endpoint is the in-network gRPC
// endpoint (the value to pass to BringUpRimsky via WithPublisher).
// Stop terminates the live container and Restart brings up a fresh one
// with IDENTICAL env, so the test can exercise the restart-recovery
// path: durable subscriptions + body-hash watermarks survive in the
// configured RIMSKY_SENSOR_HTTP_STATE_DSN, but the in-memory watches
// are dropped on terminate and rebuilt by the fresh container's
// AttachStateDB.
//
// The Postgres state DSN and the host-port-access list passed at
// construction time are preserved across Restart (the DSN points at a
// sibling Postgres container that is NOT torn down between sensor
// restarts; the host-port-access list keeps the host-side
// httptest.Server reachable from inside the post-restart sensor
// container at host.testcontainers.internal:<port>).
type SensorHTTPHandle struct {
	Endpoint string

	t               testing.TB
	networkName     string
	alias           string
	stateDSN        string
	hostAccessPorts []int

	container testcontainers.Container
}

// StartSensorHTTPHandle brings up the rimsky-sensor-http image on the
// given docker network with the given alias and a durable state DSN,
// returning a restart-capable handle. Pass stateDSN="" for the in-memory
// default (no durability proof possible). hostAccessPorts is forwarded
// verbatim to testcontainers.WithHostPortAccess on every bring-up
// (initial + restart) so a host-side httptest.Server stays reachable
// inside the fresh container at host.testcontainers.internal:<port>.
//
// Cleanup is registered via t.Cleanup; fails hard (t.Fatal) when the
// image is missing — the harness never t.Skip's.
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

// Stop terminates the live sensor-http container without bringing a
// fresh one up. Used by restart-recovery tests to drop the in-memory
// watches between Subscribe (persisted via state DSN) and the
// recovered post-restart poll.
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

// Restart brings up a fresh sensor-http container with IDENTICAL
// network / alias / state DSN / host-port-access, replacing any live
// container. The fresh container's AttachStateDB rebuilds in-memory
// watches from durable rows — recovering each subscription's
// ORIGINALLY-declared poll interval, body filter, and body-hash
// watermark rather than waiting for rimsky to re-Subscribe (rimsky's
// ResyncPublisherSubscriptions runs only at control-api startup, not on
// demand). The DSN points at a sibling Postgres container that survives
// the restart, so the durable state persists across the call.
func (h *SensorHTTPHandle) Restart(ctx context.Context) {
	h.t.Helper()
	h.Stop(ctx)
	h.container = runSensorHTTPContainer(ctx, h.t, h.networkName, h.alias, h.stateDSN, h.hostAccessPorts)
}

// runSensorHTTPContainer starts one rimsky-sensor-http container with
// the given env wiring. Shared by StartSensorHTTP, StartSensorHTTPHandle,
// and SensorHTTPHandle.Restart so the bring-up shape is identical on
// initial boot and after a restart.
func runSensorHTTPContainer(ctx context.Context, t testing.TB, networkName, alias, stateDSN string, hostAccessPorts []int) testcontainers.Container {
	t.Helper()
	env := map[string]string{
		"RIMSKY_SENSOR_HTTP_PORT": "9082",
		// rimsky's stable in-network alias; known before rimsky is up.
		"RIMSKY_ENDPOINT": "http://rimsky:8080",
	}
	if stateDSN != "" {
		// Durability gate. When set, sensor-http persists active
		// publisher-subscriptions + their body-hash watermarks to the
		// configured Postgres so a process restart resumes the durable
		// watch with its body filter + watermark intact. Empty → in-
		// memory default, which loses watches on restart (and breaks
		// the STORY-sensor-http durability acceptance).
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
