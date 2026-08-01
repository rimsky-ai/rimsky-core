// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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

const verifierShapeChecksImage = "rimsky-executor-verifier-shape-checks"

func StartVerifierShapeChecksOnNetwork(ctx context.Context, t testing.TB, networkName, alias string) (endpoint string) {
	t.Helper()
	uniqueAlias := fmt.Sprintf("%s-%d", alias, nextAliasSuffix())

	c, err := runWithRetry(ctx, ImageRef(verifierShapeChecksImage),
		tcnet.WithNetworkName([]string{uniqueAlias}, networkName),
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
	return uniqueAlias + ":9095"
}
