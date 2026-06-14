// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @blessed-invariant: migrations-run-before-runners — the
// persistence driver's Migrate completes successfully BEFORE any
// role runner opens the database. The TestMigrationsRunBeforeRunners
// case below boots StartRoleStack against a fresh tempdir-rooted
// sqlite file and asserts the schema_migrations bookkeeping row
// exists immediately after the start returns — verifying that the
// supervisor's first claim-poll transaction cannot land on an empty
// schema.
package compose_test

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	// @deliberate: anonymous sqlite driver import. modernc.org/sqlite
	// is the pure-Go sqlite driver rimsky's persistence layer also
	// uses (see lib/foundation/persistence/sqlite); we import it
	// directly in the test so the BI verification can open state.db
	// via database/sql and read the schema-migrations table without
	// pulling in any of the persistence package's privacy.
	_ "modernc.org/sqlite"

	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli/compose"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/hostagent"
)

// setupRoleStackEnv writes synthetic rimsky.yml + supervisor.yml
// into a tempdir, picks a free control-api port, sets the env vars
// the role runners read, and returns the run-dir path, the
// resolved endpoint, and the picked port. Cleanup restores the
// process env. Shared across the launcher tests so each one boots
// against an isolated tempdir-rooted sqlite + filesystem-blob
// topology.
func setupRoleStackEnv(t *testing.T) (runDir, endpoint string, port int) {
	t.Helper()
	runDir = t.TempDir()
	if err := os.MkdirAll(filepath.Join(runDir, "blobs"), 0o755); err != nil {
		t.Fatalf("mkdir blobs: %v", err)
	}
	if err := compose.WriteSyntheticRimskyYAML(runDir, &compose.Manifest{Project: "test-launcher"}, nil, nil); err != nil {
		t.Fatalf("write rimsky.yml: %v", err)
	}
	// @constraint: exercise the same WriteSyntheticSupervisorYAMLWithCallbackPort
	// helper the production verb uses (run.go), with callbackPort=0 so
	// the kernel picks a free port at supervisor bind time. The default
	// WriteSyntheticSupervisorYAML hardcodes 9100, which would collide
	// with any other rimsky process holding the port — including
	// concurrent test runs on the same host. Calling the production
	// helper here means a regression that breaks the splice (e.g., the
	// baked-default line shape drifting and degrading strings.Replace
	// to a no-op) surfaces in the launcher tests instead of only in
	// downstream scenario tests.
	if err := compose.WriteSyntheticSupervisorYAMLWithCallbackPort(runDir, 0); err != nil {
		t.Fatalf("WriteSyntheticSupervisorYAMLWithCallbackPort: %v", err)
	}
	port, err := hostagent.FreeLocalPort()
	if err != nil {
		t.Fatalf("FreeLocalPort: %v", err)
	}
	endpoint = fmt.Sprintf("http://127.0.0.1:%d", port)
	t.Setenv("RIMSKY_CONFIG", filepath.Join(runDir, "rimsky.yml"))
	t.Setenv("RIMSKY_SUPERVISOR_CONFIG", filepath.Join(runDir, "supervisor.yml"))
	t.Setenv("RIMSKY_PROCESS_ROLE", "unified")
	t.Setenv("RIMSKY_CONTROL_API_HOST", "127.0.0.1")
	t.Setenv("RIMSKY_CONTROL_API_PORT", strconv.Itoa(port))
	// Disable metrics so the per-role offset doesn't collide with
	// any other process on the host and so a metrics bind failure
	// can't masquerade as a role-stack startup failure.
	t.Setenv("RIMSKY_METRICS_PORT", "")
	return runDir, endpoint, port
}

// silentLogger returns a slog.Logger that discards every record.
// The role runners log at Info on every start step; routing the
// output to io.Discard keeps test output focused on the failure
// surface.
func silentLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

