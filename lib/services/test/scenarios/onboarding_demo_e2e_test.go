// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package scenarios

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
	"github.com/rimsky-ai/rimsky-core/test/support/awaited"
)

var onboardingInstanceIDLine = regexp.MustCompile(`(?m)^instance_id=([0-9a-fA-F-]{36})\s*$`)

func TestOnboardingDemo_RunSettlesIdle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	netName := harness.SharedNetworkName(ctx, t)
	verifierEP := harness.StartVerifierShapeChecksOnNetwork(ctx, t, netName, "verifier-shape-checks")

	ep := harness.BringUpRimsky(ctx, t,
		harness.WithSQLite(),
		harness.WithExistingNetwork(netName),
		harness.WithExecutor("verifier-shape-checks", verifierEP),
	)

	binPath := filepath.Join(t.TempDir(), "rimsky")
	buildRimskyCLI(t, binPath)

	demoScript := repoExampleSpecPath(t, "test/fixtures/demos/onboarding-demo.sh")
	repoExampleSpecPath(t, "test/fixtures/demos/onboarding-template.yaml")

	stdout, exitCode := runDemoScript(t, ctx, demoScript, binPath, ep.BaseURL)
	if exitCode != 0 {
		t.Fatalf("onboarding-demo.sh exited %d (want 0)\nstdout:\n%s", exitCode, stdout)
	}
	match := onboardingInstanceIDLine.FindStringSubmatch(stdout)
	if match == nil {
		t.Fatalf("onboarding-demo.sh did not print `instance_id=<uuid>`; stdout:\n%s", stdout)
	}
	instanceID := match[1]

	requireInstanceIdle(t, ep, instanceID)

	requireOnboardingNodeDispatched(t, ep, instanceID, "verifier")

	stdout2, exitCode2 := runDemoScript(t, ctx, demoScript, binPath, ep.BaseURL)
	if exitCode2 != 0 {
		t.Fatalf("onboarding-demo.sh (second run) exited %d (want 0)\nstdout:\n%s", exitCode2, stdout2)
	}
	match2 := onboardingInstanceIDLine.FindStringSubmatch(stdout2)
	if match2 == nil {
		t.Fatalf("onboarding-demo.sh (second run) did not print `instance_id=<uuid>`; stdout:\n%s", stdout2)
	}
	if match2[1] == instanceID {
		t.Fatalf("second run produced the same instance_id %q — the script's per-run instance_key did not actually disambiguate", instanceID)
	}
}

func runDemoScript(t *testing.T, ctx context.Context, scriptPath, binPath, baseURL string) (string, int) {
	t.Helper()
	cmd := exec.CommandContext(ctx, "/bin/bash", scriptPath)
	cmd.Env = append(os.Environ(),
		"RIMSKY_BIN="+binPath,
		"RIMSKY_CONTROL_API_URL="+baseURL,
	)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	cmd.WaitDelay = 5 * time.Second
	err := cmd.Run()
	combined := out.String()
	if errStr := errBuf.String(); errStr != "" {
		combined += "\n[stderr]\n" + errStr
	}
	if err == nil {
		return combined, 0
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("onboarding-demo.sh run error (not an exit error): %v\ncombined:\n%s", err, combined)
	}
	return combined, exitErr.ExitCode()
}

func buildRimskyCLI(t *testing.T, binPath string) {
	t.Helper()
	cmd := exec.Command("go", "build", "-o", binPath, "./cmd/rimsky")
	cmd.Dir = repoRoot(t)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go build ./cmd/rimsky: %v\nstderr:\n%s", err, stderr.String())
	}
	if _, err := os.Stat(binPath); err != nil {
		t.Fatalf("built rimsky binary missing at %s: %v", binPath, err)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed to resolve this test file's path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "..", ".."))
}

func requireInstanceIdle(t *testing.T, ep harness.RimskyEndpoint, instanceID string) {
	t.Helper()
	awaited.Until(t, fmt.Sprintf("instance %s to settle idle — no running frame, no pending message", instanceID), func() bool {
		framesStatus, framesRaw := ep.GetJSON(t, "/v1/instances/"+instanceID+"/frames?state=running", "")
		msgsStatus, msgsRaw := ep.GetJSON(t, "/v1/instances/"+instanceID+"/messages?pending=true", "")
		if framesStatus != http.StatusOK || msgsStatus != http.StatusOK {
			return false
		}
		var frames struct {
			Frames []json.RawMessage `json:"frames"`
		}
		var msgs struct {
			Messages []json.RawMessage `json:"messages"`
		}
		return json.Unmarshal(framesRaw, &frames) == nil && json.Unmarshal(msgsRaw, &msgs) == nil &&
			len(frames.Frames) == 0 && len(msgs.Messages) == 0
	})
}

func requireOnboardingNodeDispatched(t *testing.T, ep harness.RimskyEndpoint, instanceID, nodeType string) {
	t.Helper()
	awaited.Until(t, fmt.Sprintf("node %q on instance %s to emit work_started, the evidence that the "+
		"verifier-shape-checks executor was dispatched against", nodeType, instanceID), func() bool {
		status, raw := ep.GetJSON(t, "/v1/observability/nodes/"+instanceID+"/"+nodeType, "")
		if status != http.StatusOK {
			return false
		}
		var resp struct {
			Events []struct {
				Kind string `json:"kind"`
			} `json:"events"`
		}
		if err := json.Unmarshal(raw, &resp); err != nil {
			return false
		}
		for _, e := range resp.Events {
			if e.Kind == "work_started" {
				return true
			}
		}
		return false
	})
}
