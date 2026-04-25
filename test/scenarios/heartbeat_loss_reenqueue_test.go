// Scenario 12 — heartbeat loss: a node appears running with a stale
// last_heartbeat_at. The scheduler's sweep transitions it running→stale,
// emits heartbeat_lost, and re-enqueues.
package scenarios

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/scenario"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/storage"
)

func TestHeartbeatLossReenqueue(t *testing.T) {
	t.Parallel()
	// Disable the supervisor so it doesn't race us claiming the row we
	// manufacture.
	h := scenario.Start(t, scenario.HarnessOpts{NoSupervisor: true})

	// Minimal template + instance + manually-inserted running node.
	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "hb-loss", Version: "1",
		Nodes: []node.TemplateNodeDef{
			{Type: "worker", Executor: "stub"},
		},
	})
	iid := h.CreateInstance(tid, "ck-hb", map[string]any{})

	n := h.FindNode(iid, "worker")
	require.NotNil(t, n)

	// Force the node to running with a stale heartbeat (>>HeartbeatTimeout=5s).
	_, err := h.Pool.Exec(h.Ctx,
		`UPDATE rimsky_nodes
		   SET state = 'running',
		       last_heartbeat_at = NOW() - INTERVAL '30 seconds',
		       assigned_supervisor_id = 'zombie-sup'
		 WHERE id = $1`,
		n.ID,
	)
	require.NoError(t, err)

	// Remove any auto-enqueued dispatch row so we can observe re-enqueue.
	_, err = h.Pool.Exec(h.Ctx, `DELETE FROM rimsky_dispatch WHERE node_id = $1`, n.ID)
	require.NoError(t, err)

	// Scheduler's stale-heartbeat sweep fires on each tick. Wait for the
	// node to flip to stale and a dispatch row to reappear.
	deadline := time.Now().Add(25 * time.Second)
	var sawStale bool
	for time.Now().Before(deadline) {
		got, _ := h.Storage.Nodes().Get(h.Ctx, n.ID, nil)
		if got != nil && got.State == shared.NodeStateStale {
			sawStale = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	require.True(t, sawStale, "node did not transition running→stale")

	// Verify heartbeat_lost event.
	nid := n.ID
	evs, err := h.Storage.Events().List(h.Ctx, storage.EventListFilter{NodeID: &nid, Kind: "heartbeat_lost"},
		storage.ListPagination{Limit: 10}, nil)
	require.NoError(t, err)
	require.NotEmpty(t, evs.Events, "expected heartbeat_lost event")

	// A fresh dispatch row should exist (re-enqueued).
	var dispatchID uuid.UUID
	err = h.Pool.QueryRow(h.Ctx, `SELECT id FROM rimsky_dispatch WHERE node_id = $1`, n.ID).Scan(&dispatchID)
	require.NoError(t, err, "expected re-enqueued dispatch row")
}
