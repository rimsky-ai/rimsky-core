// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// ctx_demo_test.go — driver test for STORY-client-context's demo proof.
//
// Boots TWO independent `rimsky-all-in-one:latest` containers via
// testcontainers-go (each on its own host-mapped port, running on its
// baked SQLite default — no Postgres or per-stack config needed), builds
// the real `rimsky` CLI binary, and runs `examples/client-context-demo.sh`
// as a subprocess against the two endpoints. Asserts the script exits 0
// and that every documented step marker appears in stdout in order.
//
// This drives the REAL assembled product end-to-end: the demo issues real
// `rimsky ctx` verbs that mutate a real on-disk config, then real
// `rimsky ls instances` verbs that resolve the active context's endpoint
// (via the same `ResolveEndpoint` precedence chain a developer hits) and
// hit a real control-api over real HTTP. A stubbed ctx layer (e.g. one
// that always returned the same endpoint, or silently dropped a
// remove) would be caught by either the `ls-instances-staging` /
// `ls-instances-prod` steps (the HTTP request would land on the wrong
// stack and the response shape wouldn't match what each backend
// serves) or by the `rm-staging-no-longer-resolves` step (the next
// `ctx use staging` would silently succeed).
//
// The two containers are configured-identical (same image, same baked
// SQLite default) but they are independent processes on independent
// host ports; the demo's switch behavior is observable by the fact that
// each `ls instances` call resolves to a DIFFERENT host port, and the
// CLI's endpoint resolution is the only thing routing it.
//
// Skip-on-no-docker is intentional: this test requires a working Docker
// socket. It is part of the integration tier and runs alongside
// `make test-all`.
package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// rimskyAllInOneImage is the locally-built all-in-one image produced by
// `make core-images`. The driver test consumes the local tag (it does
// not pull from a registry).
const rimskyAllInOneImage = "rimsky-all-in-one:latest"

// ctxDemoHealthDeadline bounds the wait for each rimsky/all stack to start
// serving `GET /v1/health` 200.
const ctxDemoHealthDeadline = 90 * time.Second

// expectedStepMarkers is the ordered set of `step: …` lines the demo
// script prints. The driver asserts they all appear in stdout in this
// order; missing or out-of-order markers fail the test, because each
// marker corresponds to a real CLI verb the demo must successfully
// drive.
var expectedStepMarkers = []string{
	"step: clean",
	"step: add-staging",
	"step: add-prod",
	"step: list-after-add",
	"step: use-staging",
	"step: ls-instances-staging",
	"step: health-endpoint-is-staging",
	"step: use-prod",
	"step: ls-instances-prod",
	"step: health-endpoint-is-prod",
	"step: current-is-prod",
	"step: rm-staging",
	"step: rm-staging-no-longer-resolves",
	"step: done",
}

// TestCtxDemo boots two rimsky-all-in-one stacks, builds the rimsky CLI,
// and runs examples/client-context-demo.sh against them.
func TestCtxDemo(t *testing.T) {
	if testing.Short() {
		t.Skip("ctx-demo requires docker; skipped under -short")
	}
	ctx := context.Background()

	stagingURL := bringUpRimskyAllInOne(ctx, t, "ctx-demo-staging")
	prodURL := bringUpRimskyAllInOne(ctx, t, "ctx-demo-prod")

	// @deliberate: assert the two stacks resolved to distinct host ports.
	// If they collided, the demo's "switch is observable" claim would be
	// vacuously satisfied — both contexts would resolve to the same backend.
	if stagingURL == prodURL {
		t.Fatalf("the two rimsky-all-in-one stacks resolved to the same URL %q — the test cannot exhibit the switch", stagingURL)
	}

	binDir := t.TempDir()
	binPath := filepath.Join(binDir, "rimsky")
	buildRimskyCLI(t, binPath)

	scriptPath := repoFile(t, "examples", "client-context-demo.sh")

	// @deliberate: HOME points at a tempdir so the demo's `rimsky ctx add`
	// mutations write into a throwaway ~/.rimsky/config.yml — never the
	// developer's real one.
	homeDir := t.TempDir()

	// @deliberate: PATH is prepended with binDir so the demo's bare `rimsky`
	// invocation finds the freshly-built binary rather than a stale one on
	// the developer's PATH.
	pathEnv := binDir + string(os.PathListSeparator) + os.Getenv("PATH")

	cmd := exec.CommandContext(ctx, "bash", scriptPath)
	cmd.Env = append(os.Environ(),
		"HOME="+homeDir,
		"PATH="+pathEnv,
		"STAGING_URL="+stagingURL,
		"PROD_URL="+prodURL,
		// @deliberate: clear any stray RIMSKY_CONTEXT / RIMSKY_CONTROL_API in
		// the test process's environment so they cannot override the demo's
		// active-context resolution chain — those would defeat the test's
		// purpose.
		"RIMSKY_CONTEXT=",
		"RIMSKY_CONTROL_API=",
	)
	stdout, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		stderr := ""
		if asExit, ok := err.(*exec.ExitError); ok {
			exitErr = asExit
			stderr = string(exitErr.Stderr)
		}
		t.Fatalf("demo script failed: %v\nstdout:\n%s\nstderr:\n%s", err, string(stdout), stderr)
	}

	assertStepMarkersInOrder(t, string(stdout), expectedStepMarkers)
}

