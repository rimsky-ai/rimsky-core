// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scenarios

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

var onboardingInstanceIDLine = regexp.MustCompile(`(?m)^instance_id=([0-9a-fA-F-]{36})\s*$`)

func TestOnboardingDemo_RunSettlesIdle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	netName := harness.NewNetwork(ctx, t)
	verifierEP := harness.StartVerifierShapeChecksOnNetwork(ctx, t, netName, "verifier-shape-checks")

	ep := harness.BringUpRimsky(ctx, t,
		harness.WithSQLite(),
		harness.WithExistingNetwork(netName),
		harness.WithExecutor("verifier-shape-checks", verifierEP),
	)

	binPath := filepath.Join(t.TempDir(), "rimsky")
	buildOnboardingRimskyCLI(t, binPath)

	demoScript := repoExampleSpecPath(t, "examples/onboarding-demo.sh")
	templatePath := repoExampleSpecPath(t, "examples/onboarding-template.yaml")

	stdout, exitCode := runDemoScript(t, ctx, demoScript, binPath, ep.BaseURL, 180*time.Second)
	if exitCode != 0 {
		t.Fatalf("onboarding-demo.sh exited %d (want 0)\nstdout:\n%s", exitCode, stdout)
	}
	match := onboardingInstanceIDLine.FindStringSubmatch(stdout)
	if match == nil {
		t.Fatalf("onboarding-demo.sh did not print `instance_id=<uuid>`; stdout:\n%s", stdout)
	}
	instanceID := match[1]

	requireOnboardingInstanceIdle(t, ep, instanceID, 60*time.Second)

	requireOnboardingNodeDispatched(t, ep, instanceID, "verifier", 60*time.Second)

	stdout2, exitCode2 := runDemoScript(t, ctx, demoScript, binPath, ep.BaseURL, 180*time.Second)
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

	if _, err := os.Stat(templatePath); err != nil {
		t.Fatalf("shipped template %s missing on disk: %v — the README's first-steps walkthrough is broken", templatePath, err)
	}
}

func runDemoScript(t *testing.T, ctx context.Context, scriptPath, binPath, baseURL string, timeout time.Duration) (string, int) {
	t.Helper()
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "/bin/bash", scriptPath)
	cmd.Env = append(os.Environ(),
		"RIMSKY_BIN="+binPath,
		"RIMSKY_ENDPOINT="+baseURL,
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

func buildOnboardingRimskyCLI(t *testing.T, binPath string) {
	t.Helper()
	cmd := exec.Command("go", "build", "-o", binPath, "./cmd/rimsky")
	cmd.Dir = repoRootForOnboarding(t)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go build ./cmd/rimsky: %v\nstderr:\n%s", err, stderr.String())
	}
	if _, err := os.Stat(binPath); err != nil {
		t.Fatalf("built rimsky binary missing at %s: %v", binPath, err)
	}
}

func repoRootForOnboarding(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed to resolve this test file's path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "..", ".."))
}

func requireOnboardingInstanceIdle(t *testing.T, ep harness.RimskyEndpoint, instanceID string, deadline time.Duration) {
	t.Helper()
	end := time.Now().Add(deadline)
	var lastFrames, lastMessages string
	for time.Now().Before(end) {
		framesStatus, framesRaw := ep.GetJSON(t, "/v1/instances/"+instanceID+"/frames?state=running", "")
		msgsStatus, msgsRaw := ep.GetJSON(t, "/v1/instances/"+instanceID+"/messages?pending=true", "")
		if framesStatus == http.StatusOK && msgsStatus == http.StatusOK {
			var frames struct {
				Frames []json.RawMessage `json:"frames"`
			}
			var msgs struct {
				Messages []json.RawMessage `json:"messages"`
			}
			lastFrames, lastMessages = string(framesRaw), string(msgsRaw)
			if json.Unmarshal(framesRaw, &frames) == nil && json.Unmarshal(msgsRaw, &msgs) == nil &&
				len(frames.Frames) == 0 && len(msgs.Messages) == 0 {
				return
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("instance %s did not settle idle (no running frame, no pending message) within %v — the run never resolved\nlast frames body:\n%s\nlast messages body:\n%s",
		instanceID, deadline, lastFrames, lastMessages)
}

func requireOnboardingNodeDispatched(t *testing.T, ep harness.RimskyEndpoint, instanceID, nodeType string, deadline time.Duration) {
	t.Helper()
	end := time.Now().Add(deadline)
	var lastBody string
	for time.Now().Before(end) {
		status, raw := ep.GetJSON(t,
			"/v1/observability/nodes/"+instanceID+"/"+nodeType, "")
		if status == http.StatusOK {
			var resp struct {
				Events []struct {
					Kind string `json:"kind"`
				} `json:"events"`
			}
			lastBody = string(raw)
			if err := json.Unmarshal(raw, &resp); err == nil {
				for _, e := range resp.Events {
					if e.Kind == "work_started" {
						return
					}
				}
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("node %q on instance %s never emitted `work_started` within %v — the verifier-shape-checks executor was never dispatched against\nlast GET /v1/observability/nodes/%s/%s body:\n%s",
		nodeType, instanceID, deadline, instanceID, nodeType, lastBody)
}
