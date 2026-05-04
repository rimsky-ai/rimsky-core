// Minimal coverage of integration.RunNode under the stores redesign.

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/foundation/integration"
	"github.com/fallguy/rimsky/foundation/internal/pgtest"
	"github.com/fallguy/rimsky/foundation/locks"
	"github.com/fallguy/rimsky/modeling/executor"
	"github.com/fallguy/rimsky/modeling/shared"
)

// TestRunNode_NoCandidate verifies the runner returns Ran=false with
// no error when there are no eligible dispatch rows. Drives the full
// acquireCandidate path against a real Postgres so the §7.3 candidate
// SELECT executes; an empty table is the expected baseline.
func TestRunNode_NoCandidate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)

	reg := locks.NewRegistry()

	clientPool := executor.NewClientPool()
	t.Cleanup(func() { _ = clientPool.Close() })

	args := integration.RunArgs{
		Persist:           d.Store(),
		Queue:             d.Queue(),
		AdvisoryLocker:    d.AdvisoryLocker(),
		LockHolders:       d.Store().LockHolders(),
		StoreRegistry:     reg,
		Clock:             shared.SystemClock{},
		Logger:            shared.SilentLogger{},
		SupervisorID:      "test-supervisor",
		AcceptedExecutors: []string{"stub"},
		Pool:              clientPool,
		Resolver:          executor.NewStaticResolver(map[string]executor.Endpoint{}),
		HeartbeatInterval: 100 * time.Millisecond,
	}

	res, err := integration.RunNode(ctx, args, nil)
	require.NoError(t, err)
	require.False(t, res.Ran, "no eligible candidate -> Ran=false")
}

// TestRunNode_ValidateRunArgs verifies the runner rejects construction
// with missing required dependencies. Pins the validateRunArgs check
// list — adding a new required field there should add an entry here.
func TestRunNode_ValidateRunArgs(t *testing.T) {
	t.Parallel()
	_, err := integration.RunNode(context.Background(), integration.RunArgs{}, nil)
	require.Error(t, err, "RunNode with empty RunArgs must reject")
}
