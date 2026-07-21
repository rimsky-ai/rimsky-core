// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scenarios

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestNoOpCommit(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	h.Stub.WhenType("producer").Success(map[string]any{"x": 1}, true, "initial")
	h.Stub.WhenType("dependent").Success(map[string]any{"y": 2}, true, "downstream")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "noop", Version: "1",
		Messages: []spec.MessageSchema{
			{Type: "test/wake/producer"},
		},
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "producer", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{
					Node: "test/wake/producer", Type: "terminal/success",
					ForceUpstreamRefresh: node.BoolPtr(false),
				}),
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"x": map[string]any{"type": "integer"},
					},
				}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "dependent",
					Executor: "stub",
				},
				scenario.WithSubscribes(node.SubscriptionEntry{Node: "producer", Type: "terminal/*", ForceUpstreamRefresh: node.BoolPtr(false)}),
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"y": map[string]any{"type": "integer"},
					},
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-noop", map[string]any{})

	producer := h.FindNode(iid, "producer")
	dep := h.FindNode(iid, "dependent")
	require.NotNil(t, producer)
	require.NotNil(t, dep)
	h.PostInstanceMessage(iid, "test/wake/producer", nil, fmt.Sprintf("test-wake-%s-init", t.Name()))

	h.WaitForNodeState(producer.ID, cascade.NodeStateFresh)
	h.WaitForNodeState(dep.ID, cascade.NodeStateFresh)

	h.Stub.WhenType("producer").Success(map[string]any{"x": 1}, false, "noop")

	pid := producer.ID
	priorCount := h.EventCount(pid, "terminal/success")
	depPriorCount := h.EventCount(dep.ID, "terminal/success")

	h.PostInstanceMessage(iid, "test/wake/producer", nil, fmt.Sprintf("test-wake-%s-1", t.Name()))

	h.WaitForEventCount(pid, "terminal/success", priorCount+1)

	var allCommitted persistence.EventListResult
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Events().List(h.Ctx,
			persistence.EventListFilter{NodeID: &pid, KindIn: []string{"terminal/success"}},
			persistence.ListPagination{Limit: 200}, tx)
		allCommitted = r
		return err
	}))
	require.Greater(t, len(allCommitted.Events), priorCount,
		"expected a new terminal/success after changed=false run")
	latest := allCommitted.Events[0]
	changedVal, ok := latest.Payload["changed"].(bool)
	require.True(t, ok, "second run's terminal/success payload must carry a boolean changed field, got %#v", latest.Payload["changed"])
	require.False(t, changedVal,
		"second run's terminal/success must carry payload.changed=false")

	h.WaitForEventCount(dep.ID, "terminal/success", depPriorCount+1)
	h.WaitForNodeState(dep.ID, cascade.NodeStateFresh)
}
