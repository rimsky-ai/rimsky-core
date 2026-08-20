// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package frame_resolution

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/awaited"
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

	waitForFramesByState(t, h, iid, "running", 1)

	postInvalidateMessage(t, h, iid)

	awaited.Until(t, "the second wake message to accumulate on the instance's message queue while frame 1 runs", func() bool {
		var pending int
		h.QueryRowSQL(`SELECT count(*) FROM rimsky_messages WHERE instance_id = $1 AND delivered_at IS NULL AND cancelled = FALSE`,
			[]any{iid}, &pending)
		return pending == 1
	})
	require.Equal(t, 1, countFramesByState(t, h, iid, "running"),
		"only one frame may run at a time per instance")

	awaited.Until(t, "expected both frames completed", func() bool {
		return countFramesByState(t, h, iid, "completed") == 2
	})
}
