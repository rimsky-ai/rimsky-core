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

func StartHttpNodeStubOnNetwork(ctx context.Context, t testing.TB, networkName, alias string) (endpoint string) {
	t.Helper()
	uniqueAlias := fmt.Sprintf("%s-%d", alias, nextAliasSuffix())
	env := map[string]string{
		"RIMSKY_EXECUTOR_HTTP_NODE_PORT":      "9091",
		"RIMSKY_EXECUTOR_HTTP_NODE_HTTP_PORT": "9092",
		"RIMSKY_EXECUTOR_STUB_MODE":           "1",
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
