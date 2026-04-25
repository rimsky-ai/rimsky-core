// Scenario 11 — node declares an executor that is not registered; runner
// emits unresolved_executor and policy (defaulting to give_up via unknown
// class) transitions the node to failed.
package scenarios

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/scenario"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/storage"
)

func TestUnresolvedExecutor(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "ghost", Version: "1",
		Nodes: []node.TemplateNodeDef{
			{Type: "ghost", Executor: "does_not_exist"},
		},
	})
	iid := h.CreateInstance(tid, "ck-ghost", map[string]any{})

	n := h.FindNode(iid, "ghost")
	require.NotNil(t, n)

	// Supervisor's accept list does not include "does_not_exist", so Claim
	// will not pick up the dispatch row. Instead, an inspection of supervisor
	// behavior: with no policy registered, unknown_error_class → give_up →
	// failed — but only after the runner actually picks up the node.
	//
	// Supervisor's accepted list is derived from its Resolver, which here is
	// only the stub. The dispatch row's executor_name = "does_not_exist"
	// cannot be claimed. So the node will sit in stale indefinitely.
	//
	// To exercise unresolved_executor, we need a runner whose accept list
	// contains the ghost executor name but whose resolver does not have it.
	// The harness's resolver already registers "stub" and "testexec"; we
	// instead add the executor name to the dispatch but ensure a matching
	// accept. Simplest: directly invoke runner via a dispatch with matching
	// executor name. Since that requires scaffolding, take the alternate
	// route: simulate the outcome by manually inserting a dispatch row with
	// executor_name="stub" (so supervisor claims it) but change the node's
	// executor column to a missing name before the runner does its lookup.

	// Update the node's executor to an unregistered name AND enqueue a
	// matching dispatch so the supervisor (accepting stub/testexec) never
	// picks it up — timeout expected.
	//
	// Because the harness's supervisor only accepts what its Resolver knows
	// about, an unresolvable executor can't be routed through the harness's
	// supervisor. We adapt: pre-seed a dispatch with executor_name="stub" so
	// supervisor claims it, but the node's stored executor is still the
	// unknown one — the runner's Resolve(nd.Executor) call will miss.
	_, err := h.Pool.Exec(h.Ctx,
		`UPDATE rimsky_nodes SET executor = $1 WHERE id = $2`,
		"does_not_exist_unknown", n.ID,
	)
	require.NoError(t, err)
	// Insert a dispatch row with executor_name the supervisor accepts
	// ("stub") so it gets claimed.
	_, err = h.Pool.Exec(h.Ctx,
		`INSERT INTO rimsky_dispatch (id, node_id, executor_name, concurrency_tags, enqueued_at)
		 VALUES (gen_random_uuid(), $1, 'stub', '{}', NOW())
		 ON CONFLICT (node_id) DO UPDATE
		   SET executor_name = 'stub', enqueued_at = NOW()`,
		n.ID,
	)
	require.NoError(t, err)

	// Direct stale → failed via ReasonDispatchImpossible. No policy chain —
	// the node never enters running.
	require.True(t, h.WaitForNodeState(n.ID, shared.NodeStateFailed, 30*time.Second),
		"ghost did not reach failed")

	// Verify event trail: unresolved_executor followed by an error event
	// with action_taken = "dispatch_impossible". The node must NOT have
	// transitioned through running — the runner's stale→running transition
	// for the unresolved case is gone.
	nid := n.ID
	evs, err := h.Storage.Events().List(h.Ctx, storage.EventListFilter{NodeID: &nid},
		storage.ListPagination{Limit: 500}, nil)
	require.NoError(t, err)
	var (
		sawUE             bool
		sawDispatchImposs bool
		sawWorkStarted    bool
	)
	for _, e := range evs.Events {
		switch e.Kind {
		case "unresolved_executor":
			sawUE = true
		case "error":
			if action, _ := e.Payload["action_taken"].(string); action == "dispatch_impossible" {
				if cls, _ := e.Payload["error_class"].(string); cls == "unresolved_executor" {
					sawDispatchImposs = true
				}
			}
		case "work_started":
			sawWorkStarted = true
		}
	}
	require.True(t, sawUE, "expected unresolved_executor event")
	require.True(t, sawDispatchImposs, "expected error event with action_taken=dispatch_impossible")
	require.False(t, sawWorkStarted, "node must not transition through running on unresolved_executor")
}
