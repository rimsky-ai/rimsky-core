// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Task 39 — frame_coalesce_self_invalidate (post-2026-05-14 model).
//
// Single-node template with a self-subscription:
//
//	subscribes:
//	  - { node: self-type, type: terminal/success, when: payload.changed, frame: next }
//
// and frame_resolution_mode: coalesce. The self-cycle is the
// post-2026-05-14 replacement for the retired
// `invalidate: { targets: [self], frame: next }` send-side syntax;
// the receiver-side subscription expresses the same intent ("re-fire
// me on every fresh_changed commit") and is now permitted at the
// validator + cascade-walker level (see graph/node/template_validator.go
// `TestValidateSubscribes_SelfWithFrameNextOK` and
// runtime/runner_terminal.go::cascadeSubscribersStaleInTx's FrameNext
// branch).
//
// Asserts:
//   - the node re-fires after each fresh_changed commit (Observed()
//     count grows beyond 1 within a short window);
//   - the cycle terminates cleanly when the stub flips to changed=false
//     (no more frames enqueue, the trailing pending frame coalesces
//     into a single follow-up dispatch — verified by checking the
//     total dispatch count is bounded after the cutoff).
package scenarios

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestFrameCoalesceSelfInvalidate(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	// @deliberate: Initial stub state: changed=true. Each dispatch produces
	// fresh_changed, the self-subscription opens a next-frame, the
	// supervisor re-dispatches. The loop continues until we flip the
	// stub to changed=false below.
	h.Stub.WhenType("drainer").Success(map[string]any{"k": 1}, true, "first")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "frame-coalesce-self-cycle", Version: "1",
		FrameResolutionMode: node.FrameResolutionCoalesce,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "drainer", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{
					Node: "drainer", Type: "terminal/success",
					When:                 "payload.changed",
					Frame:                "next",
					WakeOnChange:         node.BoolPtr(true),  // today-equivalent
					ForceUpstreamRefresh: node.BoolPtr(false), // today-equivalent
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-frame-coalesce-self", map[string]any{})
	d := h.FindNode(iid, "drainer")
	require.NotNil(t, d)

	// @deliberate: Let the self-cycle iterate a few times. With coalesce
	// frame_resolution_mode + a steady changed=true, each commit
	// queues a single trailing frame; rapid commits collapse rather
	// than fanning out.
	require.Eventually(t, func() bool {
		return len(h.Stub.Observed()) >= 3
	}, 5*time.Second, 25*time.Millisecond, "expected >=3 dispatches from the self-cycle loop")

	// @deliberate: Flip the stub to changed=false. The next commit settles as
	// fresh_unchanged; the cascade walker's `outcome: fresh_changed`
	// filter means the self-subscription does NOT fire, and the loop
	// terminates.
	h.Stub.WhenType("drainer").Success(map[string]any{"k": 2}, false, "settled")

	require.True(t, h.WaitForNodeState(d.ID, cascade.NodeStateFresh, 30*time.Second),
		"node should reach fresh and stay there after stub flips to changed=false")

	// @deliberate: Record the dispatch count once the loop has terminated, then
	// confirm it stops growing (coalesce semantics: at most one
	// trailing frame pending at any time, so the count stabilizes
	// quickly after the changed=false flip).
	count := len(h.Stub.Observed())
	time.Sleep(500 * time.Millisecond)
	require.LessOrEqual(t, len(h.Stub.Observed())-count, 2,
		"after stub flips to changed=false the dispatch count must stabilize "+
			"(coalesce semantics permit at most one trailing pending frame)")
}
