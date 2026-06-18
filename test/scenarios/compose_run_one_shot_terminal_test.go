// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

//     ReasonAnyFailure per @decision: exit-codes;
//     complete per the spec's @decision: artifact-layout);
// @story: one-shot-to-terminal
// @story: audit-artifact
// @decision: compose-driver-emits-empty-message-after-create
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

	_ "modernc.org/sqlite"
)

const composeRunSampleManifestRel = "cmd/rimsky/cli/compose/testdata/sample-manifest"

const composeRunStubExecutorPkg = "./cmd/rimsky/cli/compose/testdata/stub-executor"

func TestComposeRunOneShotTerminal_E2E(t *testing.T) {
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

	// spec's @decision: exit-codes. A 0 here would mean the classifier
	if rc != 1 {
		t.Fatalf("expected exit code 1 (any-failure for mixed outcome); got %d\nstderr:\n%s", rc, stderrStr)
	}

	if !strings.Contains(stderrStr, "instance sample-pipeline/ok: success") {
		t.Fatalf("missing per-instance summary for 'ok' (expected success); stderr:\n%s", stderrStr)
	}
	if !strings.Contains(stderrStr, "instance sample-pipeline/oops: failure") {
		t.Fatalf("missing per-instance summary for 'oops' (expected failure); stderr:\n%s", stderrStr)
	}
	if !strings.Contains(stderrStr, "compose run: any-failure (2 instances)") {
		t.Fatalf("missing 'compose run: any-failure (2 instances)' aggregate summary; stderr:\n%s", stderrStr)
	}

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

	if info, statErr := os.Stat(filepath.Join(runDir, "blobs")); statErr != nil || !info.IsDir() {
		t.Fatalf("run dir missing blobs/ subdir (audit-artifact falsifier): err=%v info=%+v", statErr, info)
	}

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

	var terminatedCount int
	if qerr := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM rimsky_instances WHERE terminated_at IS NOT NULL`).Scan(&terminatedCount); qerr != nil {
		t.Fatalf("count terminated instances: %v", qerr)
	}
	if terminatedCount != 2 {
		t.Fatalf("expected 2 terminated instances; got %d", terminatedCount)
	}

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

	var workerNodeCount int
	if qerr := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM rimsky_nodes WHERE node_type = 'worker'`).Scan(&workerNodeCount); qerr != nil {
		t.Fatalf("count worker nodes: %v", qerr)
	}
	if workerNodeCount < 2 {
		t.Fatalf("expected >=2 worker nodes recorded by name; got %d", workerNodeCount)
	}

	var composeWakeCount int
	if qerr := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM rimsky_message_idempotencies WHERE idempotency_key LIKE 'compose-wake-%'`).Scan(&composeWakeCount); qerr != nil {
		t.Fatalf("count compose-wake idempotency rows: %v", qerr)
	}
	if composeWakeCount < 2 {
		t.Fatalf("expected >=2 rimsky_message_idempotencies rows under 'compose-wake-%%' "+
			"(one per declared instance); got %d. This means the compose driver "+
			"did NOT emit the empty-message wake step between ApplyPlan and the "+
			"wait-for-terminal loop, which is the spec's post-instance-create-is-idle "+
			"contract preserver", composeWakeCount)
	}

	var composeWakeMessageCount int
	if qerr := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM rimsky_messages m
		 JOIN rimsky_message_idempotencies idm ON idm.message_id = m.id
		 WHERE idm.idempotency_key LIKE 'compose-wake-%'
		   AND m.type = ''
		   AND m.sender_kind = 'operator'
	`).Scan(&composeWakeMessageCount); qerr != nil {
		t.Fatalf("count compose-wake empty-typed ledger rows: %v", qerr)
	}
	if composeWakeMessageCount < 2 {
		t.Fatalf("expected >=2 empty-typed operator-sender rows in rimsky_messages tied "+
			"to compose-wake idempotency keys; got %d. The compose driver's wake step "+
			"must travel the universal POST /messages surface with type='' and "+
			"sender_kind='operator' — a wake taking any other shape is the spec falsifier", composeWakeMessageCount)
	}
}

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
