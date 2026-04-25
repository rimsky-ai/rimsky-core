// Scenario 8 — commit twice, then invalidate with restore_version=previous;
// current_version swaps back to the prior version.
package scenarios

import (
	"bytes"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/scenario"
	"github.com/fallguy/rimsky/core/shared"
)

func TestRollbackViaRestoreVersion(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	h.Stub.WhenType("versioned").Complete(map[string]any{"rev": 1}, true, "v1")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "versioned", Version: "1",
		Nodes: []node.TemplateNodeDef{
			{
				Type: "versioned", Executor: "stub",
				OwnsResources: []node.ResourceDef{
					{Path: []string{"db", "{consumer_key}"}, Implementation: "inline-jsonb"},
				},
			},
		},
	})
	iid := h.CreateInstance(tid, "ck-rollback", map[string]any{})

	n := h.FindNode(iid, "versioned")
	require.NotNil(t, n)
	require.True(t, h.WaitForNodeState(n.ID, shared.NodeStateFresh, 15*time.Second))

	resources, err := h.Storage.Resources().ListByOwner(h.Ctx, n.ID, nil)
	require.NoError(t, err)
	require.Len(t, resources, 1)
	firstVersionID := resources[0].CurrentVersionID
	require.NotNil(t, firstVersionID)

	// Second commit with new payload.
	h.Stub.WhenType("versioned").Complete(map[string]any{"rev": 2}, true, "v2")
	require.NoError(t, h.Storage.Nodes().UpdateState(h.Ctx, n.ID,
		shared.NodeStateStale, node.ReasonOperatorInvalidate, nil))

	// Wait for second commit to complete by polling for version change.
	deadline := time.Now().Add(20 * time.Second)
	var secondVersionID *shared.UUID
	for time.Now().Before(deadline) {
		row, _ := h.Storage.Resources().Get(h.Ctx, resources[0].ID, nil)
		if row != nil && row.CurrentVersionID != nil && *row.CurrentVersionID != *firstVersionID {
			v := *row.CurrentVersionID
			secondVersionID = &v
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	require.NotNil(t, secondVersionID, "second commit did not advance current_version")

	// Invalidate with restore_version=previous.
	resp, err := http.Post(h.ControlBase+"/nodes/"+n.ID.String()+"/invalidate",
		"application/json", bytes.NewReader([]byte(`{"restore_version":"previous"}`)))
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// After restore, current_version should swap back to firstVersionID.
	deadline = time.Now().Add(10 * time.Second)
	var restored bool
	for time.Now().Before(deadline) {
		row, _ := h.Storage.Resources().Get(h.Ctx, resources[0].ID, nil)
		if row != nil && row.CurrentVersionID != nil && *row.CurrentVersionID == *firstVersionID {
			restored = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	require.True(t, restored, "restore_version=previous did not swap current_version back")
}
