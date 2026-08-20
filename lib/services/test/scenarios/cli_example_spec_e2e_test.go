// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package scenarios

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"testing"

	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli"
	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli/compose"
	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

var instanceIDLine = regexp.MustCompile(`(?m)^instance_id=([0-9a-fA-F-]{36})\s*$`)

func TestCLIExampleSpec_RunReachesTerminal(t *testing.T) {
	ctx := context.Background()

	netName := harness.SharedNetworkName(ctx, t)
	harness.StartExecutorStubOnNetwork(ctx, t, netName)

	ep := harness.BringUpRimsky(ctx, t,
		harness.WithSQLite(),
		harness.WithExistingNetwork(netName),
		harness.WithExecutor("stub", "executor-stub:9300"),
	)

	specPath := repoExampleSpecPath(t, "test/fixtures/compose/template-a.yml")

	// @story: operator-onboarding
	stdout, code := captureRunRun(t, ctx, []string{"--endpoint", ep.BaseURL, specPath})
	if code != 0 {
		t.Fatalf("rimsky run %s exited %d (want 0)\nstdout:\n%s", specPath, code, stdout)
	}
	match := instanceIDLine.FindStringSubmatch(stdout)
	if match == nil {
		t.Fatalf("rimsky run did not print `instance_id=<uuid>`; stdout:\n%s", stdout)
	}
	instanceID := match[1]

	// @story: operator-onboarding
	if _, getCode := captureRun(t, func() int {
		return cli.RunInstanceGet(ctx, []string{"--endpoint", ep.BaseURL, instanceID})
	}); getCode != 0 {
		t.Fatalf("rimsky instance get %s exited %d (want 0)", instanceID, getCode)
	}

	waitForDispatchToFresh(t, ep, instanceID, "worker")

	_, rerunCode := captureRunRun(t, ctx, []string{"--endpoint", ep.BaseURL, specPath})
	if rerunCode != 0 {
		t.Fatalf("second `rimsky run %s` exited %d (want 0)", specPath, rerunCode)
	}
}

func captureRunRun(t *testing.T, ctx context.Context, args []string) (string, int) {
	t.Helper()
	return captureRun(t, func() int { return compose.RunTemplateRun(ctx, args) })
}

var stdoutCaptureMu sync.Mutex

func captureRun(t *testing.T, fn func() int) (string, int) {
	t.Helper()
	stdoutCaptureMu.Lock()
	defer stdoutCaptureMu.Unlock()

	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w

	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()

	code := func() int {
		defer func() {
			os.Stdout = orig
			_ = w.Close()
		}()
		return fn()
	}()

	out := <-done
	_ = r.Close()
	return out, code
}

func repoExampleSpecPath(t *testing.T, rel string) string {
	t.Helper()
	path := filepath.Join(repoRoot(t), filepath.FromSlash(rel))
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("test fixture %s not found at %s: %v", rel, path, err)
	}
	return path
}
