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

	require.True(t, h.WaitForNodeState(producer.ID, cascade.NodeStateFresh, 60*time.Second),
		"producer did not reach fresh")
	require.True(t, h.WaitForNodeState(dep.ID, cascade.NodeStateFresh, 60*time.Second),
		"dependent did not reach fresh on first cascade")

	var depBefore *persistence.NodeRow
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Nodes().Get(h.Ctx, dep.ID, tx)
		depBefore = r
		return err
	}))

	h.Stub.WhenType("producer").Success(map[string]any{"x": 1}, false, "noop")

	pid := producer.ID
	var priorCommitted persistence.EventListResult
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Events().List(h.Ctx,
			persistence.EventListFilter{NodeID: &pid, Kind: "terminal/success"},
			persistence.ListPagination{Limit: 200}, tx)
		priorCommitted = r
		return err
	}))
	priorCount := len(priorCommitted.Events)

	h.PostInstanceMessage(iid, "test/wake/producer", nil, fmt.Sprintf("test-wake-%s-1", t.Name()))

	require.Eventually(t,
		func() bool {
			var ev persistence.EventListResult
			err := h.InTx(func(tx persistence.Tx) error {
				r, lerr := h.Persist.Events().List(h.Ctx,
					persistence.EventListFilter{NodeID: &pid, Kind: "terminal/success"},
					persistence.ListPagination{Limit: 200}, tx)
				ev = r
				return lerr
			})
			return err == nil && len(ev.Events) > priorCount
		},
		60*time.Second, 100*time.Millisecond,
		"producer did not emit a second terminal/success after changed=false run",
	)

	var allCommitted persistence.EventListResult
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Events().List(h.Ctx,
			persistence.EventListFilter{NodeID: &pid, Kind: "terminal/success"},
			persistence.ListPagination{Limit: 200}, tx)
		allCommitted = r
		return err
	}))
	require.Greater(t, len(allCommitted.Events), priorCount,
		"expected a new terminal/success after changed=false run")
	latest := allCommitted.Events[0]
	changedVal, _ := latest.Payload["changed"].(bool)
	require.False(t, changedVal,
		"second run's terminal/success must carry payload.changed=false")

	require.True(t, h.WaitForNodeState(dep.ID, cascade.NodeStateFresh, 30*time.Second),
		"dependent should re-reach fresh after idempotent cascade from producer no_op commit")

	_ = depBefore
}
