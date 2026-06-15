// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// compose_dispatch_test.go — asserts the `compose <verb>` dispatcher
// exists and routes. `cmd/rimsky/main.go`'s top-level `compose` case
// (CLICTRL-5.4) is a one-line delegation to compose.Dispatch; the
// acceptance gate exercises the real binary path, while these unit tests
// pin Dispatch's routing contract (unknown subcommand / no args → exit 2).
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

// TestDispatch_RoutesRunToRunComposeRun confirms `run` reaches
// RunComposeRun rather than falling through to the unknown-subcommand
// path. The signal is stderr content: the fallthrough writes
// "unknown subcommand"; RunComposeRun's flag parser writes its own
// usage prefix. We capture stderr and assert the fallthrough line is
// absent. This survives pass-5 wiring of the real flow because the
// dispatcher's fallthrough message is the only thing that uniquely
// identifies the wrong route.
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

// captureStderr redirects os.Stderr for the duration of fn and returns
// the captured bytes. The redirect is restored on exit. Uses a pipe so
// writes do not block on a full kernel buffer.
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
