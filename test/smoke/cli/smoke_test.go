// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

//go:build smoke

// Package cli_smoke runs the real `rimsky-cli` binary against the
// reference deploy/docker-compose.yml stack. Skipped unless Docker is
// available. Built with `go test -tags smoke`.
package cli_smoke

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func locateRepoRoot(t *testing.T) string {
	t.Helper()
	_, here, _, _ := runtime.Caller(0)
	// here = .../test/smoke/cli/smoke_test.go
	dir := filepath.Dir(here)
	for i := 0; i < 6; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("could not locate repo root")
	return ""
}

func locateCLI(t *testing.T, repoRoot string) string {
	t.Helper()
	bin := filepath.Join(repoRoot, "bin", "rimsky-cli")
	if _, err := os.Stat(bin); err != nil {
		t.Skipf("rimsky-cli binary missing (run `make cli` first): %v", err)
	}
	return bin
}

func dockerAvailable(t *testing.T) bool {
	t.Helper()
	cmd := exec.Command("docker", "info")
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run() == nil
}

func waitForControlAPI(t *testing.T, url string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return
			}
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("control-api %s did not become ready", url)
}

func TestCLISmoke(t *testing.T) {
	if !dockerAvailable(t) {
		t.Skip("docker not available")
	}
	repoRoot := locateRepoRoot(t)
	bin := locateCLI(t, repoRoot)

	tempDir := t.TempDir()
	// 1. Run init.
	if out, err := exec.Command(bin, "init", tempDir).CombinedOutput(); err != nil {
		t.Fatalf("init failed: %v\n%s", err, out)
	}

	// All CLI invocations target the local stack via RIMSKY_CONTROL_API.
	// The scaffolded manifest doesn't pin a context, and the smoke test
	// runs without ~/.rimsky/config.yml, so the env var is the only
	// path that resolves the endpoint.
	env := append(os.Environ(), "RIMSKY_CONTROL_API=http://localhost:8080")
	withEnv := func(c *exec.Cmd) *exec.Cmd { c.Env = env; return c }

	// 2. Run dev up. The compose stack runs `docker compose -f
	// deploy/docker-compose.yml up -d` against the embedded reference.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := withEnv(exec.CommandContext(ctx, bin, "dev", "up", "-f", filepath.Join(tempDir, "rimsky-compose.yml"), "--yes"))
	cmd.Dir = tempDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("dev up: %v", err)
	}
	t.Cleanup(func() {
		// Always tear down to keep CI clean. `compose down --infra` runs
		// `QueryState` first; if the test failed before the control-api
		// came up, that call errors out and the infra command never
		// fires. Fall back to a direct `docker compose down -v` so the
		// stack is reaped between smoke runs.
		downCmd := withEnv(exec.Command(bin, "compose", "down", "--infra", "--yes", "-f", filepath.Join(tempDir, "rimsky-compose.yml")))
		downCmd.Dir = tempDir
		if err := downCmd.Run(); err != nil {
			fallback := exec.Command("docker", "compose", "-f", "deploy/docker-compose.yml", "down", "-v")
			fallback.Dir = tempDir
			_ = fallback.Run()
		}
	})

	waitForControlAPI(t, "http://localhost:8080/health", 30*time.Second)

	// 3. Poll for the example instance.
	deadline := time.Now().Add(30 * time.Second)
	var seen bool
	for time.Now().Before(deadline) && !seen {
		out, err := withEnv(exec.Command(bin, "ls")).Output()
		if err == nil && strings.Contains(string(out), "compose:") {
			seen = true
			break
		}
		time.Sleep(time.Second)
	}
	if !seen {
		t.Fatalf("instance never appeared in `ls`")
	}

	// 4. compose status should run cleanly.
	statusCmd := withEnv(exec.Command(bin, "compose", "status", "-f", filepath.Join(tempDir, "rimsky-compose.yml")))
	if out, err := statusCmd.CombinedOutput(); err != nil {
		t.Fatalf("compose status: %v\n%s", err, out)
	}
}
