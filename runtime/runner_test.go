// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Minimal coverage of runtime.RunNode under the stores redesign.

package runtime_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/foundation/locks"
	"github.com/fallguy/rimsky/foundation/shared"
	"github.com/fallguy/rimsky/internal/pgtest"
	"github.com/fallguy/rimsky/runtime"
	"github.com/fallguy/rimsky/runtime/executor"
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

	args := runtime.RunArgs{
		Persist:           d.Tables(),
		Queue:             d.Queue(),
		AdvisoryLocker:    d.AdvisoryLocker(),
		ClaimHandles:      d.Tables().ClaimHandles(),
		StoreRegistry:     reg,
		Clock:             shared.SystemClock{},
		Logger:            shared.SilentLogger{},
		SupervisorID:      "test-supervisor",
		AcceptedExecutors: []string{"stub"},
		Pool:              clientPool,
		Resolver:          executor.NewStaticResolver(map[string]executor.Endpoint{}),
		HeartbeatInterval: 100 * time.Millisecond,
	}

	res, err := runtime.RunNode(ctx, args, nil)
	require.NoError(t, err)
	require.False(t, res.Ran, "no eligible candidate -> Ran=false")
}

// TestRunNode_ValidateRunArgs verifies the runner rejects construction
// with missing required dependencies. Pins the validateRunArgs check
// list — adding a new required field there should add an entry here.
func TestRunNode_ValidateRunArgs(t *testing.T) {
	t.Parallel()
	_, err := runtime.RunNode(context.Background(), runtime.RunArgs{}, nil)
	require.Error(t, err, "RunNode with empty RunArgs must reject")
}
