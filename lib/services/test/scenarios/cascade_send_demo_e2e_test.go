// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @story: cascade-send

package scenarios

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

func TestCascadeSendDemo_RunExitsZero(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	netName := harness.NewNetwork(ctx, t)
	verifierEP := harness.StartVerifierShapeChecksOnNetwork(ctx, t, netName, "verifier-shape-checks")

	ep := harness.BringUpRimsky(ctx, t,
		harness.WithSQLite(),
		harness.WithExistingNetwork(netName),
		harness.WithExecutor("verifier-shape-checks", verifierEP),
	)

	demoScript := repoExampleSpecPath(t, "examples/cascade-send-demo.sh")
	templatePath := repoExampleSpecPath(t, "examples/cascade-send-demo-template.yaml")

	stdout, exitCode := runCascadeSendDemoScript(t, ctx, demoScript, ep.BaseURL, 180*time.Second)
	if exitCode != 0 {
		t.Fatalf("cascade-send-demo.sh exited %d (want 0)\nstdout:\n%s", exitCode, stdout)
	}

	if _, err := os.Stat(templatePath); err != nil {
		t.Fatalf("shipped template %s missing on disk: %v — the cascade-send demo's template is broken", templatePath, err)
	}

	requireCascadeSendInstanceSentIterate(t, ep, 60*time.Second)
}

func runCascadeSendDemoScript(t *testing.T, ctx context.Context, scriptPath, baseURL string, timeout time.Duration) (string, int) {
	t.Helper()
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "/bin/bash", scriptPath)
	cmd.Env = append(os.Environ(),
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
		t.Fatalf("cascade-send-demo.sh run error (not an exit error): %v\ncombined:\n%s", err, combined)
	}
	return combined, exitErr.ExitCode()
}

func requireCascadeSendInstanceSentIterate(t *testing.T, ep harness.RimskyEndpoint, deadline time.Duration) {
	t.Helper()
	end := time.Now().Add(deadline)
	var lastBody string
	for time.Now().Before(end) {
		status, raw := ep.GetJSON(t, "/v1/instances?limit=20", "")
		if status != http.StatusOK {
			time.Sleep(250 * time.Millisecond)
			continue
		}
		var resp struct {
			Instances []struct {
				ID string `json:"id"`
			} `json:"instances"`
		}
		lastBody = string(raw)
		if err := json.Unmarshal(raw, &resp); err != nil {
			time.Sleep(250 * time.Millisecond)
			continue
		}
		for _, inst := range resp.Instances {
			fstatus, fraw := ep.GetJSON(t, "/v1/instances/"+inst.ID+"/frames?limit=20", "")
			if fstatus != http.StatusOK {
				continue
			}
			var fresp struct {
				Frames []struct {
					MessageType       string `json:"message_type"`
					MessageSenderKind string `json:"message_sender_kind"`
				} `json:"frames"`
			}
			if err := json.Unmarshal(fraw, &fresp); err != nil {
				continue
			}
			for _, fr := range fresp.Frames {
				if fr.MessageType == "loop/iterate" && fr.MessageSenderKind == "instance" {
					return
				}
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("no instance exhibited a loop/iterate frame with sender_kind=instance within %v — the cascade-send back-edge did not fire end-to-end\nlast /v1/instances body:\n%s", deadline, lastBody)
}
