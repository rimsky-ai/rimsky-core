// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor"
	"github.com/rimsky-ai/rimsky-core/test/support/pgdbtest"
)

func TestRunNode_NoCandidate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgdbtest.OpenDriver(ctx, t)

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
		LivenessInterval:  100 * time.Millisecond,
	}

	res, err := runtime.RunNode(ctx, args, nil)
	require.NoError(t, err)
	require.False(t, res.Ran, "no eligible candidate -> Ran=false")
}

func TestRunNode_ValidateRunArgs(t *testing.T) {
	t.Parallel()
	_, err := runtime.RunNode(context.Background(), runtime.RunArgs{}, nil)
	require.Error(t, err, "RunNode with empty RunArgs must reject")
}
