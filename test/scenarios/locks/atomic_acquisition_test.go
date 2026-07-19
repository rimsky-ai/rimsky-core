// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package locks

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks/storetest"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor"
	stubstore "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/testfixture"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestAtomicAcquisitionRollsBackOnOpenError(t *testing.T) {
	t.Parallel()

	endpoint, _, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
	})
	t.Cleanup(teardown)

	h := scenario.Start(t, scenario.HarnessOpts{
		NoSupervisor: true,
		ClaimProducers: config.RemoteClaimProducersConfig{
			ClaimProducers: map[string]config.ClaimProducerEntry{
				"content": {
					Endpoint:     "grpc://" + endpoint,
					Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
				},
			},
		},
	})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "open-error", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "worker", Executor: "stub"},
				scenario.WithClaimProducers(scenario.WriteClaimRef("content", "/region-A")),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-open-error", map[string]any{})

	n := h.FindNode(iid, "worker")
	require.NotNil(t, n)
	h.WaitForDispatch(n.ID)

	pool := executor.NewClientPool()
	t.Cleanup(func() { _ = pool.Close() })

	fake := storetest.NewFake("content", claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}})
	openErr := errOpenInjected{}
	fake.ErrorFunc = func(verb string, _ claimproducer.ClaimID) error {
		if verb == "open" {
			return openErr
		}
		return nil
	}
	reg := locks.NewRegistry()
	reg.Add("content", fake)

	args := runtime.RunArgs{
		Persist:                h.Persist,
		Queue:                  h.Queue,
		ClaimHandles:           h.Persist.ClaimHandles(),
		AdvisoryLocker:         h.Driver.AdvisoryLocker(),
		StoreRegistry:          reg,
		Clock:                  shared.SystemClock{},
		Logger:                 shared.SilentLogger{},
		SupervisorID:           "scenario-runner-rollback",
		AcceptedExecutors:      []string{"stub"},
		AcceptedClaimProducers: []string{"content"},
		Pool:                   pool,
		Resolver: executor.NewStaticResolver(map[string]executor.Endpoint{
			"stub": {Transport: "grpc", URL: h.StubAddr},
		}),
		LivenessInterval: 100 * time.Millisecond,
	}
	out, err := runtime.RunNode(h.Ctx, args, nil)
	require.Error(t, err, "Open error must surface")
	require.False(t, out.Ran,
		"acquisition tx must roll back when Open errors; runner advertises Ran=false")

	var lhCount int
	err = h.Pool.QueryRow(h.Ctx,
		`SELECT count(*) FROM rimsky_claim_handles WHERE holder_node_id = $1`, n.ID,
	).Scan(&lhCount)
	require.NoError(t, err)
	require.Equal(t, 0, lhCount, "rollback must leave no rimsky_claim_handles rows")

	var claimedBy *string
	err = h.Pool.QueryRow(h.Ctx,
		`SELECT claimed_by FROM rimsky_node_runs WHERE node_id = $1`, n.ID,
	).Scan(&claimedBy)
	require.NoError(t, err)
	require.Nil(t, claimedBy, "rollback must release the dispatch claim")

	calls := fake.Calls()
	openCount := 0
	for _, c := range calls {
		if c.Verb == "open" {
			openCount++
		}
	}
	require.Equal(t, 1, openCount,
		"Open should fire exactly once before the error rolls back the tx")
}

type errOpenInjected struct{}

func (errOpenInjected) Error() string { return "injected open error" }

