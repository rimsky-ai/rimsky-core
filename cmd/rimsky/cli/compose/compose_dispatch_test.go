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

func TestComposeRunHelpPrintsItsOwnUsageOnStdoutAndSucceeds(t *testing.T) {
	for _, flagSpelling := range []string{"--help", "-h"} {
		stdout, code := captureStdout(t, func() int {
			return compose.Dispatch(context.Background(), []string{"run", flagSpelling})
		})
		if code != 0 {
			t.Errorf("compose run %s: exit %d, want 0", flagSpelling, code)
		}
		if !strings.Contains(stdout, "usage: rimsky compose run") {
			t.Errorf("compose run %s: stdout %q, want the verb's own usage line", flagSpelling, stdout)
		}
	}
}

func captureStdout(t *testing.T, fn func() int) (string, int) {
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
