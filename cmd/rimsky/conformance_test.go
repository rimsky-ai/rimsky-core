// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// conformance_test.go — flag-surface tests for the `rimsky conformance
// <protocol>` subcommands. The seven handlers were folded in from the former
// standalone cmd/rimsky-*-conformance binaries; their flag sets are the
// implementer-facing contract, so these tests pin that each subcommand
// registers exactly its documented flags, rejects unknown ones, and maps a
// missing required input to the right exit code. They exercise only the
// parse/validate prefix of each handler — every case returns before any
// network dial — so no executor/endpoint is needed.
package main

import (
	"io"
	"os"
	"strings"
	"testing"
)

// captureStderr redirects os.Stderr to a pipe for the duration of fn and
// returns everything written. The conformance handlers write their flag
// usage (on -h) and their required-flag diagnostics to os.Stderr; capturing
// it both keeps test output clean and lets us assert on the content. Not
// safe for t.Parallel — it swaps a process-global.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stderr = w
	defer func() { os.Stderr = orig }()

	done := make(chan string, 1)
	go func() {
		data, _ := io.ReadAll(r)
		done <- string(data)
	}()

	fn()
	_ = w.Close()
	return <-done
}

// runCapt invokes a conformance handler with args, capturing stderr, and
// returns the handler's exit code plus what it wrote.
func runCapt(t *testing.T, fn func([]string) int, args []string) (int, string) {
	t.Helper()
	var code int
	out := captureStderr(t, func() { code = fn(args) })
	return code, out
}

// conformanceSubcommands mirrors the dispatch table in conformance.go. The
// flags slice is the exact set each handler's FlagSet must register; reqMsg is
// the diagnostic emitted when the required input is absent; reqExit is the
// exit code for that missing-input path (probe uses 1, the rest use 2 — a
// distinction carried over verbatim from the original standalone binaries).
var conformanceSubcommands = []struct {
	name    string
	run     func([]string) int
	flags   []string
	reqMsg  string
	reqExit int
}{
	{
		name:    "executor",
		run:     runConformanceExecutor,
		flags:   []string{"endpoint", "transport", "require-stub-mode", "scenarios", "skip", "timeout", "check-observability", "retention-test-seconds", "check-lifecycle", "callback-bind", "callback-host", "tls"},
		reqMsg:  "--endpoint required",
		reqExit: 2,
	},
	{
		name:    "claim-producer",
		run:     runConformanceClaimProducer,
		flags:   []string{"endpoint", "timeout", "check-observability", "retention-test-seconds"},
		reqMsg:  "--endpoint required",
		reqExit: 2,
	},
	{
		name:    "publisher",
		run:     runConformancePublisher,
		flags:   []string{"endpoint", "transport", "kind", "resolved-config", "timeout", "instance-id"},
		reqMsg:  "--endpoint required",
		reqExit: 2,
	},
	{
		name:    "validation",
		run:     runConformanceValidation,
		flags:   []string{"endpoint", "transport", "role", "timeout"},
		reqMsg:  "--endpoint required",
		reqExit: 2,
	},
	{
		name:    "data-processing",
		run:     runConformanceDataProcessing,
		flags:   []string{"endpoint", "transport", "timeout"},
		reqMsg:  "--endpoint required",
		reqExit: 2,
	},
	{
		name:    "blob-backend",
		run:     runConformanceBlobBackend,
		flags:   []string{"backend", "root", "pg-conn-string", "timeout"},
		reqMsg:  "--backend required",
		reqExit: 2,
	},
	{
		name:    "probe",
		run:     runConformanceProbe,
		flags:   []string{"endpoint", "transport", "timeout", "callback-bind", "callback-host"},
		reqMsg:  "--endpoint required",
		reqExit: 1,
	},
}

