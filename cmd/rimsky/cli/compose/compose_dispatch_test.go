// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package compose_test

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli/compose"
)

func TestComposeDispatch_UnknownSubcommand(t *testing.T) {
	if got := compose.Dispatch(context.Background(), []string{"bogus"}); got != 2 {
		t.Errorf("Dispatch(bogus) = %d, want 2", got)
	}
}

func TestComposeDispatch_NoArgs(t *testing.T) {
	if got := compose.Dispatch(context.Background(), nil); got != 2 {
		t.Errorf("Dispatch(nil) = %d, want 2", got)
	}
}

func TestDispatch_RoutesRunToRunComposeRun(t *testing.T) {
	stderr, _ := captureStderr(t, func() int {
		return compose.Dispatch(context.Background(), []string{"run", "--help"})
	})
	if strings.Contains(stderr, "unknown subcommand") {
		t.Errorf("Dispatch routed `run` to the unknown-subcommand path; stderr=%q", stderr)
	}
}

func TestDispatch_RunHelpFlag(t *testing.T) {
	if got := compose.Dispatch(context.Background(), []string{"run", "--help"}); got != 2 {
		t.Errorf("Dispatch(run, --help) = %d, want 2", got)
	}
}

func captureStderr(t *testing.T, fn func() int) (string, int) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	saved := os.Stderr
	os.Stderr = w
	done := make(chan []byte)
	go func() {
		buf, _ := io.ReadAll(r)
		done <- buf
	}()
	code := fn()
	_ = w.Close()
	os.Stderr = saved
	buf := <-done
	_ = r.Close()
	return string(buf), code
}
