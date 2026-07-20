// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime_test

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	_ "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor"
	pgtest "github.com/rimsky-ai/rimsky-core/test/support/pgmigrate"
)

func TestSupervisor_StartShutdown(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)

	reg := locks.NewRegistry()

	supID := "test-sv-startshutdown"
	h, err := runtime.Start(runtime.Config{
		SupervisorID:      supID,
		Persist:           d.Tables(),
		Queue:             d.Queue(),
		AdvisoryLocker:    d.AdvisoryLocker(),
		Clock:             shared.SystemClock{},
		Logger:            shared.SilentLogger{},
		Concurrency:       1,
		LivenessInterval:  200 * time.Millisecond,
		ClaimPollInterval: 200 * time.Millisecond,
		Resolver:          executor.NewStaticResolver(map[string]executor.Endpoint{}),
		StoreRegistry:     reg,
		CallbackHost:      "127.0.0.1",
		CallbackPort:      0,
	})
	require.NoError(t, err)
	require.NotEmpty(t, h.CallbackAddr())

	var rec *persistence.SupervisorRow
	require.NoError(t, d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := d.Tables().Supervisors().Get(ctx, supID, tx)
		rec = r
		return err
	}))
	require.NotNil(t, rec)
	require.Equal(t, 1, rec.Concurrency)

	shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	require.NoError(t, h.Shutdown(shutdownCtx))
}

// @concept: supervisor
func TestSupervisor_ShutdownJoinsProducerVerbDispatcherBeforeReturning(t *testing.T) {
	t.Parallel()
	for i := 0; i < 10; i++ {
		i := i
		t.Run(fmt.Sprintf("iter-%d", i), func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			dbPath := filepath.Join(t.TempDir(), "rimsky.db")
			db, err := persistence.Open(ctx, persistence.Config{
				Driver: "sqlite", SQLite: &persistence.SQLiteConfig{Path: dbPath},
			})
			require.NoError(t, err)
			require.NoError(t, db.Migrate(ctx, shared.SilentLogger{}))

			h, err := runtime.Start(runtime.Config{
				SupervisorID:      fmt.Sprintf("test-sv-sync-shutdown-%d", i),
				Persist:           db.Tables(),
				Queue:             db.Queue(),
				AdvisoryLocker:    db.AdvisoryLocker(),
				Clock:             shared.SystemClock{},
				Logger:            shared.SilentLogger{},
				Concurrency:       1,
				LivenessInterval:  200 * time.Millisecond,
				ClaimPollInterval: 5 * time.Millisecond,
				Resolver:          executor.NewStaticResolver(map[string]executor.Endpoint{}),
				StoreRegistry:     locks.NewRegistry(),
				CallbackHost:      "127.0.0.1",
				CallbackPort:      0,
			})
			require.NoError(t, err)

			shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			require.NoError(t, h.Shutdown(shutdownCtx))

			require.NoError(t, db.Close(),
				"Shutdown must fully join its background goroutines before returning, "+
					"so closing the store immediately after is always safe")
		})
	}
}

func TestSupervisor_StartFailsFastOnWildcardBindWithoutAdvertise(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)

	_, err := runtime.Start(runtime.Config{
		SupervisorID:      "test-sv-wildcard-advertise",
		Persist:           d.Tables(),
		Queue:             d.Queue(),
		AdvisoryLocker:    d.AdvisoryLocker(),
		Clock:             shared.SystemClock{},
		Logger:            shared.SilentLogger{},
		Concurrency:       1,
		LivenessInterval:  200 * time.Millisecond,
		ClaimPollInterval: 200 * time.Millisecond,
		Resolver:          executor.NewStaticResolver(map[string]executor.Endpoint{}),
		StoreRegistry:     locks.NewRegistry(),
		CallbackHost:      "0.0.0.0",
		CallbackPort:      0,
	})
	require.Error(t, err,
		"a wildcard callback bind without an advertise host must refuse startup rather than stamping http://0.0.0.0 into callback URLs")
	require.Contains(t, err.Error(), "callback.advertise_host")
	require.Contains(t, err.Error(), "RIMSKY_SUPERVISOR_CALLBACK_ADVERTISE_HOST")

	var rec *persistence.SupervisorRow
	require.NoError(t, d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, gerr := d.Tables().Supervisors().Get(ctx, "test-sv-wildcard-advertise", tx)
		rec = r
		return gerr
	}))
	require.Nil(t, rec, "a supervisor refused at startup must not register itself")
}

func TestSupervisor_StartRequiresStoreRegistry(t *testing.T) {
	t.Parallel()
	_, err := runtime.Start(runtime.Config{
		SupervisorID: "test",
		Resolver:     executor.NewStaticResolver(map[string]executor.Endpoint{}),
	})
	require.Error(t, err)
}
