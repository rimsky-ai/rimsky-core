// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package locks

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/control/observability"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

// @story: named-lock-metric
func TestNamedLockAcquisitionMovesMetric(t *testing.T) {
	t.Parallel()

	const lockName = "deploy-mutex"
	namedLocksCfg := locks.NamedLocksConfig{Locks: map[string]locks.NamedLockConfig{lockName: {Limit: 1}}}

	h := scenario.Start(t, scenario.HarnessOpts{NoSupervisor: true, NamedLocks: namedLocksCfg})
	h.Stub.WhenType("worker").Success(map[string]any{}, true, "scenario")
	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "named-lock-metric", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "worker", Executor: "stub"},
				scenario.WithLocks(node.NodeLockRef{Name: lockName}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-named-lock-metric", map[string]any{})

	n := h.FindNode(iid, "worker")
	require.NotNil(t, n)
	h.WaitForDispatchCount(n.ID, 1)

	pool := executor.NewClientPool()
	t.Cleanup(func() { _ = pool.Close() })

	reg := observability.NewMetricsRegistry()
	before := testutil.ToFloat64(reg.NamedLockAcquisitions.WithLabelValues(lockName, "acquired"))

	args := runtime.RunArgs{
		Persist:               h.Persist,
		Queue:                 h.Queue,
		ClaimHandles:          h.Persist.ClaimHandles(),
		AdvisoryLocker:        h.Driver.AdvisoryLocker(),
		ClaimProducerRegistry: locks.NewRegistry(),
		NamedLocks:            namedLocksCfg,
		Clock:                 shared.SystemClock{},
		Logger:                shared.SilentLogger{},
		SupervisorID:          "scenario-runner-named-lock-metric",
		Pool:                  pool,
		Resolver: executor.NewStaticResolver(map[string]executor.Endpoint{
			"stub": {Transport: "grpc", URL: h.StubAddr},
		}),
		Metrics: observability.MetricsHookOf(reg),
	}
	out, err := runtime.RunNode(h.Ctx, args, nil)
	require.NoError(t, err)
	require.True(t, out.Ran, "runner must have acquired and run the named-lock node")

	after := testutil.ToFloat64(reg.NamedLockAcquisitions.WithLabelValues(lockName, "acquired"))
	require.Equal(t, before+1, after,
		"acquiring the named lock through the real acquire path must move the labeled counter")
}

// @story: named-lock-metric
func TestNamedLockContentionMovesUnavailableMetricDistinctFromClaimAcquisitions(t *testing.T) {
	t.Parallel()

	const lockName = "deploy-mutex-contended"
	namedLocksCfg := locks.NamedLocksConfig{Locks: map[string]locks.NamedLockConfig{lockName: {Limit: 1}}}

	h := scenario.Start(t, scenario.HarnessOpts{NoSupervisor: true, NamedLocks: namedLocksCfg})
	holdCh := make(chan struct{})
	h.Stub.WhenType("worker").Success(map[string]any{}, true, "scenario").HoldUntil(holdCh)

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "named-lock-contention-metric", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "worker", Executor: "stub"},
				scenario.WithLocks(node.NodeLockRef{Name: lockName}),
			),
		},
	})
	iidHolder := h.CreateInstance(tid, "ck-named-lock-contention-holder", map[string]any{})
	iidContender := h.CreateInstance(tid, "ck-named-lock-contention-contender", map[string]any{})

	holder := h.FindNode(iidHolder, "worker")
	contender := h.FindNode(iidContender, "worker")
	require.NotNil(t, holder)
	require.NotNil(t, contender)
	h.WaitForDispatchCount(holder.ID, 1)
	h.WaitForDispatchCount(contender.ID, 1)

	pool := executor.NewClientPool()
	t.Cleanup(func() { _ = pool.Close() })

	reg := observability.NewMetricsRegistry()
	beforeUnavailable := testutil.ToFloat64(reg.NamedLockAcquisitions.WithLabelValues(lockName, "unavailable"))
	beforeClaimAcquisitions := testutil.ToFloat64(reg.ClaimAcquisitions.WithLabelValues(lockName, "unavailable"))

	makeArgs := func(supID string) runtime.RunArgs {
		return runtime.RunArgs{
			Persist:               h.Persist,
			Queue:                 h.Queue,
			ClaimHandles:          h.Persist.ClaimHandles(),
			AdvisoryLocker:        h.Driver.AdvisoryLocker(),
			ClaimProducerRegistry: locks.NewRegistry(),
			NamedLocks:            namedLocksCfg,
			Clock:                 shared.SystemClock{},
			Logger:                shared.SilentLogger{},
			SupervisorID:          supID,
			Pool:                  pool,
			Resolver: executor.NewStaticResolver(map[string]executor.Endpoint{
				"stub": {Transport: "grpc", URL: h.StubAddr},
			}),
			Metrics: observability.MetricsHookOf(reg),
		}
	}

	holderDone := make(chan struct{})
	go func() {
		defer close(holderDone)
		out, err := runtime.RunNode(h.Ctx, makeArgs("scenario-runner-named-lock-holder"), nil)
		require.NoError(t, err)
		require.True(t, out.Ran, "holder must have acquired and run the named-lock node")
	}()

	h.WaitForNodeState(holder.ID, cascade.NodeStateRunning)

	out, err := runtime.RunNode(h.Ctx, makeArgs("scenario-runner-named-lock-contender"), nil)
	require.NoError(t, err)
	require.False(t, out.Ran, "contender must be refused the already-held named lock")

	close(holdCh)
	<-holderDone

	afterUnavailable := testutil.ToFloat64(reg.NamedLockAcquisitions.WithLabelValues(lockName, "unavailable"))
	require.Equal(t, beforeUnavailable+1, afterUnavailable,
		"a named-lock acquisition blocked by the configured limit must move the unavailable-labeled counter")

	afterClaimAcquisitions := testutil.ToFloat64(reg.ClaimAcquisitions.WithLabelValues(lockName, "unavailable"))
	require.Equal(t, beforeClaimAcquisitions, afterClaimAcquisitions,
		"named-lock contention must not move the producer-claim counter — the two are distinct series")
}
