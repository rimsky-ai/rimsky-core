// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package compose_test

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli"
	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli/compose"
	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli/internal/clitest"
)

func captureComposeStdout(t *testing.T, fn func() int) (string, int) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	saved := os.Stdout
	os.Stdout = w
	done := make(chan []byte)
	go func() {
		buf, _ := io.ReadAll(r)
		done <- buf
	}()
	code := fn()
	_ = w.Close()
	os.Stdout = saved
	buf := <-done
	_ = r.Close()
	return string(buf), code
}

// @decision: auth-dry-run-request-flag
func TestRunComposeUp_ReportsADryRunPreviewAtExitZero(t *testing.T) {
	srv := setupServer(t)
	mf := writeFullManifest(t)
	cli.SetActiveCommonFlags(&cli.CommonFlags{Format: cli.FormatHuman})
	t.Cleanup(func() { cli.SetActiveCommonFlags(nil) })
	srv.SetFailure("POST", "/v1/templates", clitest.FailureSpec{
		Status: 200,
		Body: map[string]any{
			"dry_run": true,
			"would_have_registered": map[string]any{
				"template_hash": "sha256-preview",
			},
		},
	})

	out, code := captureComposeStdout(t, func() int {
		return compose.RunComposeUp(context.Background(), []string{"-f", mf, "--yes"})
	})

	if code != 0 {
		t.Fatalf("rimsky compose up exits %d on a dry-run preview, want 0: nothing was written, so nothing failed", code)
	}
	if !strings.Contains(out, "would have registered") {
		t.Fatalf("rimsky compose up stdout = %q, want the preview line", out)
	}
	if !strings.Contains(out, "sha256-preview") {
		t.Fatalf("rimsky compose up stdout = %q, want the preview details", out)
	}
}

// @decision: auth-dry-run-request-flag
func TestRunComposeUp_ReportsAGenuineFailureAtExitOne(t *testing.T) {
	srv := setupServer(t)
	mf := writeFullManifest(t)
	srv.SetFailure("POST", "/v1/templates", clitest.FailureSpec{
		Status: 403,
		Body:   map[string]any{"error": "forbidden"},
	})

	if code := compose.RunComposeUp(context.Background(), []string{"-f", mf, "--yes"}); code != 1 {
		t.Fatalf("rimsky compose up exits %d on a rejected write, want 1", code)
	}
}
