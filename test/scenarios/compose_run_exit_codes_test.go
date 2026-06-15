// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// compose_run_exit_codes_test.go — STORY-script-friendly-outcome
// executable proof. Builds the real `rimsky compose run` binary and
// runs it three times against three different manifests, asserting
// the three distinct exit codes a wrapper script needs to distinguish:
//
//   - 0 (all-success): two-instance manifest where every instance
//     reaches terminal as success (rimsky-compose-success.yml);
//   - 1 (any-failure): mixed-outcome manifest where one instance
//     succeeds and one fails at dispatch time — the stub executor
//     emits StreamClose.Error (error_class `stub/failed`) for the
//     `oops` instance, the runner settles it as ColorFailed under
//     the default fail-fast policy, and the verb's classifier maps
//     the mixed roster to ReasonAnyFailure (rimsky-compose.yml);
//   - 2 (timeout): manifest whose stub-executor dispatch sleeps for
//     30 seconds (delay_ms=30000) under a --timeout 1s bound. The
//     verb's terminal-wait loop never sees terminated_at flip, the
//     1-second timer fires, the verb drains with ReasonTimeout and
//     returns exit 2 (rimsky-compose-live.yml).
//
// The three subtests cover the spec's @decision: exit-codes table
// (all-success → 0, any-failure → 1, wall-clock-bound → 2). A
// wrapper script that branches on these three codes can react to
// the three outcome classes without parsing log output.
//
// @story: script-friendly-outcome
package scenarios

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// composeRunSuccessManifestRel is the testdata path for the all-success
// manifest that drives the exit-code-0 subtest. Distinct from the
// active rimsky-compose.yml (mixed-outcome → exit 1) so the success
// leg is unambiguous. Consumed by the success subtest via
// filepath.Base — the full repo-relative form lives here so a future
// move of the testdata file surfaces here rather than scattered
// across the subtest argv strings.
const composeRunSuccessManifestRel = "cmd/rimsky/cli/compose/testdata/sample-manifest/rimsky-compose-success.yml"

// composeRunLiveManifestRel is the testdata path for the live-progress
// (slow + fast) manifest. The slow leg's `delay_ms` template attribute
// makes the slow instance's dispatch hold for several seconds — the
// timeout subtest reuses it under a --timeout 1s bound so the wait
// loop's deadline fires before the slow instance terminates. Consumed
// by the timeout subtest via filepath.Base.
const composeRunLiveManifestRel = "cmd/rimsky/cli/compose/testdata/sample-manifest/rimsky-compose-live.yml"

// TestComposeRunExitCodes_ThreeClasses asserts the three exit-code
// classes the script-friendly-outcome story names: 0 for all-success,
// 1 for any-failure, 2 for wall-clock bound exceeded. Each subtest
// runs the real `rimsky compose run` binary as a subprocess, captures
// its exit code via exec.Command, and asserts the expected value.
func TestComposeRunExitCodes_ThreeClasses(t *testing.T) {
	binDir := t.TempDir()
	rimskyBin := filepath.Join(binDir, "rimsky")
	stubBin := filepath.Join(binDir, "stub-executor")
	buildRimskyCLIBinary(t, rimskyBin)
	buildComposeStubExecutorBinary(t, stubBin)

	// @deliberate: Sub-test isolation: each subtest gets its own working directory
	// so the per-run artifact root is fresh.

	t.Run("success_exit_0", func(t *testing.T) {
		work := t.TempDir()
		copyComposeSampleManifest(t, work)
		rc, out := runComposeRunBinary(t, rimskyBin, work, []string{
			"--service", "stub=" + stubBin,
			"./" + filepath.Base(composeRunSuccessManifestRel),
		}, 90*time.Second)
		if rc != 0 {
			t.Fatalf("expected exit code 0 for all-success; got %d\nstderr:\n%s", rc, out)
		}
	})

	t.Run("failure_exit_1", func(t *testing.T) {
		work := t.TempDir()
		copyComposeSampleManifest(t, work)
		// @deliberate: The mixed-outcome manifest (the active rimsky-compose.yml)
		// exercises a real dispatch-time failure: one instance
		// terminal-success and one terminal-failure (the stub
		// executor's outcome="fail" terminal, settled under the
		// runner's default fail-fast policy). The verb's classifier
		// maps the mixed roster to ReasonAnyFailure and returns
		// exit 1 — the load-bearing exit-code class for the story.
		rc, out := runComposeRunBinary(t, rimskyBin, work, []string{
			"--service", "stub=" + stubBin,
			"./rimsky-compose.yml",
		}, 90*time.Second)
		if rc != 1 {
			t.Fatalf("expected exit code 1 for any-failure; got %d\nstderr:\n%s", rc, out)
		}
	})

	t.Run("timeout_exit_2", func(t *testing.T) {
		work := t.TempDir()
		copyComposeSampleManifest(t, work)
		// @deliberate: The live manifest's slow instance has a delay_ms=3000
		// attribute. Setting --timeout 1s puts the verb's wait
		// deadline well below the dispatch's settle time, so the
		// timer fires while the slow instance is still mid-flight.
		rc, out := runComposeRunBinary(t, rimskyBin, work, []string{
			"--service", "stub=" + stubBin,
			"--timeout", "1s",
			"./" + filepath.Base(composeRunLiveManifestRel),
		}, 30*time.Second)
		if rc != 2 {
			t.Fatalf("expected exit code 2 for wall-clock-bound; got %d\nstderr:\n%s", rc, out)
		}
	})
}

// runComposeRunBinary runs the rimsky CLI's `compose run` verb as a
// subprocess and returns its exit code + combined stdout/stderr. A
// fork-level error (the binary couldn't start) fatals the test —
// that's a test-infrastructure failure, not a verb behavior to
// assert against.
func runComposeRunBinary(
	t *testing.T,
	rimskyBin string,
	cwd string,
	args []string,
	timeout time.Duration,
) (int, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	fullArgs := append([]string{"compose", "run"}, args...)
	cmd := exec.CommandContext(ctx, rimskyBin, fullArgs...)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), "HOME="+t.TempDir())
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	combined := stderr.String()
	if so := stdout.String(); so != "" {
		combined = fmt.Sprintf("%s\n[stdout]\n%s", combined, so)
	}
	if err == nil {
		return 0, combined
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), combined
	}
	t.Fatalf("rimsky compose run fork-level failure: %v\noutput:\n%s", err, combined)
	return -1, combined
}
