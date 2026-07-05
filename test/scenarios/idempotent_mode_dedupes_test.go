// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scenarios

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/test/support/executors/stub"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

// @story: idempotent-mode-dedupes
func TestIdempotentModeDedupes_QueueComparison(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	resumeAt := time.Now().Add(15 * time.Second)
	h.Stub.WhenType("a").Success(map[string]any{"x": "stable"}, true, "round-1")
	h.Stub.WhenType("b").Park(genv1.ParkReason_PARK_REASON_SNOOZE, "wait", resumeAt)

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "idempotent-queue-dedupes", Version: "1",
		Messages: []spec.MessageSchema{
			{Type: "test/wake"},
		},
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "a", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{
					Node: "test/wake", Type: "terminal/success",
					ForceUpstreamRefresh: node.BoolPtr(false),
				}),
				scenario.WithAttributes(map[string]any{
					"type":       "object",
					"properties": map[string]any{"x": map[string]any{"type": "string"}},
					"required":   []any{"x"},
				}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "b", Executor: "stub", CascadeMode: string(cascade.CascadeModeIdempotentQueue)},
				scenario.WithSubscribes(
					node.SubscriptionEntry{
						Node: "a", Type: "terminal/success",
						ForceUpstreamRefresh: node.BoolPtr(false),
					},
					node.SubscriptionEntry{
						Node: "a", Type: "attribute/x/changed",
						ForceUpstreamRefresh: node.BoolPtr(false),
					},
				),
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"snapshot_x": map[string]any{
							"type":   "string",
							"source": "{{nodes.a.attribute.x}}",
						},
					},
					"required": []any{"snapshot_x"},
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-idempotent-queue", map[string]any{})
	a := h.FindNode(iid, "a")
	b := h.FindNode(iid, "b")
	require.NotNil(t, a)
	require.NotNil(t, b)

	h.PostInstanceMessage(iid, "test/wake", nil, "round-1-wake")

	require.True(t, h.WaitForNodeState(b.ID, cascade.NodeStateParked, 30*time.Second),
		"b should park on round 1")

	bObs := func() []stub.ObservedRequest {
		var out []stub.ObservedRequest
		for _, o := range h.Stub.Observed() {
			if o.NodeType == "b" {
				out = append(out, o)
			}
		}
		return out
	}
	aObs := func() []stub.ObservedRequest {
		var out []stub.ObservedRequest
		for _, o := range h.Stub.Observed() {
			if o.NodeType == "a" {
				out = append(out, o)
			}
		}
		return out
	}

	for round := 2; round <= 4; round++ {
		h.PostInstanceMessage(iid, "test/wake", nil, fmt.Sprintf("round-%d-wake", round))
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			if len(aObs()) >= round {
				break
			}
			time.Sleep(150 * time.Millisecond)
		}
		require.GreaterOrEqual(t, len(aObs()), round, "a should run for each posted message round")
	}

	require.Equal(t, 1, len(bObs()), "b's executor must NOT be re-invoked while parked")

	h.Stub.WhenType("b").Success(map[string]any{}, true, "b-resumed")

	require.True(t, h.WaitForEventKind(b.ID, "parked_resume_started", 30*time.Second),
		"deadline sweep should wake the parked b-run")

	deadline := time.Now().Add(60 * time.Second)
	var observedAfter int
	for time.Now().Before(deadline) {
		observedAfter = len(bObs())
		if observedAfter >= 3 {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}

	stableDeadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(stableDeadline) {
		time.Sleep(150 * time.Millisecond)
	}

	observedAfter = len(bObs())
	require.Equal(t, 3, observedAfter,
		"under cascade_mode=idempotent-queue, identical-input cascade rounds must be dropped: "+
			"b should be invoked exactly three times (round-1 parked, round-1 deadline-resume, "+
			"and exactly ONE post-settle dispatch from the only un-deduped cascade), "+
			"NOT four post-settle dispatches")
}
