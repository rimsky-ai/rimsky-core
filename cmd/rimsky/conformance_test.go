// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package main

import (
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

type tallyCheckResult struct {
	Name string
	Err  error
}

func reportTally(results []tallyCheckResult) int {
	return reportConformanceResults("rimsky conformance widget", results,
		func(r tallyCheckResult) string { return r.Name },
		func(r tallyCheckResult) error { return r.Err })
}

func TestReportConformanceResults_AllPassing(t *testing.T) {
	results := []tallyCheckResult{
		{Name: "check-a"},
		{Name: "check-b"},
	}
	var code int
	stderr := captureStderr(t, func() {
		stdout := captureStdout(t, func() {
			code = reportTally(results)
		})
		if !strings.Contains(stdout, "ok    check-a\n") || !strings.Contains(stdout, "ok    check-b\n") {
			t.Fatalf("stdout missing expected ok lines: %q", stdout)
		}
	})
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty (no summary line when nothing failed)", stderr)
	}
}

func TestReportConformanceResults_SomeFailing(t *testing.T) {
	boom := errors.New("boom")
	results := []tallyCheckResult{
		{Name: "check-a"},
		{Name: "check-b", Err: boom},
		{Name: "check-c", Err: boom},
	}
	var code int
	var stdout string
	stderr := captureStderr(t, func() {
		stdout = captureStdout(t, func() {
			code = reportTally(results)
		})
	})
	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if !strings.Contains(stdout, "ok    check-a\n") {
		t.Fatalf("stdout missing passing check: %q", stdout)
	}
	if !strings.Contains(stdout, "FAIL  check-b: boom\n") || !strings.Contains(stdout, "FAIL  check-c: boom\n") {
		t.Fatalf("stdout missing FAIL lines: %q", stdout)
	}
	if !strings.Contains(stderr, "rimsky conformance widget: 2/3 checks failed\n") {
		t.Fatalf("stderr summary = %q, want the widget/2/3 tally line", stderr)
	}
}

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

func runCapt(t *testing.T, fn func([]string) int, args []string) (int, string) {
	t.Helper()
	var code int
	out := captureStderr(t, func() { code = fn(args) })
	return code, out
}

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
		flags:   []string{"endpoint", "transport", "allow-live", "scenarios", "skip", "timeout", "check-observability", "retention-test-seconds", "callback-bind", "callback-host", "tls"},
		reqMsg:  "--endpoint required",
		reqExit: 2,
	},
	{
		name:    "claim-producer",
		run:     runConformanceClaimProducer,
		flags:   []string{"endpoint", "transport", "timeout", "check-observability", "retention-test-seconds"},
		reqMsg:  "--endpoint required",
		reqExit: 2,
	},
	{
		name:    "publisher",
		run:     runConformancePublisher,
		flags:   []string{"endpoint", "transport", "kind", "resolved-config", "timeout", "instance-id", "message-type", "control-api"},
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
		reqExit: 2,
	},
	{
		name:    "lifecycle-subscriber",
		run:     runConformanceLifecycleSubscriber,
		flags:   []string{"endpoint", "transport", "timeout", "tls"},
		reqMsg:  "--endpoint required",
		reqExit: 2,
	},
}

func TestConformanceSubcommandsRegisterDocumentedFlags(t *testing.T) {
	for _, sc := range conformanceSubcommands {
		t.Run(sc.name, func(t *testing.T) {
			_, usage := runCapt(t, sc.run, []string{"-h"})
			for _, f := range sc.flags {
				if !strings.Contains(usage, "-"+f) {
					t.Errorf("subcommand %q: documented flag --%s missing from usage:\n%s", sc.name, f, usage)
				}
			}
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

// @concept: conformance
func TestNoProtocolSuiteHangsOffAnotherProtocolsSubcommand(t *testing.T) {
	protocols := []string{"executor", "claim-producer", "publisher", "validation", "data-processing", "blob-backend", "lifecycle-subscriber"}
	for _, sc := range conformanceSubcommands {
		t.Run(sc.name, func(t *testing.T) {
			_, usage := runCapt(t, sc.run, []string{"-h"})
			for _, line := range strings.Split(usage, "\n") {
				trimmed := strings.TrimSpace(line)
				if !strings.HasPrefix(trimmed, "-") {
					continue
				}
				name := strings.SplitN(strings.TrimPrefix(trimmed, "-"), " ", 2)[0]
				for _, p := range protocols {
					if p == sc.name {
						continue
					}
					if strings.Contains(name, p) {
						t.Errorf("subcommand %q registers flag -%s naming the %q protocol: each protocol's suite is reachable through its own subcommand only", sc.name, name, p)
					}
				}
			}
		})
	}
}

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
