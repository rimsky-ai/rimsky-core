// Scenario 13 — orphaned claim: a dispatch row is held by a dead supervisor
// past the orphan cutoff while its node is still stale. Scheduler's orphan
// sweep releases the claim and emits orphaned_claim_released.
package scenarios

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/scenario"
	"github.com/fallguy/rimsky/core/storage"
)

func TestOrphanedClaim(t *testing.T) {
	t.Parallel()
	// NoSupervisor so our manufactured claim isn't fought over.
	h := scenario.Start(t, scenario.HarnessOpts{NoSupervisor: true})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "orphan", Version: "1",
		Nodes: []node.TemplateNodeDef{
			{Type: "worker", Executor: "stub"},
		},
	})
	iid := h.CreateInstance(tid, "ck-orphan", map[string]any{})
	n := h.FindNode(iid, "worker")
	require.NotNil(t, n)

	// Replace the auto-enqueued dispatch row with one claimed far in the
	// past by a dead supervisor. OrphanedClaimTimeout defaults to 25s in
	// the harness; push claimed_at well past that.
	_, err := h.Pool.Exec(h.Ctx, `DELETE FROM rimsky_dispatch WHERE node_id = $1`, n.ID)
	require.NoError(t, err)
	_, err = h.Pool.Exec(h.Ctx,
		`INSERT INTO rimsky_dispatch (id, node_id, executor_name, concurrency_tags, enqueued_at, claimed_by, claimed_at)
		 VALUES ($1, $2, 'stub', '{}', NOW() - INTERVAL '2 minutes', 'dead-supervisor', NOW() - INTERVAL '2 minutes')`,
		uuid.New(), n.ID,
	)
	require.NoError(t, err)

	// Wait for scheduler's orphan-claim sweep to release the claim.
	deadline := time.Now().Add(30 * time.Second)
	var released bool
	for time.Now().Before(deadline) {
		var claimedBy *string
		err := h.Pool.QueryRow(h.Ctx, `SELECT claimed_by FROM rimsky_dispatch WHERE node_id = $1`, n.ID).Scan(&claimedBy)
		if err == nil && claimedBy == nil {
			released = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	require.True(t, released, "orphaned claim was not released")

	nid := n.ID
	evs, err := h.Storage.Events().List(h.Ctx,
		storage.EventListFilter{NodeID: &nid, Kind: "orphaned_claim_released"},
		storage.ListPagination{Limit: 10}, nil)
	require.NoError(t, err)
	require.NotEmpty(t, evs.Events, "expected orphaned_claim_released event")
}
