// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// compose_run_spawned_services_test.go — STORY-spawned-local-services
// executable proof. Drives the real `rimsky compose run` binary
// against the mixed-outcome two-instance manifest whose nodes both
// target a stub executor the verb spawns via `--service stub=<path>`.
// The driver parses stderr for the `spawned service` slog envelope
// the verb emits at info level, captures the child PID, waits for the
// verb to exit, then signals PID 0 to the captured PID to confirm
// the OS no longer maps it to a live process. A surviving stub-
// executor process after the verb's drain returns is exactly the
// Falsifier the story names ("the binary spawns but is leaked
// after the verb exits").
//
// The mixed-outcome manifest exhibits both Falsifier #1 (the manifest's
// nodes reached the spawned binary — both dispatches landed terminal
// rows, success and failure) and Falsifier #3 (the spawned PID is gone
// after exit). Falsifier #2 (the spawn happened — emit a `spawned
// service` JSON log envelope with pid+port) is independent of the
// outcome shape.
//
// @story: spawned-local-services
// @blessed-invariant: spawn-child-reaped-on-exit — verifies the
// `rimsky compose run` drain coordinator's SIGTERM-then-SIGKILL
// path reaps every spawned --service child before the verb returns,
// so a `kill -0 <pid>` after exit returns "process not found".
package scenarios

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestComposeRunSpawnedServices_NoLeakAfterExit exercises
// STORY-spawned-local-services through the real `rimsky compose run`
// binary against the mixed-outcome sample manifest. The driver verifies:
//
//   - the verb spawns the stub-executor binary (proven by the
//     `spawned service` JSON-envelope log line on stderr carrying a
//     numeric pid + port);
//   - the verb returns on its own (mixed outcome → exit 1, but the
//     verb is the one classifying the run as any-failure and exiting;
//     the load-bearing claim is "exits on its own", not the specific
//     code);
//   - the manifest's nodes actually reached the spawned binary —
//     proven by the per-instance summary lines on stderr for both
//     `ok: success` (the stub's default Success terminal) and
//     `oops: failure` (the stub's outcome="fail" terminal). Either
//     dispatch failing to reach the stub would be a different
//     pre-spawn error path;
//   - after the verb returns, `kill -0 <pid>` against the captured
//     stub PID returns process-not-found (the spawn was reaped
//     during drain, no leak).
func TestComposeRunSpawnedServices_NoLeakAfterExit(t *testing.T) {
	binDir := t.TempDir()
	rimskyBin := filepath.Join(binDir, "rimsky")
	stubBin := filepath.Join(binDir, "stub-executor")
	buildRimskyCLIBinary(t, rimskyBin)
	buildComposeStubExecutorBinary(t, stubBin)

	work := t.TempDir()
	copyComposeSampleManifest(t, work)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, rimskyBin,
		"compose", "run",
		"--service", fmt.Sprintf("stub=%s", stubBin),
		"./rimsky-compose.yml",
	)
	cmd.Dir = work
	cmd.Env = append(os.Environ(), "HOME="+t.TempDir())

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()

	rc := -1
	if err == nil {
		rc = 0
	} else {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			rc = exitErr.ExitCode()
		} else {
			t.Fatalf("rimsky compose run fork-level failure: %v\nstderr:\n%s", err, stderr.String())
		}
	}

	stderrStr := stderr.String()

	// @deliberate: The mixed-outcome manifest classifies as any-failure → exit 1.
	// The PRIMARY proof for STORY-spawned-local-services is the PID-
	// reaped check below; the exit-code shape is the corroborating
	// "verb exited on its own" surface, not the load-bearing one.
	if rc != 1 {
		t.Fatalf("expected exit code 1 (any-failure for mixed-outcome manifest); got %d\nstderr:\n%s", rc, stderrStr)
	}

	// @constraint: Falsifier #1: the spawn must have happened. The verb emits a
	// `spawned service` info-level slog line at spawn time carrying
	// the child PID, port, name, and path. Parsing the structured
	// envelope (not a regex against the human-readable message)
	// keeps the assertion brittle in the right direction — if the
	// log format drifts, the test fails loudly rather than masking
	// the spawn missing.
	stubPID := parseSpawnedServicePID(t, stderrStr, "stub")
	if stubPID <= 0 {
		t.Fatalf("could not locate `spawned service` log envelope for name=stub in stderr:\n%s", stderrStr)
	}

	// @constraint: Falsifier #2: the manifest's nodes must have actually reached
	// the spawned binary. The per-instance summary lines prove the
	// dispatches landed at the stub executor (any other executor or
	// a never-reachable spawn would have failed the dispatch). Both
	// outcome classes prove the dispatch round-trip happened — the
	// success leg's terminal AND the failure leg's terminal are
	// emitted by the stub itself.
	if !strings.Contains(stderrStr, "instance sample-pipeline/ok: success") {
		t.Fatalf("missing 'ok: success' summary — dispatch never reached the spawned stub:\n%s", stderrStr)
	}
	if !strings.Contains(stderrStr, "instance sample-pipeline/oops: failure") {
		t.Fatalf("missing 'oops: failure' summary — failure-leg dispatch never reached the spawned stub:\n%s", stderrStr)
	}

	// @constraint: Falsifier #3 (the load-bearing claim): the stub PID must be
	// gone after the verb returns. Allow a short grace window —
	// SIGTERM propagation through the gRPC server's GracefulStop is
	// asynchronous but bounded; the verb's drain coordinator gives
	// the child up to 5s before escalating to SIGKILL.
	if !waitProcessGone(stubPID, 10*time.Second) {
		t.Fatalf("stub-executor PID %d still alive after `compose run` returned — Falsifier triggered (spawn leaked)", stubPID)
	}
}

// parseSpawnedServicePID walks the verb's stderr for the slog JSON
// envelope `{"msg":"spawned service","name":"<name>","pid":<N>,...}`
// and returns the pid field. Returns 0 when no matching envelope is
// found.
//
// The verb emits this line via the JSON-handler slog.Info call in
// spawnServices (cmd/rimsky/cli/compose/run.go); the wire shape is
// the JSON shape `slog.NewJSONHandler` produces. Lines that aren't
// valid JSON (the stub-executor itself writes some legacy
// stderr-text lines) are skipped silently.
func parseSpawnedServicePID(t *testing.T, stderrStr, name string) int {
	t.Helper()
	for _, line := range strings.Split(stderrStr, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}
		var env map[string]any
		if jerr := json.Unmarshal([]byte(line), &env); jerr != nil {
			continue
		}
		if env["msg"] != "spawned service" {
			continue
		}
		if envName, _ := env["name"].(string); envName != name {
			continue
		}
		// @deliberate: JSON numbers decode as float64 by default through
		// encoding/json; cast to int after a non-zero check.
		pidVal, ok := env["pid"].(float64)
		if !ok {
			t.Fatalf("`spawned service` envelope missing numeric pid field: %s", line)
		}
		return int(pidVal)
	}
	return 0
}

// @constraint: waitProcessGone is defined in host_agent_control_plane_demo_test.go;
// re-using it here keeps the no-leak check uniform with the host-agent
// reap proof. The pattern: `syscall.Kill(pid, 0)` returns
// process-not-found (ESRCH) once the OS reclaims the PID; polling
// with a short backoff lets SIGTERM-then-graceful-stop drain without
// flapping.
