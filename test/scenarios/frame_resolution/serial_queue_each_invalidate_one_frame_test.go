// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package frame_resolution

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/awaited"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestSerialQueueEachInvalidateOneFrame(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("worker").Success(map[string]any{}, true, "ok")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "serial-queue-each-invalidate", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "worker", Executor: "stub"}),
		},
	})
	iid := h.CreateInstance(tid, "ck-serial-each", map[string]any{})
	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)

	const totalFrames = 10
	for i := 0; i < totalFrames-1; i++ {
		postInvalidateMessage(t, h, iid)
	}

	awaited.Until(t, fmt.Sprintf("all %d frames to reach the completed state", totalFrames), func() bool {
		return countFramesByState(t, h, iid, "completed") == totalFrames
	})

	frames := listFrames(t, h, iid)
	require.Len(t, frames, totalFrames, "expected exactly %d frames", totalFrames)
	seenTriggers := make(map[string]struct{}, totalFrames)
	for i, f := range frames {
		require.Equal(t, "completed", f.State, "frame %d not completed", i)
		require.NotEqual(t, (frameRow{}).TriggeringMessageID, f.TriggeringMessageID,
			"frame %d missing triggering_message_id", i)
		seenTriggers[f.TriggeringMessageID.String()] = struct{}{}
	}
	require.Equal(t, totalFrames, len(seenTriggers),
		"expected %d distinct triggering_message_id values; got %d", totalFrames, len(seenTriggers))
}