func TestAtomicAcquisitionMultiSpec_SortedOrderAndAllOrNothingRollback(t *testing.T) {
	t.Parallel()

	endpoint, _, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
	})
	t.Cleanup(teardown)

	h := scenario.Start(t, scenario.HarnessOpts{
		NoSupervisor: true,
		ClaimProducers: config.RemoteClaimProducersConfig{
			ClaimProducers: map[string]config.ClaimProducerEntry{
				"content": {
					Endpoint:     "grpc://" + endpoint,
					Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
				},
			},
		},
	})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "multi-spec-sorted-rollback", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "worker", Executor: "stub"},
				scenario.WithClaimProducers(
					scenario.AliasedClaimRef("content", "/zzz-selector", "rw", "zzz"),
					scenario.AliasedClaimRef("content", "/aaa-selector", "rw", "aaa"),
				),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-multi-spec-sorted-rollback", map[string]any{})

	n := h.FindNode(iid, "worker")
	require.NotNil(t, n)
	h.WaitForDispatch(n.ID)

	pool := executor.NewClientPool()
	t.Cleanup(func() { _ = pool.Close() })

	fake := storetest.NewFake("content", claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}})
	openErr := errOpenInjected{}
	var openCount int32
	fake.ErrorFunc = func(verb string, _ claimproducer.ClaimID) error {
		if verb != "open" {
			return nil
		}
		if atomic.AddInt32(&openCount, 1) == 2 {
			return openErr
		}
		return nil
	}
	reg := locks.NewRegistry()
	reg.Add("content", fake)

	args := runtime.RunArgs{
		Persist:                h.Persist,
		Queue:                  h.Queue,
		ClaimHandles:           h.Persist.ClaimHandles(),
		AdvisoryLocker:         h.Driver.AdvisoryLocker(),
		StoreRegistry:          reg,
		Clock:                  shared.SystemClock{},
		Logger:                 shared.SilentLogger{},
		SupervisorID:           "scenario-runner-multi-rollback",
		AcceptedExecutors:      []string{"stub"},
		AcceptedClaimProducers: []string{"content"},
		Pool:                   pool,
		Resolver: executor.NewStaticResolver(map[string]executor.Endpoint{
			"stub": {Transport: "grpc", URL: h.StubAddr},
		}),
		LivenessInterval: 100 * time.Millisecond,
	}
	out, err := runtime.RunNode(h.Ctx, args, nil)
	require.Error(t, err, "the second spec's Open error must surface")
	require.False(t, out.Ran, "acquisition tx must roll back when the second spec's Open errors")

	calls := fake.Calls()
	var opens []storetest.FakeCall
	for _, c := range calls {
		if c.Verb == "open" {
			opens = append(opens, c)
		}
	}
	require.Len(t, opens, 2, "both specs must reach Open before the second one errors")
	require.Equal(t, "/aaa-selector", opens[0].Selector,
		"sortLockSpecs must order specs by (kind, producer:selector) ascending, so /aaa-selector "+
			"acquires before /zzz-selector even though /zzz-selector was declared first in the template")
	require.Equal(t, "/zzz-selector", opens[1].Selector)

	var lhCount int
	err = h.Pool.QueryRow(h.Ctx,
		`SELECT count(*) FROM rimsky_claim_handles WHERE holder_node_id = $1`, n.ID,
	).Scan(&lhCount)
	require.NoError(t, err)
	require.Equal(t, 0, lhCount,
		"multi-insert atomicity: the first spec's claim-handle insert (which the DB already committed "+
			"to this open transaction) must be rolled back too when the second spec's Open fails — "+
			"the worker-request claim and ALL claim-handle inserts must commit together or not at all")
}

func TestClaimHandleRowDeletedAfterTerminal(t *testing.T) {
	t.Parallel()

	endpoint, _, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
	})
	t.Cleanup(teardown)

	h := scenario.Start(t, scenario.HarnessOpts{
		ClaimProducers: config.RemoteClaimProducersConfig{
			ClaimProducers: map[string]config.ClaimProducerEntry{
				"content": {
					Endpoint:     "grpc://" + endpoint,
					Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
				},
			},
		},
	})
	h.Stub.WhenType("worker").Success(map[string]any{}, true, "scenario")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "release-after-terminal", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "worker", Executor: "stub"},
				scenario.WithClaimProducers(scenario.WriteClaimRef("content", "/region-B")),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-release-after-terminal", map[string]any{})

	n := h.FindNode(iid, "worker")
	require.NotNil(t, n)
	h.WaitForNodeState(n.ID, cascade.NodeStateFresh)

	deadline := time.Now().Add(2 * time.Second)
	var activeCount int
	for time.Now().Before(deadline) {
		err := h.Pool.QueryRow(h.Ctx,
			`SELECT count(*) FROM rimsky_claim_handles
			  WHERE holder_node_id = $1 AND state = 'active'`, n.ID,
		).Scan(&activeCount)
		require.NoError(t, err)
		if activeCount == 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	require.Equal(t, 0, activeCount,
		"after worker reaches fresh, zero ACTIVE lock-holder rows must remain (claimant-guarded release should have cleared them)")

	var nullHolderCount int
	err := h.Pool.QueryRow(h.Ctx,
		`SELECT count(*) FROM rimsky_claim_handles
		  WHERE holder_node_id = $1 AND holder_supervisor_id IS NOT NULL`, n.ID,
	).Scan(&nullHolderCount)
	require.NoError(t, err)
	require.Equal(t, 0, nullHolderCount,
		"after worker reaches fresh, every claim-handle row for the node must have holder_supervisor_id NULL")
}