// TestConformanceSubcommandsRegisterDocumentedFlags asserts each handler's
// FlagSet declares exactly its documented flags — no more, no fewer. A dropped
// or renamed flag is a silent regression the build/lint can't catch, so we pin
// the surface here. `-h` makes the FlagSet print its full flag list (and
// nothing else) before the handler returns.
func TestConformanceSubcommandsRegisterDocumentedFlags(t *testing.T) {
	for _, sc := range conformanceSubcommands {
		t.Run(sc.name, func(t *testing.T) {
			_, usage := runCapt(t, sc.run, []string{"-h"})
			for _, f := range sc.flags {
				if !strings.Contains(usage, "-"+f) {
					t.Errorf("subcommand %q: documented flag --%s missing from usage:\n%s", sc.name, f, usage)
				}
			}
			// Guard against drift the other direction: the only flag tokens in
			// the usage should be the documented ones. flag's PrintDefaults
			// renders each flag on its own line beginning with "  -<name>".
			for _, line := range strings.Split(usage, "\n") {
				trimmed := strings.TrimSpace(line)
				if !strings.HasPrefix(trimmed, "-") {
					continue
				}
				name := strings.SplitN(strings.TrimPrefix(trimmed, "-"), " ", 2)[0]
				if name == "" {
					continue
				}
				if !containsString(sc.flags, name) {
					t.Errorf("subcommand %q: undocumented flag -%s present in usage (update the test table or the handler)", sc.name, name)
				}
			}
		})
	}
}

// TestConformanceSubcommandsRejectUnknownFlags asserts each handler uses
// ContinueOnError and surfaces an unknown flag as a non-zero exit, rather than
// silently ignoring it.
func TestConformanceSubcommandsRejectUnknownFlags(t *testing.T) {
	for _, sc := range conformanceSubcommands {
		t.Run(sc.name, func(t *testing.T) {
			code, out := runCapt(t, sc.run, []string{"--definitely-not-a-real-flag"})
			if code == 0 {
				t.Errorf("subcommand %q: unknown flag should produce a non-zero exit, got 0", sc.name)
			}
			if !strings.Contains(out, "not defined") {
				t.Errorf("subcommand %q: expected a 'flag provided but not defined' diagnostic, got:\n%s", sc.name, out)
			}
		})
	}
}

// TestConformanceSubcommandsRequireTheirInputs asserts that, with no args, each
// handler reports its missing required input and exits with the documented code
// — before attempting any network connection. This pins both the validation
// and the exit-code mapping (notably probe=1 vs the rest=2).
func TestConformanceSubcommandsRequireTheirInputs(t *testing.T) {
	for _, sc := range conformanceSubcommands {
		t.Run(sc.name, func(t *testing.T) {
			code, out := runCapt(t, sc.run, nil)
			if code != sc.reqExit {
				t.Errorf("subcommand %q: missing required input should exit %d, got %d", sc.name, sc.reqExit, code)
			}
			if !strings.Contains(out, sc.reqMsg) {
				t.Errorf("subcommand %q: expected %q in diagnostic, got:\n%s", sc.name, sc.reqMsg, out)
			}
		})
	}
}

// TestDispatchConformanceRouting pins the top-level dispatcher: usage on no
// args, clean help, a clear error on an unknown subcommand, and that each
// documented name routes to its handler (verified by the handler's own
// required-input diagnostic appearing — and the unknown-subcommand path NOT
// firing).
func TestDispatchConformanceRouting(t *testing.T) {
	if code, out := runCapt(t, dispatchConformance, nil); code != 2 || !strings.Contains(out, "usage: rimsky conformance") {
		t.Errorf("empty args: want exit 2 + usage, got exit %d, out:\n%s", code, out)
	}

	if code, _ := runCapt(t, dispatchConformance, []string{"help"}); code != 0 {
		t.Errorf("help: want exit 0, got %d", code)
	}

	code, out := runCapt(t, dispatchConformance, []string{"bogus-subcommand"})
	if code != 2 || !strings.Contains(out, "unknown subcommand") {
		t.Errorf("unknown subcommand: want exit 2 + 'unknown subcommand', got exit %d, out:\n%s", code, out)
	}

	for _, sc := range conformanceSubcommands {
		t.Run(sc.name, func(t *testing.T) {
			code, out := runCapt(t, dispatchConformance, []string{sc.name})
			if strings.Contains(out, "unknown subcommand") {
				t.Errorf("subcommand %q did not route — dispatcher reported it unknown", sc.name)
			}
			if code != sc.reqExit {
				t.Errorf("subcommand %q routed to the wrong handler: want exit %d, got %d", sc.name, sc.reqExit, code)
			}
		})
	}
}

func containsString(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
