// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scenarios

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestUnresolvedExecutor(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{
		NoSupervisor: true,
		ExtraExecutors: map[string]executor.Endpoint{
			"does_not_exist_unknown": {Transport: "grpc", URL: "127.0.0.1:1"},
		},
	})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "ghost", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "ghost", Executor: "does_not_exist_unknown"}),
		},
	})
	iid := h.CreateInstance(tid, "ck-ghost", map[string]any{})

	n := h.FindNode(iid, "ghost")
	require.NotNil(t, n)

	pool := executor.NewClientPool()
	t.Cleanup(func() { _ = pool.Close() })

	_, err := h.Pool.Exec(h.Ctx,
		`UPDATE rimsky_node_runs
		    SET required_stores = '{}',
		        claimed_by = NULL,
		        claimed_at = NULL,
		        enqueued_at = NOW() - INTERVAL '5 seconds'
		  WHERE node_id = $1`,
		n.ID,
	)
	require.NoError(t, err)

	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		latest, err := h.Persist.Nodes().GetLatestRunForNode(h.Ctx, tx, n.ID)
		if err != nil {
			return err
		}
		return h.Persist.NodeAttributes().SetDispatchInputBag(h.Ctx, tx, latest.NodeRunID, n.ID, map[string]any{})
	}))

	args := runtime.RunArgs{
		Persist:           h.Persist,
		Queue:             h.Queue,
		ClaimHandles:      h.Persist.ClaimHandles(),
		AdvisoryLocker:    h.Driver.AdvisoryLocker(),
		StoreRegistry:     locks.NewRegistry(),
		Clock:             shared.SystemClock{},
		Logger:            shared.SilentLogger{},
		SupervisorID:      "scenario-runner",
		AcceptedExecutors: []string{"does_not_exist_unknown"},
		Pool:              pool,
		Resolver:          executor.NewStaticResolver(map[string]executor.Endpoint{}),
		LivenessInterval:  100 * time.Millisecond,
	}

	out, err := runtime.RunNode(h.Ctx, args, nil)
	require.NoError(t, err)
	require.True(t, out.Ran, "runner should commit acquisition for the candidate")

	var latest *persistence.NodeRunLatest
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Nodes().GetLatestRunForNode(h.Ctx, tx, n.ID)
		latest = r
		return err
	}))
	require.NotNil(t, latest)
	require.Equal(t, cascade.NodeStateFailed, latest.State)

	nid := n.ID
	var evs persistence.EventListResult
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Events().List(h.Ctx, persistence.EventListFilter{NodeID: &nid},
			persistence.ListPagination{Limit: 500}, tx)
		evs = r
		return err
	}))
	var (
		sawUnresolved          bool
		sawTerminalErrorSignal bool
	)
	for _, e := range evs.Events {
		switch e.KindRaw {
		case "unresolved_executor":
			sawUnresolved = true
		case "terminal/error/unresolved_executor":
			sawTerminalErrorSignal = true
		}
	}
	require.True(t, sawUnresolved, "expected unresolved_executor event")
	require.True(t, sawTerminalErrorSignal,
		"expected terminal/error/unresolved_executor signal row")
}

func TestUnresolvedExecutor_LateBindResolverMiss(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{NoSupervisor: true})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "ghost-late-bind", Version: "1",
		LateBindServices: []string{"ghost-late-bind-service"},
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "ghost", Executor: "ghost-late-bind-service"}),
		},
	})
	iid := h.CreateInstance(tid, "ck-ghost-late-bind", map[string]any{})

	n := h.FindNode(iid, "ghost")
	require.NotNil(t, n)

	pool := executor.NewClientPool()
	t.Cleanup(func() { _ = pool.Close() })

	_, err := h.Pool.Exec(h.Ctx,
		`UPDATE rimsky_instances SET service_bindings = '{"ghost-late-bind-service":{}}' WHERE id = $1`,
		iid,
	)
	require.NoError(t, err)

	_, err = h.Pool.Exec(h.Ctx,
		`UPDATE rimsky_node_runs
		    SET required_stores = '{}',
		        claimed_by = NULL,
		        claimed_at = NULL,
		        enqueued_at = NOW() - INTERVAL '5 seconds'
		  WHERE node_id = $1`,
		n.ID,
	)
	require.NoError(t, err)

	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		latest, err := h.Persist.Nodes().GetLatestRunForNode(h.Ctx, tx, n.ID)
		if err != nil {
			return err
		}
		return h.Persist.NodeAttributes().SetDispatchInputBag(h.Ctx, tx, latest.NodeRunID, n.ID, map[string]any{})
	}))

	lookupBindings := func(ctx context.Context, instanceID string) (map[string]json.RawMessage, bool, error) {
		var row *persistence.InstanceRow
		if txErr := h.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			id, perr := uuid.Parse(instanceID)
			if perr != nil {
				return perr
			}
			r, ierr := h.Persist.Instances().Get(ctx, id, tx)
			row = r
			return ierr
		}); txErr != nil {
			return nil, false, txErr
		}
		if row == nil || len(row.ServiceBindings) == 0 {
			return nil, false, nil
		}
		var bindings map[string]json.RawMessage
		if uerr := json.Unmarshal(row.ServiceBindings, &bindings); uerr != nil {
			return nil, false, uerr
		}
		return bindings, true, nil
	}
	staticResolver := executor.NewStaticResolver(map[string]executor.Endpoint{})
	resolver := executor.NewLateBindResolver(staticResolver, lookupBindings,
		map[string]string{"executor": "ghost-late-bind-proxy"})

	args := runtime.RunArgs{
		Persist:                h.Persist,
		Queue:                  h.Queue,
		ClaimHandles:           h.Persist.ClaimHandles(),
		AdvisoryLocker:         h.Driver.AdvisoryLocker(),
		StoreRegistry:          locks.NewRegistry(),
		Clock:                  shared.SystemClock{},
		Logger:                 shared.SilentLogger{},
		SupervisorID:           "scenario-runner",
		AcceptedExecutors:      []string{"ghost-late-bind-proxy"},
		LateBindServiceProxies: map[string]string{"executor": "ghost-late-bind-proxy"},
		Pool:                   pool,
		Resolver:               resolver,
		LivenessInterval:       100 * time.Millisecond,
	}

	out, err := runtime.RunNode(h.Ctx, args, nil)
	require.NoError(t, err)
	require.True(t, out.Ran, "runner should commit acquisition for the candidate")

	var latest *persistence.NodeRunLatest
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Nodes().GetLatestRunForNode(h.Ctx, tx, n.ID)
		latest = r
		return err
	}))
	require.NotNil(t, latest)
	require.Equal(t, cascade.NodeStateFailed, latest.State)

	nid := n.ID
	var evs persistence.EventListResult
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Events().List(h.Ctx, persistence.EventListFilter{NodeID: &nid},
			persistence.ListPagination{Limit: 500}, tx)
		evs = r
		return err
	}))
	var (
		sawUnresolved          bool
		sawTerminalErrorSignal bool
	)
	for _, e := range evs.Events {
		switch e.KindRaw {
		case "unresolved_executor":
			sawUnresolved = true
		case "terminal/error/unresolved_executor":
			sawTerminalErrorSignal = true
		}
	}
	require.True(t, sawUnresolved,
		"expected unresolved_executor event when the late-bind resolver's instance service_bindings "+
			"lack an entry for the node's executor name")
	require.True(t, sawTerminalErrorSignal,
		"expected terminal/error/unresolved_executor signal row")
}
