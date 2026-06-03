// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package harness

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcnet "github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
)

// StartExecutorStubOnNetwork builds (on first use) and starts the test-only
// stub executor on the given docker network with the given alias. The stub
// implements the Executor gRPC service and returns Success for every
// dispatch, so harness tests about stores, subscribers, and observability can
// complete the claim loop without standing up a real executor. The image is
// built from test/stubexecutor/ via testcontainers and kept for reuse —
// nothing is pulled from a registry. Returns the in-network endpoint
// ("<alias>:9300") that callers pass to BringUpRimsky's WithExecutor option.
// Cleanup is registered via t.Cleanup.
func StartExecutorStubOnNetwork(ctx context.Context, t testing.TB, networkName, alias string) (endpoint string) {
	t.Helper()
	return startExecutorStub(ctx, t, networkName, alias, false)
}

// StartErroringExecutorStubOnNetwork is the error-only variant of
// StartExecutorStubOnNetwork: it sets EXECUTOR_STUB_FORCE_ERROR=1 so the
// stub emits a single terminal Error (error_class=stub/forced_error) for
// every dispatch instead of Success. The Gate-10 held-subgraph e2e uses
// it to drive the held co-holder set to aggregate-failure so auto-terminal
// fires Abandon (drop staging) on the real filesystem producer. Same image
// as the success stub — the env var alone selects the outcome.
func StartErroringExecutorStubOnNetwork(ctx context.Context, t testing.TB, networkName, alias string) (endpoint string) {
	t.Helper()
	return startExecutorStub(ctx, t, networkName, alias, true)
}

// startExecutorStub builds (on first use) and starts the test-only stub
// executor on the given docker network with the given alias. When
// forceError is true the stub emits Error for every dispatch; otherwise
// Success. Returns the in-network endpoint ("<alias>:9300").
func startExecutorStub(ctx context.Context, t testing.TB, networkName, alias string, forceError bool) (endpoint string) {
	t.Helper()
	env := map[string]string{
		"EXECUTOR_STUB_BIND": "0.0.0.0:9300",
	}
	if forceError {
		env["EXECUTOR_STUB_FORCE_ERROR"] = "1"
	}
	c, err := testcontainers.Run(ctx, "",
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
	if err != nil {
		t.Fatalf("harness: start executor-stub: %v", err)
	}
	t.Cleanup(func() {
		termCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = c.Terminate(termCtx)
	})
	return alias + ":9300"
}

// repoRoot returns the rimsky-core repo root (the directory containing
// go.work), derived from this file's own location
// (lib/services/test/harness/executor_stub.go) so it is independent of the
// test's working directory. The Docker build context for the stub executor
// is the repo root because the build copies in lib/protocols + lib/services
// via go.work — see lib/services/test/stubexecutor/Dockerfile.stubexecutor.
func repoRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "..", ".."))
}