// bringUpRimskyAllInOne starts a single rimsky-all-in-one container on
// its baked SQLite default, exposes port 8080 to the host, waits for
// /v1/health to return 200, and returns the host-mapped base URL.
// Registers t.Cleanup for the container.
func bringUpRimskyAllInOne(ctx context.Context, t *testing.T, alias string) string {
	t.Helper()

	c, err := testcontainers.Run(ctx, rimskyAllInOneImage,
		testcontainers.WithExposedPorts("8080/tcp"),
		testcontainers.WithEnv(map[string]string{
			"RIMSKY_CONTROL_API_HOST": "0.0.0.0",
			"RIMSKY_CONTROL_API_PORT": "8080",
		}),
		testcontainers.WithWaitStrategy(
			wait.ForListeningPort("8080/tcp").WithStartupTimeout(120*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("[%s] start rimsky-all-in-one: %v", alias, err)
	}
	t.Cleanup(func() {
		termCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = c.Terminate(termCtx)
	})

	host, err := c.Host(ctx)
	if err != nil {
		t.Fatalf("[%s] container host: %v", alias, err)
	}
	port, err := c.MappedPort(ctx, "8080")
	if err != nil {
		t.Fatalf("[%s] container mapped port: %v", alias, err)
	}
	baseURL := fmt.Sprintf("http://%s:%s", host, port.Port())

	if err := waitForCtlAPIHealth(ctx, baseURL, ctxDemoHealthDeadline); err != nil {
		dumpRimskyLogs(t, alias, c)
		t.Fatalf("[%s] /v1/health did not return 200 within %v: %v", alias, ctxDemoHealthDeadline, err)
	}
	return baseURL
}

// waitForCtlAPIHealth polls baseURL+"/v1/health" until it returns 200
// or the deadline elapses.
func waitForCtlAPIHealth(ctx context.Context, baseURL string, deadline time.Duration) error {
	pollCtx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	const interval = 500 * time.Millisecond
	for {
		if pollCtx.Err() != nil {
			return fmt.Errorf("timed out after %v", deadline)
		}
		req, _ := http.NewRequestWithContext(pollCtx, http.MethodGet, baseURL+"/v1/health", nil)
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		select {
		case <-pollCtx.Done():
			return fmt.Errorf("timed out after %v", deadline)
		case <-time.After(interval):
		}
	}
}

// dumpRimskyLogs prints the container's stdout/stderr at test-fail time
// so a failing bring-up doesn't require manual `docker logs` to diagnose.
func dumpRimskyLogs(t *testing.T, alias string, c testcontainers.Container) {
	t.Helper()
	rc, err := c.Logs(context.Background())
	if err != nil {
		t.Logf("[%s] cannot read container logs: %v", alias, err)
		return
	}
	defer rc.Close()
	out, _ := io.ReadAll(rc)
	t.Logf("=== [%s] container logs ===\n%s\n=== end logs ===", alias, string(out))
}

// buildRimskyCLI compiles ./cmd/rimsky/ into a fresh binary at binPath.
// We build from source rather than relying on a pre-built bin/rimsky so
// the test always exercises the current tree's CLI behavior, not a
// possibly-stale checkout artifact.
func buildRimskyCLI(t *testing.T, binPath string) {
	t.Helper()
	repoRoot := repoRoot(t)

	args := []string{"build", "-o", binPath, "./cmd/rimsky/"}
	cmd := exec.Command("go", args...)
	cmd.Dir = repoRoot
	// @deliberate: inherit os.Environ so the developer's GOFLAGS / GOPROXY
	// are honored by the build.
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build ./cmd/rimsky/ failed: %v\n%s", err, string(out))
	}
	if _, err := os.Stat(binPath); err != nil {
		t.Fatalf("built binary %q missing: %v", binPath, err)
	}
}

// repoRoot returns the rimsky-core repo root, derived from this file's
// own location (cmd/rimsky/cli/ctx_demo_test.go → ../../..).
func repoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", "..", ".."))
}

// repoFile returns the absolute path to a file relative to the repo root,
// failing the test if it doesn't exist.
func repoFile(t *testing.T, parts ...string) string {
	t.Helper()
	parts = append([]string{repoRoot(t)}, parts...)
	p := filepath.Join(parts...)
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("required file %q missing: %v", p, err)
	}
	return p
}

// assertStepMarkersInOrder scans stdout line-by-line and asserts each
// marker in `want` appears, in order, somewhere in the output. Lines
// between markers are allowed (the demo prints CLI verb output between
// markers). A missing marker, or a marker appearing out of order, fails
// the test with a descriptive diagnostic.
func assertStepMarkersInOrder(t *testing.T, stdout string, want []string) {
	t.Helper()
	idx := 0
	scanner := bufio.NewScanner(strings.NewReader(stdout))
	for scanner.Scan() {
		line := scanner.Text()
		if idx < len(want) && strings.TrimSpace(line) == want[idx] {
			idx++
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan stdout: %v", err)
	}
	if idx != len(want) {
		t.Fatalf("missing or out-of-order step markers: matched %d of %d\nfirst-missing: %q\nstdout:\n%s",
			idx, len(want), want[idx], stdout)
	}
}