// TestStartRoleStack_BootsAndDrains is the primary happy-path
// integration test for Pass 4: synthetic configs on disk, env vars
// set, StartRoleStack returns a usable stack whose Drain releases
// every resource. A passing run also implicitly verifies the
// post-migrate close-then-reopen rule — the role runners can open
// the same sqlite file the migrate driver just closed.
func TestStartRoleStack_BootsAndDrains(t *testing.T) {
	runDir, endpoint, _ := setupRoleStackEnv(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stack, err := compose.StartRoleStack(ctx, silentLogger(), filepath.Join(runDir, "rimsky.yml"), endpoint)
	if err != nil {
		t.Fatalf("StartRoleStack: %v", err)
	}
	if got := stack.Endpoint(); got != endpoint {
		t.Errorf("Endpoint() = %q, want %q", got, endpoint)
	}
	if err := compose.WaitForControlAPIReady(ctx, stack.Endpoint(), 5*time.Second); err != nil {
		stack.Drain(context.Background(), 5*time.Second)
		t.Fatalf("WaitForControlAPIReady: %v", err)
	}
	// Drain must not panic and must complete within the deadline.
	drainDone := make(chan struct{})
	go func() {
		stack.Drain(context.Background(), 5*time.Second)
		close(drainDone)
	}()
	select {
	case <-drainDone:
	case <-time.After(10 * time.Second):
		t.Fatal("Drain did not return within 10s")
	}
	// No role-failure should be pending in the happy-path drain.
	select {
	case rf := <-stack.FailCh():
		t.Fatalf("unexpected role failure after clean drain: role=%s err=%v", rf.Role, rf.Err)
	default:
	}
}

// TestMigrationsRunBeforeRunners is the load-bearing test for the
// @blessed-invariant: migrations-run-before-runners slug: the
// persistence driver's Migrate completes successfully BEFORE any
// role runner opens the database. If StartRoleStack returns
// without error AND the schema-migrations bookkeeping table exists
// at <runDir>/state.db, the verb's terminal-wait path is safe from
// the falsifier (the supervisor's first transaction hitting "no
// such table").
//
// The check is direct: open state.db with database/sql, query the
// schema_migrations table for at least one applied-row count.
func TestMigrationsRunBeforeRunners(t *testing.T) {
	runDir, endpoint, _ := setupRoleStackEnv(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stack, err := compose.StartRoleStack(ctx, silentLogger(), filepath.Join(runDir, "rimsky.yml"), endpoint)
	if err != nil {
		t.Fatalf("StartRoleStack: %v", err)
	}
	t.Cleanup(func() { stack.Drain(context.Background(), 5*time.Second) })

	// Open state.db directly with database/sql to verify the migrate
	// bookkeeping row landed BEFORE any runner could open the file.
	// The role runners' open path holds their own connection, but
	// sqlite permits a concurrent reader on the same file via a
	// separate connection — this read does not race the runners'
	// migrate (because migrate already ran to completion before
	// StartRoleStack returned).
	dbPath := filepath.Join(runDir, "state.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open(%q): %v", dbPath, err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM rimsky_migrations`).Scan(&n); err != nil {
		t.Fatalf("query rimsky_migrations: %v (state.db at %s)", err, dbPath)
	}
	if n == 0 {
		t.Fatal("rimsky_migrations has zero rows — migrate did not run before StartRoleStack returned")
	}
}

// TestWaitForControlAPIReady_Polls exercises the readiness poll
// against a stub server that returns 503 for the first stretch and
// flips to 200 once a hold expires. The flip happens well within
// the supplied deadline so the poll succeeds; a deadline shorter
// than the flip would surface the failure path instead. This pins
// the contract the verb depends on: "control-api ready" is the
// 200-on-health gate, not a process-state guess.
func TestWaitForControlAPIReady_Polls(t *testing.T) {
	var hits atomic.Int64
	flipAt := int64(3)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/health" {
			http.NotFound(w, r)
			return
		}
		if hits.Add(1) <= flipAt {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if err := compose.WaitForControlAPIReady(context.Background(), srv.URL, 2*time.Second); err != nil {
		t.Fatalf("WaitForControlAPIReady: %v", err)
	}
	if got := hits.Load(); got < flipAt {
		t.Errorf("poll-hits = %d, want at least %d (poll must have advanced past the 503 stretch)", got, flipAt)
	}
}

// TestWaitForControlAPIReady_DeadlineExceeded covers the falsifier
// surface: when the endpoint never returns 200, the poll surfaces
// a deadline-exceeded error rather than blocking forever.
func TestWaitForControlAPIReady_DeadlineExceeded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	err := compose.WaitForControlAPIReady(context.Background(), srv.URL, 150*time.Millisecond)
	if err == nil {
		t.Fatal("WaitForControlAPIReady must error when the endpoint never returns 200")
	}
}
