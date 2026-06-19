// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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

func TestFrameOriginAuditDemo_RunExitsZero(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	netName := harness.NewNetwork(ctx, t)
	verifierEP := harness.StartVerifierShapeChecksOnNetwork(ctx, t, netName, "verifier-shape-checks")

	ep := harness.BringUpRimsky(ctx, t,
		harness.WithSQLite(),
		harness.WithExistingNetwork(netName),
		harness.WithExecutor("verifier-shape-checks", verifierEP),
	)

	demoScript := repoExampleSpecPath(t, "examples/frame-origin-audit-demo.sh")
	templatePath := repoExampleSpecPath(t, "examples/frame-origin-audit-demo-template.yaml")

	stdout, exitCode := runFrameOriginAuditDemoScript(t, ctx, demoScript, ep.BaseURL, 180*time.Second)
	if exitCode != 0 {
		t.Fatalf("frame-origin-audit-demo.sh exited %d (want 0)\nstdout:\n%s", exitCode, stdout)
	}

	if _, err := os.Stat(templatePath); err != nil {
		t.Fatalf("shipped template %s missing on disk: %v — the frame-origin-audit demo's template is broken", templatePath, err)
	}
}

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
