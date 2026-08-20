// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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
	"github.com/rimsky-ai/rimsky-core/test/support/awaited"
)

func TestCascadeSendDemo_RunExitsZero(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	netName := harness.SharedNetworkName(ctx, t)
	verifierEP := harness.StartVerifierShapeChecksOnNetwork(ctx, t, netName, "verifier-shape-checks")

	ep := harness.BringUpRimsky(ctx, t,
		harness.WithSQLite(),
		harness.WithExistingNetwork(netName),
		harness.WithExecutor("verifier-shape-checks", verifierEP),
	)

	demoScript := repoExampleSpecPath(t, "test/fixtures/demos/cascade-send-demo.sh")
	repoExampleSpecPath(t, "test/fixtures/demos/cascade-send-demo-template.yaml")

	stdout, exitCode := runCascadeSendDemoScript(t, ctx, demoScript, ep.BaseURL)
	if exitCode != 0 {
		t.Fatalf("cascade-send-demo.sh exited %d (want 0)\nstdout:\n%s", exitCode, stdout)
	}

	requireCascadeSendInstanceSentIterate(t, ep)
}

func runCascadeSendDemoScript(t *testing.T, ctx context.Context, scriptPath, baseURL string) (string, int) {
	t.Helper()
	cmd := exec.CommandContext(ctx, "/bin/bash", scriptPath)
	cmd.Env = append(os.Environ(),
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
		t.Fatalf("cascade-send-demo.sh run error (not an exit error): %v\ncombined:\n%s", err, combined)
	}
	return combined, exitErr.ExitCode()
}

func requireCascadeSendInstanceSentIterate(t *testing.T, ep harness.RimskyEndpoint) {
	t.Helper()
	awaited.Until(t, "an instance to exhibit a loop/iterate frame with sender_kind=instance, the end-to-end "+
		"evidence that the cascade-send back-edge fired", func() bool {
		status, raw := ep.GetJSON(t, "/v1/instances?limit=20", "")
		if status != http.StatusOK {
			return false
		}
		var resp struct {
			Instances []struct {
				ID string `json:"id"`
			} `json:"instances"`
		}
		if err := json.Unmarshal(raw, &resp); err != nil {
			return false
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
					return true
				}
			}
		}
		return false
	})
}
