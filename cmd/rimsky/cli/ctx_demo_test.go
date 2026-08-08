// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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

	"github.com/rimsky-ai/rimsky-core/test/support/imagetag"
)

const rimskyAllInOneImage = "rimsky-all-in-one"

const ctxDemoHealthDeadline = 90 * time.Second

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

func TestCtxDemo(t *testing.T) {
	if testing.Short() {
		t.Skip("ctx-demo requires docker; skipped under -short")
	}
	ctx := context.Background()

	stagingURL := bringUpRimskyAllInOne(ctx, t, "ctx-demo-staging")
	prodURL := bringUpRimskyAllInOne(ctx, t, "ctx-demo-prod")

	if stagingURL == prodURL {
		t.Fatalf("the two rimsky-all-in-one stacks resolved to the same URL %q — the test cannot exhibit the switch", stagingURL)
	}

	binDir := t.TempDir()
	binPath := filepath.Join(binDir, "rimsky")
	buildRimskyCLI(t, binPath)

	scriptPath := repoFile(t, "test", "fixtures", "demos", "client-context-demo.sh")

	homeDir := t.TempDir()

	pathEnv := binDir + string(os.PathListSeparator) + os.Getenv("PATH")

	cmd := exec.CommandContext(ctx, "bash", scriptPath)
	cmd.Env = append(os.Environ(),
		"HOME="+homeDir,
		"PATH="+pathEnv,
		"STAGING_URL="+stagingURL,
		"PROD_URL="+prodURL,
		"RIMSKY_CONTEXT=",
		"RIMSKY_CONTROL_API_URL=",
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

func bringUpRimskyAllInOne(ctx context.Context, t *testing.T, alias string) string {
	t.Helper()

	img := imagetag.Ref(rimskyAllInOneImage)
	c, err := testcontainers.Run(ctx, img,
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
		if imagetag.IsMissingLocalImage(img, err) {
			err = imagetag.MissingImageError(img, err)
		}
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

func buildRimskyCLI(t *testing.T, binPath string) {
	t.Helper()
	repoRoot := repoRoot(t)

	args := []string{"build", "-o", binPath, "./cmd/rimsky/"}
	cmd := exec.Command("go", args...)
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build ./cmd/rimsky/ failed: %v\n%s", err, string(out))
	}
	if _, err := os.Stat(binPath); err != nil {
		t.Fatalf("built binary %q missing: %v", binPath, err)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", "..", ".."))
}

func repoFile(t *testing.T, parts ...string) string {
	t.Helper()
	parts = append([]string{repoRoot(t)}, parts...)
	p := filepath.Join(parts...)
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("required file %q missing: %v", p, err)
	}
	return p
}

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
