// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package harness

import (
	"context"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcnet "github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
)

// verifierShapeChecksImage is the locally-built bundled verifier-shape-checks
// executor image, produced by `make service-images`. The harness consumes the
// local tag rather than pulling from a registry — operators run
// `make core-images && make service-images` before driving these scenarios.
const verifierShapeChecksImage = "rimsky-executor-verifier-shape-checks:latest"

// StartVerifierShapeChecksOnNetwork brings up the production
// rimsky-executor-verifier-shape-checks image on the given docker network
// with the given alias, returning the in-network endpoint a rimsky peer
// dials via harness.WithExecutor. The executor implements the real
// Executor gRPC protocol (no stub mode) — every dispatch runs the
// configured shape checks against the rows payload and returns Success
// or Error per the aggregate outcome.
//
// rimsky eager-dials its declared executors for a Capabilities handshake
// at startup, so this helper MUST be invoked BEFORE BringUpRimsky on the
// same network. Cleanup is registered via t.Cleanup. Fails hard
// (t.Fatal) when the image is missing — the harness never t.Skip's.
func StartVerifierShapeChecksOnNetwork(ctx context.Context, t testing.TB, networkName, alias string) (endpoint string) {
	t.Helper()

	// @deliberate: pin the binary's default 9095 (RIMSKY_EXECUTOR_VERIFIER_SHAPE_CHECKS_PORT)
	// rather than override it, so the in-network `<alias>:9095` endpoint we return
	// matches the binary's own startup log line when an operator debugs by reading
	// container stdout.
	c, err := testcontainers.Run(ctx, verifierShapeChecksImage,
		tcnet.WithNetworkName([]string{alias}, networkName),
		testcontainers.WithEnv(map[string]string{
			"RIMSKY_EXECUTOR_VERIFIER_SHAPE_CHECKS_HOST": "0.0.0.0",
			"RIMSKY_EXECUTOR_VERIFIER_SHAPE_CHECKS_PORT": "9095",
		}),
		testcontainers.WithExposedPorts("9095/tcp"),
		testcontainers.WithWaitStrategy(
			wait.ForListeningPort("9095/tcp").WithStartupTimeout(120*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("harness: start verifier-shape-checks: %v", err)
	}
	t.Cleanup(func() {
		termCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = c.Terminate(termCtx)
	})
	return alias + ":9095"
}
