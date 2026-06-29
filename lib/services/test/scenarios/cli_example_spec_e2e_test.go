// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scenarios

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli"
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

	waitForExampleDispatchToFresh(t, ep, instanceID, "worker", 90*time.Second)

	readmeArgs := documentedRunInvocation(t, ep.BaseURL, specPath)
	_, readmeCode := captureRunRun(t, ctx, readmeArgs)
	if readmeCode != 0 {
		t.Fatalf("README-documented `rimsky run` invocation %v exited %d (want 0)", readmeArgs, readmeCode)
	}
}

func documentedRunInvocation(t *testing.T, baseURL, specPath string) []string {
	t.Helper()
	return []string{
		"--endpoint", baseURL,
		"--instance-key", "example-readme-run",
		specPath,
	}
}

func captureRunRun(t *testing.T, ctx context.Context, args []string) (string, int) {
	t.Helper()
	return captureRun(t, func() int { return cli.RunRun(ctx, args) })
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

func waitForExampleDispatchToFresh(t *testing.T, ep harness.RimskyEndpoint, instanceID, nodeType string, deadline time.Duration) {
	t.Helper()
	end := time.Now().Add(deadline)
	var (
		lastFresh   int
		lastFailed  int
		sawDispatch bool
	)
	for time.Now().Before(end) {
		status, raw := ep.GetJSON(t,
			"/v1/observability/nodes/"+instanceID+"/"+nodeType, "")
		if status == http.StatusOK {
			var resp struct {
				RunSummary struct {
					FreshCount  int `json:"fresh_count"`
					FailedCount int `json:"failed_count"`
				} `json:"run_summary"`
				Events []struct {
					Kind string `json:"kind"`
				} `json:"events"`
			}
			if err := json.Unmarshal(raw, &resp); err == nil {
				lastFresh = resp.RunSummary.FreshCount
				lastFailed = resp.RunSummary.FailedCount
				for _, e := range resp.Events {
					if e.Kind == "work_started" {
						sawDispatch = true
						break
					}
				}
				if sawDispatch && (lastFresh > 0 || lastFailed > 0) {
					if lastFailed > 0 {
						t.Fatalf("node %q dispatched via `rimsky run` but settled with failed_count=%d, want all-fresh — the stub executor returns Success, so any failed run is a real dev-loop defect",
							nodeType, lastFailed)
					}
					return
				}
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("node %q on instance %s (driven by `rimsky run`) did not complete a real dispatch within %v; run_summary.fresh_count=%d failed_count=%d, work_started seen=%v",
		nodeType, instanceID, deadline, lastFresh, lastFailed, sawDispatch)
}
