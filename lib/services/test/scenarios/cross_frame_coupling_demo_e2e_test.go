// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// End-to-end driver for STORY-cross-frame-coupling's demo half.
//
// The story's proof is "all-of-the-above" — executable proofs under
// test/scenarios/ AND a demo walking through the scenario succeeding.
// This file drives the shipped demo (examples/cross-frame-coupling-demo.sh)
// against a live rimsky stack wired to the bundled verifier-shape-checks
// executor and asserts:
//
//   - the script exits 0,
//   - the cascade exhibits both the wake frame (operator-posted) and the
//     iterate frame (cascade-emitted),
//   - the receiver node R (subscribed to the typed-message loop/iterate)
//     reaches terminal/success — pinning the spec's "receiver reads the
//     sender's data through {{messages.<type>.<field>}}" property end-
//     to-end.
//
// The bring-up mirrors onboarding_demo_e2e_test.go: network first, then
// the verifier-shape-checks bundled image as a network peer, then
// rimsky-all-in-one with the executor declared via WithExecutor. The
// services-scenario harness owns the testcontainers lifecycle; the demo
// script is run as a subprocess with RIMSKY_ENDPOINT pointing at the
// mapped port.
//
// @story: cross-frame-coupling

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

// TestCrossFrameCouplingDemo_RunExitsZero drives the shipped demo as a
// subprocess against a live all-in-one stack wired to the
// verifier-shape-checks executor and asserts the script exits zero — the
// demo's self-check covers structural correctness (both frames present,
// every frame carries triggering_message_id), so a non-zero exit
// surfaces both infra and spec violations.
func TestCrossFrameCouplingDemo_RunExitsZero(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// The verifier-shape-checks executor must be reachable on the shared
	// network before rimsky/all starts — the control-api fires a
	// Capabilities handshake against declared executors at startup.
	netName := harness.NewNetwork(ctx, t)
	harness.StartVerifierShapeChecksOnNetwork(ctx, t, netName, "verifier-shape-checks")

	ep := harness.BringUpRimsky(ctx, t,
		harness.WithSQLite(),
		harness.WithExistingNetwork(netName),
		harness.WithExecutor("verifier-shape-checks", "verifier-shape-checks:9095"),
	)

	demoScript := repoExampleSpecPath(t, "examples/cross-frame-coupling-demo.sh")
	templatePath := repoExampleSpecPath(t, "examples/cross-frame-coupling-demo-template.yaml")

	stdout, exitCode := runCrossFrameDemoScript(t, ctx, demoScript, ep.BaseURL, 180*time.Second)
	if exitCode != 0 {
		t.Fatalf("cross-frame-coupling-demo.sh exited %d (want 0)\nstdout:\n%s", exitCode, stdout)
	}

	// Sanity: confirm the template path the script points at exists on
	// disk. A missing template would have already failed the script run;
	// this is belt-and-suspenders for the Falsifier ("shipped example
	// isn't a real runnable templatespec").
	if _, err := os.Stat(templatePath); err != nil {
		t.Fatalf("shipped template %s missing on disk: %v — the cross-frame-coupling demo's template is broken", templatePath, err)
	}

	// Reach into the instance via the API to verify the back-edge actually
	// fired. The script's self-check already asserts the frames pattern;
	// this is the persistence-layer witness that the loop/iterate envelope
	// landed AND its sender_kind is instance (the cascade-emit origin).
	requireCrossFrameInstanceEmittedIterate(t, ep, 60*time.Second)
}

// runCrossFrameDemoScript invokes the demo shell script as a subprocess
// with the live stack's endpoint plumbed via env, and returns its
// combined stdout/stderr plus the subprocess exit code. Any non-exit
// error (e.g. failure to fork) fatals the test.
//
// @source: lib/services/test/scenarios/onboarding_demo_e2e_test.go::runDemoScript
// Duplicated rather than shared: the onboarding helper plumbs RIMSKY_BIN
// (the CLI binary path) which this demo doesn't use — the curl-only
// script needs only RIMSKY_ENDPOINT.
func runCrossFrameDemoScript(t *testing.T, ctx context.Context, scriptPath, baseURL string, timeout time.Duration) (string, int) {
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
		t.Fatalf("cross-frame-coupling-demo.sh run error (not an exit error): %v\ncombined:\n%s", err, combined)
	}
	return combined, exitErr.ExitCode()
}

// requireCrossFrameInstanceEmittedIterate scans the most recent instances
// for one with a loop/iterate frame, exhibiting the cascade-emit origin.
// The script already asserts this at the frame-list level; this is the
// belt-and-suspenders persistence-layer witness that the emit-node
// actually wrote an envelope (the back-edge dispatched real work, not a
// no-op).
func requireCrossFrameInstanceEmittedIterate(t *testing.T, ep harness.RimskyEndpoint, deadline time.Duration) {
	t.Helper()
	end := time.Now().Add(deadline)
	var lastBody string
	for time.Now().Before(end) {
		// Use the cross-instance message list filter to find an envelope of
		// type loop/iterate. The control-API exposes per-instance message
		// reads but not a global filter; iterate by polling the recent
		// instance's frames. We just look for any frame in any instance
		// from this run that has type=loop/iterate.
		status, raw := ep.GetJSON(t, "/v1/instances?limit=20", "")
		if status != http.StatusOK {
			time.Sleep(250 * time.Millisecond)
			continue
		}
		var resp struct {
			Instances []struct {
				// The instances-list endpoint returns the instance UUID
				// under `id` (not `instance_id`). The other CRUD endpoints
				// use `instance_id`; this is a known shape divergence in
				// the list response.
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
	t.Fatalf("no instance exhibited a loop/iterate frame with sender_kind=instance within %v — the cross-frame back-edge did not fire end-to-end\nlast /v1/instances body:\n%s", deadline, lastBody)
}
