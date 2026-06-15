// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

//go:build smoke

// Package all is the unified-image smoke test. Builds Dockerfile.rimsky,
// runs the container, polls /health, asserts the SQLite startup banner
// appears in the container logs, and verifies clean shutdown. Per
// spec §9.6.
//
// Gated by `//go:build smoke`: run via `go test -tags=smoke
// ./test/smoke/all/...`. Skips automatically when docker is unavailable.
package all

import (
	"fmt"
	"net/http"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestUnifiedImage(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker unavailable")
	}

	// @deliberate: Resolve repo root from this test's source location so the build
	// works regardless of the test runner's CWD.
	_, thisFile, _, _ := runtime.Caller(0)
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", "..", ".."))

	buildCmd := exec.Command("docker", "build",
		"-f", filepath.Join(repoRoot, "Dockerfile.rimsky"),
		"-t", "rimsky:smoke", repoRoot)
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("docker build: %v\n%s", err, out)
	}

	runID := "rimsky-smoke-" + strings.ReplaceAll(time.Now().Format("150405.000"), ".", "")
	volID := runID + "-state"
	t.Cleanup(func() {
		_ = exec.Command("docker", "rm", "-f", runID).Run()
		_ = exec.Command("docker", "volume", "rm", volID).Run()
	})

	runCmd := exec.Command("docker", "run", "--rm", "-d", "--name", runID,
		"-p", "0:8080", "-v", volID+":/var/lib/rimsky",
		"rimsky-all:smoke")
	if out, err := runCmd.CombinedOutput(); err != nil {
		t.Fatalf("docker run: %v\n%s", err, out)
	}

	portCmd := exec.Command("docker", "port", runID, "8080/tcp")
	portOut, err := portCmd.Output()
	if err != nil {
		t.Fatalf("docker port: %v", err)
	}
	firstLine := strings.SplitN(strings.TrimSpace(string(portOut)), "\n", 2)[0]
	parts := strings.Split(firstLine, ":")
	port := parts[len(parts)-1]

	url := fmt.Sprintf("http://localhost:%s/health", port)
	deadline := time.Now().Add(30 * time.Second)
	for {
		resp, err := http.Get(url)
		if err == nil && resp.StatusCode == 200 {
			resp.Body.Close()
			break
		}
		if resp != nil {
			resp.Body.Close()
		}
		if time.Now().After(deadline) {
			logsOut, _ := exec.Command("docker", "logs", runID).CombinedOutput()
			t.Fatalf("/health did not return 200 within deadline\nlast err: %v\nlogs:\n%s", err, logsOut)
		}
		time.Sleep(500 * time.Millisecond)
	}

	logsOut, err := exec.Command("docker", "logs", runID).CombinedOutput()
	if err != nil {
		t.Fatalf("docker logs: %v", err)
	}
	if !strings.Contains(string(logsOut), "SQLite driver is for local development only") {
		t.Fatalf("startup banner missing from container logs:\n%s", logsOut)
	}

	if out, err := exec.Command("docker", "stop", runID).CombinedOutput(); err != nil {
		t.Fatalf("docker stop: %v\n%s", err, out)
	}
}
