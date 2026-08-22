// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package scenarios

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/awaited"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

// @concept: frame
func TestFramesStartInTheArrivalOrderOfTheirMessages(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	release := make(chan struct{})
	h.Stub.WhenType("w").
		Success(map[string]any{"ok": true}, true, "w-1").HoldUntil(release).
		Then().Success(map[string]any{"ok": true}, true, "w-2").
		Then().Success(map[string]any{"ok": true}, true, "w-3")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "frames-in-arrival-order", Version: "1",
		Messages: []spec.MessageSchema{{Type: "test/wake"}},
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "w", Executor: "stub"},
				scenario.WithSubscribes(
					node.SubscriptionEntry{
						Node: "test/wake", Type: "terminal/success",
						ForceUpstreamRefresh: node.BoolPtr(false),
					},
				),
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"ok": map[string]any{"type": "boolean", "readOnly": true},
					},
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-frames-arrival-order", map[string]any{})

	first := h.PostInstanceMessage(iid, "test/wake", nil, "arrival-1")
	awaited.Until(t, "the first frame's dispatch to reach the executor and hold", func() bool {
		return h.Stub.Holding() >= 1
	})

	second := h.PostInstanceMessage(iid, "test/wake", nil, "arrival-2")
	third := h.PostInstanceMessage(iid, "test/wake", nil, "arrival-3")
	posted := []shared.UUID{first, second, third}

	close(release)
	h.WaitForSettledFrameCount(iid, 3)
	h.WaitForSchedulerQuiescence()

	var pickedUp []shared.UUID
	h.QuerySQL(`
		SELECT triggering_message_id
		  FROM rimsky_frames
		 WHERE instance_id = $1 AND triggering_message_id = ANY($2)
		 ORDER BY started_at ASC
	`, []any{iid, posted}, func(scan func(...any) error) error {
		var id shared.UUID
		if err := scan(&id); err != nil {
			return err
		}
		pickedUp = append(pickedUp, id)
		return nil
	})

	require.Equal(t, posted, pickedUp,
		"the frame engine picks up an instance's messages in arrival order, so the frames start in that order too")
}
