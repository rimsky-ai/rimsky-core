// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package harness

import (
	"context"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcnet "github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	sharedStubAlias    = "executor-stub"
	sharedStubErrAlias = "executor-stub-erroring"
)

var (
	stubBuildMu sync.Mutex

	stubOnce    sync.Once
	stubErr     error
	stubErrOnce sync.Once
	stubErrErr  error
)

func StartExecutorStubOnNetwork(ctx context.Context, t testing.TB, networkName string) (endpoint string) {
	t.Helper()
	stubOnce.Do(func() {
		stubErr = launchExecutorStub(ctx, networkName, sharedStubAlias, false)
	})
	if stubErr != nil {
		t.Fatalf("harness: start executor-stub: %v", stubErr)
	}
	return sharedStubAlias + ":9300"
}

func StartErroringExecutorStubOnNetwork(ctx context.Context, t testing.TB, networkName string) (endpoint string) {
	t.Helper()
	stubErrOnce.Do(func() {
		stubErrErr = launchExecutorStub(ctx, networkName, sharedStubErrAlias, true)
	})
	if stubErrErr != nil {
		t.Fatalf("harness: start erroring executor-stub: %v", stubErrErr)
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
	stubBuildMu.Lock()
	defer stubBuildMu.Unlock()
	_, err := testcontainers.Run(ctx, "",
		testcontainers.WithDockerfile(testcontainers.FromDockerfile{
			Context:    repoRoot(),
			Dockerfile: "lib/services/test/stubexecutor/Dockerfile.stubexecutor",
			Repo:       "rimsky-test/stubexecutor",
			Tag:        "latest",
			KeepImage:  true,
		}),
		tcnet.WithNetworkName([]string{alias}, networkName),
		testcontainers.WithEnv(env),
		testcontainers.WithExposedPorts("9300/tcp"),
		testcontainers.WithWaitStrategy(
			wait.ForListeningPort("9300/tcp").WithStartupTimeout(120*time.Second),
		),
	)
	return err
}

func repoRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "..", ".."))
}
