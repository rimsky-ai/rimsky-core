// Scenario 3 — scheduled node fires when its `rimsky_schedules` row
// becomes due.
//
// Migrated to the stores-redesign template grammar (spec §11): the cron
// node is built via scenario.MakeNode. Schedule semantics are unchanged
// (see `core/scheduler/schedule_ticker.go`).
package scenarios

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/modeling/node"
	"github.com/fallguy/rimsky/modeling/scenario"
	"github.com/fallguy/rimsky/modeling/shared"
)

func TestScheduledNode(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("cron_job").Complete(map[string]any{"ran": true}, true, "ok")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "cron", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "cron_job",
					Executor: "stub",
					Schedule: "* * * * *",
				},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"ran": map[string]any{"type": "boolean"},
					},
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-cron", map[string]any{})

	n := h.FindNode(iid, "cron_job")
	require.NotNil(t, n)

	// Force the schedule's next_fire_at into the past so the ticker fires it
	// on the next tick without waiting for a real minute boundary.
	_, err := h.Pool.Exec(h.Ctx,
		`UPDATE rimsky_schedules SET next_fire_at = NOW() - INTERVAL '1 second' WHERE node_id = $1`,
		n.ID,
	)
	require.NoError(t, err)

	// Poll for the schedule_fired event (up to 20s). The scheduler tick
	// interval in the harness is 250ms.
	nid := n.ID
	deadline := time.Now().Add(20 * time.Second)
	var sawFired bool
	for time.Now().Before(deadline) {
		evs, err := h.Persist.Events().List(h.Ctx,
			persistence.EventListFilter{NodeID: &nid, Kind: "schedule_fired"},
			persistence.ListPagination{Limit: 10}, nil)
		require.NoError(t, err)
		if len(evs.Events) > 0 {
			sawFired = true
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	require.True(t, sawFired, "expected schedule_fired event")

	// Node should eventually return to fresh after the schedule-triggered
	// invalidate + re-run.
	require.True(t, h.WaitForNodeState(n.ID, shared.NodeStateFresh, 20*time.Second),
		"scheduled node did not reach fresh")
}
