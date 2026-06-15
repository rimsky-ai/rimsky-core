// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// compose_run_one_shot_terminal_test.go — STORY-one-shot-to-terminal
// executable proof. Builds the real `rimsky` CLI, drives the mixed-
// outcome two-instance manifest end-to-end through
// `rimsky compose run`, and asserts the falsifier-load-bearing
// properties from both the STORY-one-shot-to-terminal and
// STORY-audit-artifact stories (the pass shares one harness across
// both, per the brainstorm skill's "stories share acceptance passes
// by default" rule):
//
//  1. the verb exits ON ITS OWN with exit code 1 — one instance
//     terminal-success and one terminal-failure classify as
//     ReasonAnyFailure per @decision: exit-codes;
//  2. stderr carries a per-instance summary line for EACH declared
//     instance with its outcome label by name — not a count or a
//     single consolidated line (the falsifier the story names);
//  3. stderr carries the `compose run: any-failure (2 instances)`
//     aggregate line, proving the classifier observed the mixed
//     outcomes rather than defaulting;
//  4. the `<.rimsky>/runs/<latest>/state.db` audit artifact records
//     both instances under the sample-pipeline project, one
//     rimsky_node_runs row in phase 'completed' (the success leg) and
//     one in phase 'failed' (the failure leg) — proving per-node-run
//     history surfaces both terminal classes by name in the artifact;
//  5. the per-run dir has a `blobs/` subdirectory (the filesystem
//     blob backend's root, required for the audit artifact to be
//     complete per the spec's @decision: artifact-layout).
//
// @story: one-shot-to-terminal
// @story: audit-artifact
package scenarios

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	// @deliberate: anonymous sqlite driver import. modernc.org/sqlite is the
	// pure-Go driver rimsky's persistence layer uses; importing it directly
	// here lets the audit-artifact assertions open state.db via database/sql
	// without pulling the persistence package's privacy into the test.
	_ "modernc.org/sqlite"
)

// composeRunSampleManifestRel is the testdata path the scenario copies
// into a per-test working directory. Keeping it relative to the repo
// root (resolved via repoRoot) so the test does not bake a hard-coded
// absolute path.
const composeRunSampleManifestRel = "cmd/rimsky/cli/compose/testdata/sample-manifest"

// composeRunStubExecutorPkg is the build target for the stub-executor
// binary. The test builds it on demand into the per-test tempdir.
const composeRunStubExecutorPkg = "./cmd/rimsky/cli/compose/testdata/stub-executor"

