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

const overlapProducerImage = "rimsky-test/overlapproducer:latest"

func StartOverlapClaimProducerOnNetwork(ctx context.Context, t testing.TB, networkName, alias string) (endpoint string) {
	t.Helper()
	uniqueAlias := fmt.Sprintf("%s-%d", alias, nextAliasSuffix())
	c, err := runWithRetry(ctx, overlapProducerImage,
		tcnet.WithNetworkName([]string{uniqueAlias}, networkName),
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
	return "grpc://" + uniqueAlias + ":9400"
}
