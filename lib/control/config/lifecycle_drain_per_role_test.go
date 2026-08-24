// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package config

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/lifecycle"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor"
	"github.com/rimsky-ai/rimsky-core/test/support/awaited"
)

func openDrainDatabase(t *testing.T) persistence.Database {
	t.Helper()
	ctx := context.Background()
	db, err := persistence.Open(ctx, persistence.Config{
		Driver: "sqlite",
		SQLite: &persistence.SQLiteConfig{Path: filepath.Join(t.TempDir(), "drain.db")},
	})
	require.NoError(t, err)
	require.NoError(t, db.Migrate(ctx, shared.SilentLogger{}))
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func stageRowForUnregisteredService(t *testing.T, tables persistence.Tables, hash string) {
	t.Helper()
	ctx := context.Background()
	sp := spec.TemplateSpec{
		Name: "drain-per-role", Version: "v1",
		Nodes: []spec.TemplateNodeDef{{Type: "n1", Executor: "ghost"}},
	}
	require.NoError(t, tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return lifecycle.StageTemplateEvent(ctx, tables.LifecycleOutbox(),
			lifecycle.EventTemplateRegistered, hash, sp, lifecycle.TemplatePayload{}, tx)
	}))
	require.Len(t, pendingTemplateRows(t, tables, hash), 1, "the row is owed before the role starts")
}

func pendingTemplateRows(t *testing.T, tables persistence.Tables, hash string) []persistence.LifecycleOutboxRow {
	t.Helper()
	ctx := context.Background()
	var rows []persistence.LifecycleOutboxRow
	require.NoError(t, tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := tables.LifecycleOutbox().ListPendingForScope(ctx,
			persistence.LifecycleScopeTemplate, hash, tx)
		rows = r
		return err
	}))
	return rows
}

func shutdownWithin(t *testing.T, shutdown func(context.Context) error) {
	t.Helper()
	t.Cleanup(func() {
		//nolint:testwallclock-pacing the shutdown grace is not a verdict input; no assertion reads it
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = shutdown(ctx)
	})
}

// @decision: lifecycle-drain-per-role
func TestSchedulerRoleDrainsTheLifecycleOutbox(t *testing.T) {
	t.Parallel()
	db := openDrainDatabase(t)
	hash := "sha256-scheduler-drain"
	stageRowForUnregisteredService(t, db.Tables(), hash)

	h, err := StartScheduler(SchedulerConfig{
		Driver:       db,
		Clock:        shared.SystemClock{},
		Logger:       shared.SilentLogger{},
		TickInterval: 250 * time.Millisecond,
		SupervisorID: "scheduler-drain",
	})
	require.NoError(t, err)
	shutdownWithin(t, h.Shutdown)

	awaited.Until(t, "the scheduler role's own drain to take the row, with no other role running",
		func() bool { return len(pendingTemplateRows(t, db.Tables(), hash)) == 0 })
}

// @decision: lifecycle-drain-per-role
func TestSupervisorRoleDrainsTheLifecycleOutbox(t *testing.T) {
	t.Parallel()
	db := openDrainDatabase(t)
	hash := "sha256-supervisor-drain"
	stageRowForUnregisteredService(t, db.Tables(), hash)

	h, err := StartSupervisor(SupervisorConfig{
		SupervisorID: "supervisor-drain",
		Driver:       db,
		Clock:        shared.SystemClock{},
		Logger:       shared.SilentLogger{},
		Resolver:     executor.NewStaticResolver(nil),
		CallbackHost: "127.0.0.1",
		CallbackPort: 0,
	})
	require.NoError(t, err)
	shutdownWithin(t, h.Shutdown)

	awaited.Until(t, "the supervisor role's own drain to take the row, with no other role running",
		func() bool { return len(pendingTemplateRows(t, db.Tables(), hash)) == 0 })
}

// @decision: lifecycle-drain-per-role
func TestControlAPIRoleDrainsTheLifecycleOutbox(t *testing.T) {
	t.Parallel()
	db := openDrainDatabase(t)
	hash := "sha256-control-api-drain"
	stageRowForUnregisteredService(t, db.Tables(), hash)

	h, err := StartControlAPI(ControlAPIConfig{
		Driver: db,
		Clock:  shared.SystemClock{},
		Logger: shared.SilentLogger{},
		Host:   "127.0.0.1",
		Port:   0,
	})
	require.NoError(t, err)
	shutdownWithin(t, h.Shutdown)

	awaited.Until(t, "the control-api role's own drain to take the row, with no other role running",
		func() bool { return len(pendingTemplateRows(t, db.Tables(), hash)) == 0 })
}
