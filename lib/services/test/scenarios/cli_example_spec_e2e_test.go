// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scenarios

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli"
	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli/compose"
	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

var instanceIDLine = regexp.MustCompile(`(?m)^instance_id=([0-9a-fA-F-]{36})\s*$`)

func TestCLIExampleSpec_RunReachesTerminal(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	netName := harness.NewNetwork(ctx, t)
	harness.StartExecutorStubOnNetwork(ctx, t, netName)

	ep := harness.BringUpRimsky(ctx, t,
		harness.WithSQLite(),
		harness.WithExistingNetwork(netName),
		harness.WithExecutor("stub", "executor-stub:9300"),
	)

	specPath := repoExampleSpecPath(t, "examples/compose/template-a.yml")

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

	waitForDispatchToFresh(t, ep, instanceID, "worker", 90*time.Second)

	readmeArgs := documentedRunInvocation(t, ep.BaseURL, "examples/compose/template-a.yml")
	_, readmeCode := captureRunRun(t, ctx, readmeArgs)
	if readmeCode != 0 {
		t.Fatalf("README-documented `rimsky run` invocation %v exited %d (want 0)", readmeArgs, readmeCode)
	}
}

var readmeRunInvocationRe = regexp.MustCompile(`(?m)^rimsky run (\S+)\s*$`)

func documentedRunInvocation(t *testing.T, baseURL, specRelPath string) []string {
	t.Helper()
	raw, err := os.ReadFile(repoExampleSpecPath(t, "examples/README.md"))
	if err != nil {
		t.Fatalf("read examples/README.md: %v", err)
	}
	m := readmeRunInvocationRe.FindStringSubmatch(string(raw))
	if m == nil {
		t.Fatalf("examples/README.md has no `rimsky run <file>` invocation line to prove")
	}
	documentedRelPath := filepath.FromSlash(m[1])
	if documentedRelPath != filepath.FromSlash(specRelPath) {
		t.Fatalf("examples/README.md documents `rimsky run %s`, but the test drives %s — keep the README and the proof in sync", m[1], specRelPath)
	}
	return []string{"--endpoint", baseURL, repoExampleSpecPath(t, specRelPath)}
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
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed to locate the test source file")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", "..", "..", ".."))
	path := filepath.Join(repoRoot, filepath.FromSlash(rel))
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("shipped example spec %s not found at %s: %v — the example must ship as a real on-disk file", rel, path, err)
	}
	return path
}
