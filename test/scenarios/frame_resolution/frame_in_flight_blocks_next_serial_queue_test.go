// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package frame_resolution

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestFrameInFlightBlocksNextSerialQueue(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("worker").Success(map[string]any{}, true, "ok").Delay(3 * time.Second)

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "blocks-next", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "worker", Executor: "stub"}),
		},
	})
	iid := h.CreateInstance(tid, "ck-blocks-next", map[string]any{})
	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)

	require.True(t, waitForFramesByState(t, h, iid, "running", 1, 5*time.Second),
		"first frame did not enter running")

	postInvalidateMessage(t, h, iid)

	require.Eventually(t, func() bool {
		var pending int
		h.QueryRowSQL(`SELECT count(*) FROM rimsky_messages WHERE instance_id = $1 AND delivered_at IS NULL AND cancelled = FALSE`,
			[]any{iid}, &pending)
		return pending == 1
	}, 2*time.Second, 50*time.Millisecond,
		"second wake message did not accumulate on the instance's message queue while frame 1 is running")
	require.Equal(t, 1, countFramesByState(t, h, iid, "running"),
		"only one frame may run at a time per instance")

	require.Eventually(t, func() bool {
		return countFramesByState(t, h, iid, "completed") == 2
	}, 30*time.Second, 100*time.Millisecond,
		"expected both frames completed")
}
