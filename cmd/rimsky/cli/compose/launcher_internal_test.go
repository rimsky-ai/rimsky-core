// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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

	_ "modernc.org/sqlite"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	"github.com/rimsky-ai/rimsky-core/lib/control/launch"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/hostagent"
)

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
	errFakeRunnerStart := errors.New("fake startRoleStackFn: synthetic stop")
	startRoleStackFn = func(ctx context.Context, logger *slog.Logger, driver persistence.Database, cfg *config.RimskyConfig) (*launch.UnifiedStack, error) {
		runnerStartCalled.Add(1)
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
