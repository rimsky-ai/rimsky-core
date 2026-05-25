// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Minimal coverage of the supervisor Start/Shutdown lifecycle under
// the stores redesign.

package runtime_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fallguyconsulting/rimsky/foundation/locks"
	"github.com/fallguyconsulting/rimsky/foundation/persistence"
	"github.com/fallguyconsulting/rimsky/foundation/shared"
	"github.com/fallguyconsulting/rimsky/internal/pgtest"
	"github.com/fallguyconsulting/rimsky/runtime"
	"github.com/fallguyconsulting/rimsky/runtime/executor"
)

// TestSupervisor_StartShutdown verifies Start spins up a callback
// listener, registers the supervisor row, and Shutdown cleans up.
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
		HeartbeatInterval: 200 * time.Millisecond,
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

// TestSupervisor_StartRequiresStoreRegistry verifies the construction-
// time check rejects a missing StoreRegistry.
func TestSupervisor_StartRequiresStoreRegistry(t *testing.T) {
	t.Parallel()
	_, err := runtime.Start(runtime.Config{
		SupervisorID: "test",
		Resolver:     executor.NewStaticResolver(map[string]executor.Endpoint{}),
	})
	require.Error(t, err)
}
