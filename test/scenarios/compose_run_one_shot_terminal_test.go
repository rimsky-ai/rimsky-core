// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @story: one-shot-to-terminal
// @story: audit-artifact
// @decision: compose-driver-sends-empty-message-after-create
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
	buildRepoBinary(t, "./cmd/rimsky", rimskyBin)
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
		`SELECT COUNT(*) FROM rimsky_node_runs WHERE state = 'fresh'`).Scan(&completedCount); qerr != nil {
		t.Fatalf("count completed node-runs: %v", qerr)
	}
	if qerr := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM rimsky_node_runs WHERE state = 'failed'`).Scan(&failedCount); qerr != nil {
		t.Fatalf("count failed node-runs: %v", qerr)
	}
	if completedCount < 1 {
		t.Fatalf("expected at least one rimsky_node_runs row in state 'fresh' (success leg); got %d", completedCount)
	}
	if failedCount < 1 {
		t.Fatalf("expected at least one rimsky_node_runs row in state 'failed' (failure leg); got %d", failedCount)
	}

	var workerNodeCount int
	if qerr := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM rimsky_nodes WHERE node_type = 'worker'`).Scan(&workerNodeCount); qerr != nil {
		t.Fatalf("count worker nodes: %v", qerr)
	}
	if workerNodeCount < 2 {
		t.Fatalf("expected >=2 worker nodes recorded by name; got %d", workerNodeCount)
	}

	var failEventCount int
	if qerr := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM rimsky_events e
		  JOIN rimsky_nodes n ON n.id = e.node_id
		  JOIN rimsky_instances i ON i.id = n.instance_id
		 WHERE i.instance_key LIKE '%oops%'
		   AND e.kind = 'terminal/error/stub/failed'
	`).Scan(&failEventCount); qerr != nil {
		t.Fatalf("count failing node-run's terminal event in the event log: %v", qerr)
	}
	if failEventCount < 1 {
		t.Fatalf("expected >=1 terminal/error/stub/failed event for the failing node-run "+
			"in rimsky_events (audit-artifact: the event log must be readable from the "+
			"artifact); got %d", failEventCount)
	}

	var failPayload string
	if qerr := db.QueryRowContext(ctx, `
		SELECT e.payload FROM rimsky_events e
		  JOIN rimsky_nodes n ON n.id = e.node_id
		  JOIN rimsky_instances i ON i.id = n.instance_id
		 WHERE i.instance_key LIKE '%oops%'
		   AND e.kind = 'terminal/error/stub/failed'
		 LIMIT 1
	`).Scan(&failPayload); qerr != nil {
		t.Fatalf("read failing node-run's terminal event payload: %v", qerr)
	}
	if !strings.Contains(failPayload, `"error_class":"stub/failed"`) {
		t.Fatalf("failing node-run's terminal event payload missing error_class=stub/failed "+
			"(audit-artifact: pulling the failing node-run's terminal event out by hand); "+
			"payload=%s", failPayload)
	}

	var outcomeAttrData string
	if qerr := db.QueryRowContext(ctx, `
		SELECT a.data FROM rimsky_node_attributes a
		  JOIN rimsky_nodes n ON n.id = a.node_id
		  JOIN rimsky_instances i ON i.id = n.instance_id
		 WHERE i.instance_key LIKE '%oops%'
		 ORDER BY a.updated_at DESC LIMIT 1
	`).Scan(&outcomeAttrData); qerr != nil {
		t.Fatalf("read failing node-run's attribute values: %v", qerr)
	}
	if !strings.Contains(outcomeAttrData, `"outcome":"fail"`) {
		t.Fatalf("failing node-run's attribute bag missing outcome=fail "+
			"(audit-artifact: pulling attribute values out by hand); data=%s", outcomeAttrData)
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

func TestComposeRunOneShotTerminal_QueryableWithStockSqlite3CLI(t *testing.T) {
	sqlite3Path, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("sqlite3 not on PATH; cannot verify the durable artifact is queryable with the stock CLI")
	}

	binDir := t.TempDir()
	rimskyBin := filepath.Join(binDir, "rimsky")
	stubBin := filepath.Join(binDir, "stub-executor")
	buildRepoBinary(t, "./cmd/rimsky", rimskyBin)
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
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err = cmd.Run()
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("rimsky compose run fork-level failure: %v\nstderr:\n%s", err, stderr.String())
		}
	}

	rimskyDir := filepath.Join(work, ".rimsky")
	latestTarget, lerr := os.Readlink(filepath.Join(rimskyDir, "latest"))
	if lerr != nil {
		t.Fatalf(".rimsky/latest symlink missing or unreadable: %v", lerr)
	}
	runDir := latestTarget
	if !filepath.IsAbs(runDir) {
		runDir = filepath.Clean(filepath.Join(rimskyDir, latestTarget))
	}
	dbPath := filepath.Join(runDir, "state.db")

	out, sErr := exec.CommandContext(ctx, sqlite3Path, dbPath,
		"SELECT COUNT(*) FROM rimsky_instances;").CombinedOutput()
	if sErr != nil {
		t.Fatalf("stock sqlite3 CLI could not query state.db (not a valid stock-sqlite3-readable file): %v\noutput:\n%s", sErr, out)
	}
	if strings.TrimSpace(string(out)) != "2" {
		t.Fatalf("stock sqlite3 CLI reported %q instances, want 2; output:\n%s", strings.TrimSpace(string(out)), out)
	}

	out, sErr = exec.CommandContext(ctx, sqlite3Path, dbPath,
		"SELECT COUNT(*) FROM rimsky_events;").CombinedOutput()
	if sErr != nil {
		t.Fatalf("stock sqlite3 CLI could not query rimsky_events in state.db: %v\noutput:\n%s", sErr, out)
	}
	if n := strings.TrimSpace(string(out)); n == "0" || n == "" {
		t.Fatalf("stock sqlite3 CLI reported %q rows in rimsky_events, want > 0 (event log must be queryable from the artifact); output:\n%s", n, out)
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
