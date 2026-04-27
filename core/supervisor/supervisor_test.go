// Minimal coverage of the supervisor Start/Shutdown lifecycle under
// stores-redesign-v2.

package supervisor_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/executor"
	"github.com/fallguy/rimsky/core/internal/pgtest"
	pgqueue "github.com/fallguy/rimsky/core/queue/postgres"
	"github.com/fallguy/rimsky/core/shared"
	pgstorage "github.com/fallguy/rimsky/core/storage/postgres"
	"github.com/fallguy/rimsky/core/store"
	"github.com/fallguy/rimsky/core/store/stub"
	"github.com/fallguy/rimsky/core/supervisor"
)

// TestSupervisor_StartShutdown verifies Start spins up a callback
// listener, registers the supervisor row, and Shutdown cleans up.
func TestSupervisor_StartShutdown(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool, teardown := pgtest.StartPostgres(ctx, t)
	t.Cleanup(teardown)

	backend := pgstorage.New(pool)
	q := pgqueue.New(pool)
	reg := store.NewRegistry()
	reg.Register(stub.FilesystemFactory())

	supID := "test-sv-startshutdown"
	h, err := supervisor.Start(supervisor.Config{
		SupervisorID:      supID,
		Storage:           backend,
		Queue:             q,
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

	rec, err := backend.Supervisors().Get(ctx, supID, nil)
	require.NoError(t, err)
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
	_, err := supervisor.Start(supervisor.Config{
		SupervisorID: "test",
		Storage:      nil,
		Queue:        nil,
		Resolver:     executor.NewStaticResolver(map[string]executor.Endpoint{}),
	})
	require.Error(t, err)
}
