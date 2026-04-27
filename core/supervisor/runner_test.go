// Minimal coverage of supervisor.RunNode under stores-redesign-v2.
//
// The pre-v2 runner_test.go covered the omnibus runner against the old
// AcquireLock/OpenHandle/ReleaseLock surface; under v2 the runner runs
// candidate selection -> acquisition tx -> Open/Commit/Abandon directly.
// This file keeps the package buildable and pins the small surfaces that
// don't require a full scenario harness: validateRunArgs error paths,
// and a no-candidate run against a real Postgres + stub store registry.

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

// TestRunNode_NoCandidate verifies the runner returns Ran=false with
// no error when there are no eligible dispatch rows. Drives the full
// acquireCandidate path against a real Postgres so the §13.3 candidate
// SELECT executes; an empty table is the expected baseline.
func TestRunNode_NoCandidate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool, teardown := pgtest.StartPostgres(ctx, t)
	t.Cleanup(teardown)

	backend := pgstorage.New(pool)
	q := pgqueue.New(pool)
	reg := store.NewRegistry()
	reg.Register(stub.FilesystemFactory())

	clientPool := executor.NewClientPool()
	t.Cleanup(func() { _ = clientPool.Close() })

	args := supervisor.RunArgs{
		Storage:           backend,
		Queue:             q,
		QueuePool:         pool,
		LockHolders:       store.NewLockHoldersClient(pool),
		StoreRegistry:     reg,
		Clock:             shared.SystemClock{},
		Logger:            shared.SilentLogger{},
		SupervisorID:      "test-supervisor",
		AcceptedExecutors: []string{"stub"},
		Pool:              clientPool,
		Resolver:          executor.NewStaticResolver(map[string]executor.Endpoint{}),
		HeartbeatInterval: 100 * time.Millisecond,
	}

	res, err := supervisor.RunNode(ctx, args, nil)
	require.NoError(t, err)
	require.False(t, res.Ran, "no eligible candidate -> Ran=false")
}

// TestRunNode_ValidateRunArgs verifies the runner rejects construction
// with missing required dependencies. Pins the validateRunArgs check
// list — adding a new required field there should add an entry here.
func TestRunNode_ValidateRunArgs(t *testing.T) {
	t.Parallel()
	_, err := supervisor.RunNode(context.Background(), supervisor.RunArgs{}, nil)
	require.Error(t, err, "RunNode with empty RunArgs must reject")
}
