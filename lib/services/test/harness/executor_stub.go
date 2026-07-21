// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package harness

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcnet "github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	stubExecutorImage  = "rimsky-test/stubexecutor"
	sharedStubAlias    = "executor-stub"
	sharedStubErrAlias = "executor-stub-erroring"
)

var (
	stubMu          sync.Mutex
	stubLaunched    = map[string]error{}
	stubErrMu       sync.Mutex
	stubErrLaunched = map[string]error{}
)

func StartExecutorStubOnNetwork(ctx context.Context, t testing.TB, networkName string) (endpoint string) {
	t.Helper()
	stubMu.Lock()
	err, seen := stubLaunched[networkName]
	if !seen {
		err = launchExecutorStub(ctx, networkName, sharedStubAlias, false)
		stubLaunched[networkName] = err
	}
	stubMu.Unlock()
	if err != nil {
		t.Fatalf("harness: start executor-stub: %v", err)
	}
	return sharedStubAlias + ":9300"
}

func StartErroringExecutorStubOnNetwork(ctx context.Context, t testing.TB, networkName string) (endpoint string) {
	t.Helper()
	stubErrMu.Lock()
	err, seen := stubErrLaunched[networkName]
	if !seen {
		err = launchExecutorStub(ctx, networkName, sharedStubErrAlias, true)
		stubErrLaunched[networkName] = err
	}
	stubErrMu.Unlock()
	if err != nil {
		t.Fatalf("harness: start erroring executor-stub: %v", err)
	}
	return sharedStubErrAlias + ":9300"
}

func launchExecutorStub(ctx context.Context, networkName, alias string, forceError bool) error {
	env := map[string]string{
		"EXECUTOR_STUB_BIND": "0.0.0.0:9300",
	}
	if forceError {
		env["EXECUTOR_STUB_FORCE_ERROR"] = "1"
	}
	_, err := runWithRetry(ctx, ImageRef(stubExecutorImage),
		tcnet.WithNetworkName([]string{alias}, networkName),
		testcontainers.WithEnv(env),
		testcontainers.WithExposedPorts("9300/tcp"),
		testcontainers.WithWaitStrategy(
			wait.ForListeningPort("9300/tcp").WithStartupTimeout(120*time.Second),
		),
	)
	return err
}
