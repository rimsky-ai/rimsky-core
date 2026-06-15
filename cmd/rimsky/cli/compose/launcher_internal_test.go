// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// launcher_internal_test.go — package-internal coverage for the
// migrations-run-before-runners blessed invariant. The check is
// structural rather than post-condition based: it patches the
// startRoleStackFn seam with a fake that records the order in which
// MigratePersistence completed (i.e., the rimsky_migrations row
// landed) and startRoleStackFn was invoked, then asserts the migrate
// completion strictly precedes the runner-start call.
//
// Lives in package compose (not compose_test) because startRoleStackFn
// is unexported — the seam exists for this test, not as public API.
//
// @blessed-invariant: migrations-run-before-runners
package compose

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"

	// @deliberate: anonymous sqlite driver import so the structural
	// test can read rimsky_migrations directly via database/sql to
	// confirm the migrate ran before the fake startRoleStackFn fired.
	_ "modernc.org/sqlite"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	"github.com/rimsky-ai/rimsky-core/lib/control/launch"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/hostagent"
)

// TestMigratePersistence_CompletesBeforeStartRoleStack pins the
// @blessed-invariant: migrations-run-before-runners ordering
// structurally: the fake startRoleStackFn asserts the migration
// table is populated AT the moment it is called. If a future
// refactor reordered the calls (e.g., ran Migrate inside the
// unified-stack start), this test would observe an empty
// rimsky_migrations table at the runner-start callback and fail.
//
// The previous post-condition test (TestMigrationsRunBeforeRunners)
// only checks rimsky_migrations is populated AFTER StartRoleStack
// returns — which holds for several wrong orderings the BI's name
// forbids (e.g., Migrate run concurrently with the role start,
// completing first by sheer race-luck). The structural test rules
// those out.
func TestMigratePersistence_CompletesBeforeStartRoleStack(t *testing.T) {
	runDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(runDir, "blobs"), 0o755); err != nil {
		t.Fatalf("mkdir blobs: %v", err)
	}
	if err := WriteSyntheticRimskyYAML(runDir, &Manifest{Project: "test-bi"}, nil, nil); err != nil {
		t.Fatalf("WriteSyntheticRimskyYAML: %v", err)
	}
	if err := WriteSyntheticSupervisorYAMLWithCallbackPort(runDir, 0); err != nil {
		t.Fatalf("WriteSyntheticSupervisorYAMLWithCallbackPort: %v", err)
	}
	port, err := hostagent.FreeLocalPort()
	if err != nil {
		t.Fatalf("FreeLocalPort: %v", err)
	}
	endpoint := fmt.Sprintf("http://127.0.0.1:%d", port)
	t.Setenv("RIMSKY_CONFIG", filepath.Join(runDir, "rimsky.yml"))
	t.Setenv("RIMSKY_SUPERVISOR_CONFIG", filepath.Join(runDir, "supervisor.yml"))
	t.Setenv("RIMSKY_PROCESS_ROLE", "unified")
	t.Setenv("RIMSKY_CONTROL_API_HOST", "127.0.0.1")
	t.Setenv("RIMSKY_CONTROL_API_PORT", strconv.Itoa(port))
	t.Setenv("RIMSKY_METRICS_PORT", "")

	dbPath := filepath.Join(runDir, "state.db")

	origFn := startRoleStackFn
	defer func() { startRoleStackFn = origFn }()

	var migrationsTableSeen atomic.Bool
	var runnerStartCalled atomic.Int32
	// @deliberate: errFakeRunnerStart short-circuits StartRoleStack — the
	// real role runners do not need to boot, only to confirm migrate
	// completed first. The caller in StartRoleStack closes the driver and
	// surfaces this error.
	errFakeRunnerStart := errors.New("fake startRoleStackFn: synthetic stop")
	startRoleStackFn = func(ctx context.Context, logger *slog.Logger, driver persistence.Database, cfg *config.RimskyConfig) (*launch.UnifiedStack, error) {
		runnerStartCalled.Add(1)
		// @deliberate: read rimsky_migrations on a separate connection
		// INSIDE the runner-start callback to rule out the post-condition
		// race — a Migrate that runs concurrently would not have committed
		// by now unless it completed before this function was called.
		db, oerr := sql.Open("sqlite", dbPath)
		if oerr == nil {
			defer db.Close()
			var n int
			if qerr := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM rimsky_migrations`).Scan(&n); qerr == nil && n > 0 {
				migrationsTableSeen.Store(true)
			}
		}
		return nil, errFakeRunnerStart
	}

	logger := slog.New(slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, err = StartRoleStack(ctx, logger, filepath.Join(runDir, "rimsky.yml"), endpoint)
	if !errors.Is(err, errFakeRunnerStart) {
		t.Fatalf("StartRoleStack: got err=%v, want wrapped errFakeRunnerStart (fake runner-start was supposed to fire)", err)
	}
	if runnerStartCalled.Load() == 0 {
		t.Fatal("fake startRoleStackFn was never called — migrate must have failed before the runner-start seam was reached")
	}
	if !migrationsTableSeen.Load() {
		t.Fatal("rimsky_migrations was empty at the moment startRoleStackFn fired — migrate did NOT complete before runner-start (the @blessed-invariant: migrations-run-before-runners falsifier)")
	}
}
