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

func StartHttpNodeStubOnNetwork(ctx context.Context, t testing.TB, networkName, alias string) (endpoint string) {
	t.Helper()
	uniqueAlias := fmt.Sprintf("%s-%d", alias, nextAliasSuffix())
	env := map[string]string{
		"RIMSKY_EXECUTOR_PORT_GRPC": "9091",
		"RIMSKY_EXECUTOR_PORT_HTTP": "9092",
		"RIMSKY_EXECUTOR_STUB_MODE": "1",
	}
	c, err := runWithRetry(ctx, ImageRef("rimsky-executor-http-node"),
		tcnet.WithNetworkName([]string{uniqueAlias}, networkName),
		testcontainers.WithEnv(env),
		testcontainers.WithExposedPorts("9091/tcp", "9092/tcp"),
		testcontainers.WithWaitStrategy(
			wait.ForListeningPort("9091/tcp").WithStartupTimeout(120*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("harness: start rimsky-executor-http-node: %v", err)
	}
	t.Cleanup(func() {
		termCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = c.Terminate(termCtx)
	})
	return uniqueAlias + ":9091"
}
