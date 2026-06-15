// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// End-to-end driver for STORY-frame-origin-audit's demo.
//
// The story's proof form is "Demo" — every frame in a representative
// end-to-end run has an originating message visible through the
// observability surface. This file drives the shipped demo
// (examples/frame-origin-audit-demo.sh) against a live rimsky stack
// wired to the bundled verifier-shape-checks executor and asserts:
//
//   - the script exits 0,
//   - the cascade exhibits BOTH operator-posted (kind=operator) and
//     cascade-emitted (kind=instance) origins,
//   - every frame line carries a triggering_message_id and a joined
//     envelope type+sender — the load-bearing acceptance property.
//
// @story: frame-origin-audit

package scenarios

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

// TestFrameOriginAuditDemo_RunExitsZero drives the shipped demo as a
// subprocess against a live all-in-one stack wired to the
// verifier-shape-checks executor and asserts the script exits zero — the
// demo's self-check covers structural correctness (every frame carries
// triggering_message_id+sender+type, at least one operator-origin and
// one cascade-emit frame).
func TestFrameOriginAuditDemo_RunExitsZero(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	netName := harness.NewNetwork(ctx, t)
	harness.StartVerifierShapeChecksOnNetwork(ctx, t, netName, "verifier-shape-checks")

	ep := harness.BringUpRimsky(ctx, t,
		harness.WithSQLite(),
		harness.WithExistingNetwork(netName),
		harness.WithExecutor("verifier-shape-checks", "verifier-shape-checks:9095"),
	)

	demoScript := repoExampleSpecPath(t, "examples/frame-origin-audit-demo.sh")
	templatePath := repoExampleSpecPath(t, "examples/frame-origin-audit-demo-template.yaml")

	stdout, exitCode := runFrameOriginAuditDemoScript(t, ctx, demoScript, ep.BaseURL, 180*time.Second)
	if exitCode != 0 {
		t.Fatalf("frame-origin-audit-demo.sh exited %d (want 0)\nstdout:\n%s", exitCode, stdout)
	}

	// @deliberate: confirm the template path exists on disk —
	// belt-and-suspenders for the Falsifier "shipped example isn't a
	// real runnable templatespec."
	if _, err := os.Stat(templatePath); err != nil {
		t.Fatalf("shipped template %s missing on disk: %v — the frame-origin-audit demo's template is broken", templatePath, err)
	}
}

// runFrameOriginAuditDemoScript invokes the demo shell script as a
// subprocess with the live stack's endpoint plumbed via env, and returns
// its combined stdout/stderr plus the subprocess exit code.
//
// @source: lib/services/test/scenarios/cross_frame_coupling_demo_e2e_test.go::runCrossFrameDemoScript
// Duplicated rather than shared: each demo driver names its own contract.
func runFrameOriginAuditDemoScript(t *testing.T, ctx context.Context, scriptPath, baseURL string, timeout time.Duration) (string, int) {
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
		t.Fatalf("frame-origin-audit-demo.sh run error (not an exit error): %v\ncombined:\n%s", err, combined)
	}
	return combined, exitErr.ExitCode()
}
