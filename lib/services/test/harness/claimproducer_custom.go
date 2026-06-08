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

// StartOverlapClaimProducerOnNetwork builds (on first use) and starts the
// test-only overlap claim-producer on the given docker network with the
// given alias, returning the in-network endpoint a rimsky peer dials via
// harness.WithClaimProducer.
//
// The overlap producer advertises a NON-TRIVIAL ScopesConflict predicate
// (prefix-containment) plus SplitScope, so two writers whose scopes
// overlap but are NOT byte-equal can be driven through a real rimsky
// stack — the S-claimproducer-scopesconflict-wired scenario.
//
// It runs as a CONTAINER on the shared network (not in-process) on
// purpose: rimsky eager-dials its declared producers for a Capabilities
// handshake at startup and EXITS NON-ZERO if any is unreachable. A
// container on a stable in-network alias is up before rimsky boots, so
// the handshake reaches it deterministically — an in-process producer
// exposed via WithHostPortAccess races the reverse-SSH host-port tunnel
// against rimsky's startup dial and flakes under load. So the producer
// MUST be started on the network BEFORE BringUpRimsky (same ordering the
// executor stub and filesystem store require).
//
// The image is built from test/overlapproducer/ via testcontainers and
// kept for reuse — nothing is pulled from a registry. Cleanup is
// registered via t.Cleanup. Fails hard (t.Fatal) on a build/start
// failure — the harness never t.Skip's.
func StartOverlapClaimProducerOnNetwork(ctx context.Context, t testing.TB, networkName, alias string) (endpoint string) {
	t.Helper()
	c, err := testcontainers.Run(ctx, "",
		testcontainers.WithDockerfile(testcontainers.FromDockerfile{
			Context:    repoRoot(),
			Dockerfile: "lib/services/test/overlapproducer/Dockerfile.overlapproducer",
			Repo:       "rimsky-test/overlapproducer",
			Tag:        "latest",
			KeepImage:  true,
		}),
		tcnet.WithNetworkName([]string{alias}, networkName),
		testcontainers.WithEnv(map[string]string{
			"OVERLAP_PRODUCER_BIND": "0.0.0.0:9400",
		}),
		testcontainers.WithExposedPorts("9400/tcp"),
		testcontainers.WithWaitStrategy(
			wait.ForListeningPort("9400/tcp").WithStartupTimeout(120*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("harness: start overlap claim-producer: %v", err)
	}
	t.Cleanup(func() {
		termCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = c.Terminate(termCtx)
	})
	return "grpc://" + alias + ":9400"
}
