// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package harness

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcnet "github.com/testcontainers/testcontainers-go/network"
)

const (
	EnvInStackBaseURL   = "RIMSKY_TEST_STACK_BASE_URL"
	InStackExecutorName = "stub"
	TestRunnerImage     = "rimsky-test/test-runner"
)

func AcquireInStackEndpoint(ctx context.Context, t testing.TB) RimskyEndpoint {
	t.Helper()
	base := os.Getenv(EnvInStackBaseURL)
	if base == "" {
		return BootInStackProfile(ctx, t)
	}
	if err := waitForHealth(ctx, base); err != nil {
		t.Fatalf("harness: in-stack rimsky at %s (from %s) not healthy: %v", base, EnvInStackBaseURL, err)
	}
	return RimskyEndpoint{BaseURL: base, InternalURL: base}
}

func BootInStackProfile(ctx context.Context, t testing.TB) RimskyEndpoint {
	t.Helper()
	netName := SharedNetworkName(ctx, t)
	stubEndpoint := StartExecutorStubOnNetwork(ctx, t, netName)
	return BringUpRimsky(ctx, t,
		WithSQLite(),
		WithExistingNetwork(netName),
		WithExecutor(InStackExecutorName, stubEndpoint),
	)
}

func RunInStackSuites(ctx context.Context, t testing.TB, ep RimskyEndpoint, suites ...string) {
	t.Helper()
	if len(suites) == 0 {
		t.Fatalf("harness: RunInStackSuites: no suites given")
	}
	if ep.Network == "" {
		t.Fatalf("harness: RunInStackSuites: endpoint has no network — boot the stack with the harness first")
	}
	alias := fmt.Sprintf("test-runner-%d", nextAliasSuffix())
	runner, err := runWithRetry(ctx, ImageRef(TestRunnerImage),
		tcnet.WithNetworkName([]string{alias}, ep.Network),
		testcontainers.WithEnv(map[string]string{EnvInStackBaseURL: ep.InternalURL}),
		testcontainers.WithCmd(suites...),
	)
	if err != nil {
		t.Fatalf("harness: start test-runner: %v", err)
	}
	t.Cleanup(func() {
		termCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = runner.Terminate(termCtx)
	})

	exitCode := waitForContainerExit(ctx, t, runner)
	dumpLogsForFailure(t, "test-runner", runner)
	if exitCode != 0 {
		t.Fatalf("harness: test-runner exited %d for suites %s", exitCode, strings.Join(suites, ", "))
	}
}

func waitForContainerExit(ctx context.Context, t testing.TB, c testcontainers.Container) int {
	t.Helper()
	for {
		state, err := c.State(ctx)
		if err != nil {
			t.Fatalf("harness: test-runner state: %v", err)
		}
		switch state.Status {
		case "exited":
			return state.ExitCode
		case "dead":
			t.Fatalf("harness: test-runner container is dead (exit code %d)", state.ExitCode)
		}
		time.Sleep(500 * time.Millisecond)
	}
}
