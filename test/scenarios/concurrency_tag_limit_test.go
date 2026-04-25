// Scenario 16 — concurrency tag limits bound simultaneous claims. The
// harness's supervisor hardcodes an empty limits map, so we exercise the
// queue directly: two enqueued nodes with the same tag, Claim with
// limit "slot:foo":1 — only one claim succeeds until the first is Complete.
package scenarios

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/queue"
	"github.com/fallguy/rimsky/core/scenario"
	"github.com/fallguy/rimsky/core/shared"
)

func TestConcurrencyTagLimit(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{NoSupervisor: true, NoScheduler: true})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "tag-limit", Version: "1",
		Nodes: []node.TemplateNodeDef{
			{Type: "a", Executor: "stub", ConcurrencyTags: []string{"slot:foo"}},
			{Type: "b", Executor: "stub", ConcurrencyTags: []string{"slot:foo"}},
		},
	})
	iid := h.CreateInstance(tid, "ck-tag", map[string]any{})

	a := h.FindNode(iid, "a")
	b := h.FindNode(iid, "b")
	require.NotNil(t, a)
	require.NotNil(t, b)

	// Wipe any auto-enqueued rows and re-enqueue explicitly with tags.
	_, err := h.Pool.Exec(h.Ctx, `DELETE FROM rimsky_dispatch WHERE node_id IN ($1,$2)`, a.ID, b.ID)
	require.NoError(t, err)
	for _, id := range []shared.UUID{a.ID, b.ID} {
		require.NoError(t, h.Queue.Enqueue(h.Ctx, queue.DispatchRequest{
			NodeID:          id,
			ExecutorName:    "stub",
			ConcurrencyTags: []string{"slot:foo"},
			EnqueuedAt:      timeNow(),
		}))
	}

	limits := map[string]int{"slot:foo": 1}

	// First claim succeeds.
	row1, err := h.Queue.Claim(h.Ctx, "sup-1", []string{"stub"}, limits)
	require.NoError(t, err)
	require.NotNil(t, row1, "first claim should succeed")

	// Second claim is tag-blocked → nil.
	row2, err := h.Queue.Claim(h.Ctx, "sup-2", []string{"stub"}, limits)
	require.NoError(t, err)
	require.Nil(t, row2, "second claim should be tag-blocked")

	// Complete the first; the second claim now succeeds.
	require.NoError(t, h.Queue.Complete(h.Ctx, row1.ID, "sup-1"))

	row3, err := h.Queue.Claim(h.Ctx, "sup-2", []string{"stub"}, limits)
	require.NoError(t, err)
	require.NotNil(t, row3, "second claim should succeed after first is released")
}