// TestComposeRunOneShotTerminal_E2E exercises STORY-one-shot-to-terminal
// and STORY-audit-artifact through the real `rimsky compose run`
// binary against the mixed-outcome sample manifest. The driver
// verifies:
//   - exit code is 1 (mixed outcome → any-failure);
//   - stderr carries `instance sample-pipeline/ok: success` and
//     `instance sample-pipeline/oops: failure` summary lines — per-
//     instance outcome surfacing by name AND by outcome class;
//   - stderr carries the `compose run: any-failure (2 instances)`
//     aggregate line, proving the classifier observed the outcomes
//     rather than defaulting;
//   - the per-run state.db opens via database/sql and contains
//     two `rimsky_instances` rows (both terminated), one
//     rimsky_node_runs row in phase 'completed', and one in phase
//     'failed' — proving per-node-run terminal class surfaces in the
//     audit artifact, not just instance-level state;
//   - the per-run dir has a `blobs/` subdirectory.
func TestComposeRunOneShotTerminal_E2E(t *testing.T) {
	binDir := t.TempDir()
	rimskyBin := filepath.Join(binDir, "rimsky")
	stubBin := filepath.Join(binDir, "stub-executor")
	buildRimskyCLIBinary(t, rimskyBin)
	buildComposeStubExecutorBinary(t, stubBin)

	// @deliberate: Stage a per-test working directory and copy the sample manifest
	// in. The verb's artifact-root discovery walks up from cwd, so
	// running with cwd=<work> lands the .rimsky/ directory under
	// <work>/.rimsky/.
	work := t.TempDir()
	copyComposeSampleManifest(t, work)

	// @deliberate: Run the verb under a generous timeout — the local rimsky stack
	// boots in well under a second; 90s admits any CI slowness without
	// masking a wedge.
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, rimskyBin,
		"compose", "run",
		"--service", fmt.Sprintf("stub=%s", stubBin),
		"./rimsky-compose.yml",
	)
	cmd.Dir = work
	// @deliberate: Isolate HOME so any default config-lookup path the CLI follows
	// does not stumble on an operator-installed ~/.rimsky.
	cmd.Env = append(os.Environ(), "HOME="+t.TempDir())

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()

	// @deliberate: Capture the exit code. ExitError.ExitCode() carries the verb's
	// own exit value; a fork-level error means the binary itself
	// could not start, which is a test infrastructure problem, not a
	// falsifier hit.
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

	// @constraint: Falsifier #1: exit code MUST be 1 — one instance terminal-success
	// and one terminal-failure classify as ReasonAnyFailure per the
	// spec's @decision: exit-codes. A 0 here would mean the classifier
	// missed the failed instance; a 2 would mean the timeout path
	// fired before the wait observed terminal.
	if rc != 1 {
		t.Fatalf("expected exit code 1 (any-failure for mixed outcome); got %d\nstderr:\n%s", rc, stderrStr)
	}

	// @constraint: Falsifier #2: per-instance summary lines must appear by name AND
	// outcome class. A count-only output (`2 instances done`) is the
	// failure mode the story rules out.
	if !strings.Contains(stderrStr, "instance sample-pipeline/ok: success") {
		t.Fatalf("missing per-instance summary for 'ok' (expected success); stderr:\n%s", stderrStr)
	}
	if !strings.Contains(stderrStr, "instance sample-pipeline/oops: failure") {
		t.Fatalf("missing per-instance summary for 'oops' (expected failure); stderr:\n%s", stderrStr)
	}
	// @constraint: Aggregate summary must surface the any-failure reason — proves
	// the exit-code classification was driven by the observed mixed
	// outcomes rather than a default.
	if !strings.Contains(stderrStr, "compose run: any-failure (2 instances)") {
		t.Fatalf("missing 'compose run: any-failure (2 instances)' aggregate summary; stderr:\n%s", stderrStr)
	}

	// @deliberate: Locate the per-run artifact directory via .rimsky/latest. The
	// readlink target is the directory the verb wrote under
	// .rimsky/runs/<timestamp>-<name>.
	rimskyDir := filepath.Join(work, ".rimsky")
	if _, statErr := os.Stat(rimskyDir); statErr != nil {
		t.Fatalf(".rimsky/ not created by verb (audit-artifact falsifier): %v", statErr)
	}
	latestTarget, lerr := os.Readlink(filepath.Join(rimskyDir, "latest"))
	if lerr != nil {
		t.Fatalf(".rimsky/latest symlink missing or unreadable: %v", lerr)
	}
	runDir := latestTarget
	if !filepath.IsAbs(runDir) {
		runDir = filepath.Clean(filepath.Join(rimskyDir, latestTarget))
	}

	// @constraint: Falsifier #3a: blobs/ subdirectory must exist under the run
	// dir — the filesystem-blob backend's root and the spec's
	// `<.rimsky>/runs/<latest>/blobs/` artifact-layout invariant.
	if info, statErr := os.Stat(filepath.Join(runDir, "blobs")); statErr != nil || !info.IsDir() {
		t.Fatalf("run dir missing blobs/ subdir (audit-artifact falsifier): err=%v info=%+v", statErr, info)
	}

	// @constraint: Falsifier #3b: state.db must load and record both instances
	// plus one completed + one failed node-run row. A state.db that
	// records only "last-known status flags" — the falsifier the
	// story names — would have no rimsky_node_runs rows at all, OR
	// would collapse the failed leg into the same phase as the
	// successful one.
	dbPath := filepath.Join(runDir, "state.db")
	db, dbErr := sql.Open("sqlite", dbPath)
	if dbErr != nil {
		t.Fatalf("open state.db: %v (path %s)", dbErr, dbPath)
	}
	defer db.Close()

	var instanceCount int
	if qerr := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM rimsky_instances`).Scan(&instanceCount); qerr != nil {
		t.Fatalf("count rimsky_instances: %v", qerr)
	}
	if instanceCount != 2 {
		t.Fatalf("expected 2 instances in state.db; got %d", instanceCount)
	}

	// @deliberate: Both instances must be terminated — the verb's TerminateAfterRun
	// hook is what makes the wait loop ever exit, so a non-terminated
	// row here means the wait loop returned early.
	var terminatedCount int
	if qerr := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM rimsky_instances WHERE terminated_at IS NOT NULL`).Scan(&terminatedCount); qerr != nil {
		t.Fatalf("count terminated instances: %v", qerr)
	}
	if terminatedCount != 2 {
		t.Fatalf("expected 2 terminated instances; got %d", terminatedCount)
	}

	// @deliberate: Falsifier #3c: per-node-run phase distribution — one completed
	// (the success leg) and one failed (the failure leg). The
	// rimsky_node_runs.phase column is the audit-trail terminal label
	// for each dispatch (CHECK constraint:
	// pending|active|held|parked|completed|failed). A run where both
	// phases were 'completed' would mean the failure leg was masked;
	// a run with neither row in phase 'failed' would mean the runner
	// fell back to a different terminal class than the stub's
	// emitted Error.
	var completedCount, failedCount int
	if qerr := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM rimsky_node_runs WHERE phase = 'completed'`).Scan(&completedCount); qerr != nil {
		t.Fatalf("count completed node-runs: %v", qerr)
	}
	if qerr := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM rimsky_node_runs WHERE phase = 'failed'`).Scan(&failedCount); qerr != nil {
		t.Fatalf("count failed node-runs: %v", qerr)
	}
	if completedCount < 1 {
		t.Fatalf("expected at least one rimsky_node_runs row in phase 'completed' (success leg); got %d", completedCount)
	}
	if failedCount < 1 {
		t.Fatalf("expected at least one rimsky_node_runs row in phase 'failed' (failure leg); got %d", failedCount)
	}

	// @deliberate: Falsifier #3d: per-node names are recorded — the operator can
	// follow the manifest's node type to a per-node-run row. The
	// audit-artifact story's Falsifier names "only state metadata
	// (last-known status flags) without per-node-run history" — verify
	// the run's nodes are recorded by name via rimsky_nodes.node_type
	// so a post-mortem reader can navigate from instance ID to per-
	// node-run row by the same name the manifest declared.
	var workerNodeCount int
	if qerr := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM rimsky_nodes WHERE node_type = 'worker'`).Scan(&workerNodeCount); qerr != nil {
		t.Fatalf("count worker nodes: %v", qerr)
	}
	if workerNodeCount < 2 {
		t.Fatalf("expected >=2 worker nodes recorded by name; got %d", workerNodeCount)
	}

}

// buildComposeStubExecutorBinary compiles the testdata stub-executor
// into the given path. Distinct from buildRimskyCLIBinary (the rimsky
// CLI itself) so the failure surface names which target failed.
func buildComposeStubExecutorBinary(t *testing.T, binPath string) {
	t.Helper()
	cmd := exec.Command("go", "build", "-o", binPath, composeRunStubExecutorPkg)
	cmd.Dir = repoRoot(t)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go build %s: %v\nstderr:\n%s", composeRunStubExecutorPkg, err, stderr.String())
	}
}

// copyComposeSampleManifest copies every file from the testdata sample-
// manifest directory into dst. The verb's artifact-root walker resolves
// .rimsky/ relative to cwd, so dst becomes the operator's "working
// directory" for the verb run.
func copyComposeSampleManifest(t *testing.T, dst string) {
	t.Helper()
	src := filepath.Join(repoRoot(t), filepath.FromSlash(composeRunSampleManifestRel))
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatalf("read sample-manifest dir: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, rerr := os.ReadFile(filepath.Join(src, e.Name()))
		if rerr != nil {
			t.Fatalf("read %s: %v", e.Name(), rerr)
		}
		if werr := os.WriteFile(filepath.Join(dst, e.Name()), data, 0o644); werr != nil {
			t.Fatalf("write %s into %s: %v", e.Name(), dst, werr)
		}
	}
}
