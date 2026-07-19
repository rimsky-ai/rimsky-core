// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @decision: frame-isolation-is-structural
package per_run_attributes

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

func TestPerRunAttributes_FreshScopeDefaultsAtFrameStart(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("worker").Success(map[string]any{"value": "mutated-frame-1"}, true, "ok")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "fresh-scope-defaults", Version: "1",
		Messages: []spec.MessageSchema{
			{Type: "test/wake/worker"},
		},
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "worker", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{
					Node: "test/wake/worker", Type: "terminal/success",
					ForceUpstreamRefresh: node.BoolPtr(false),
				}),
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"value": map[string]any{"type": "string", "default": "factory-default"},
					},
					"required": []any{"value"},
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-fresh-scope-defaults", map[string]any{})
	w := h.FindNode(iid, "worker")
	require.NotNil(t, w)

	h.PostInstanceMessage(iid, "test/wake/worker", nil, fmt.Sprintf("test-wake-%s-1", t.Name()))
	h.WaitForNodeState(w.ID, cascade.NodeStateFresh)

	var firstRun *persistence.NodeAttributesRow
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.NodeAttributes().GetLatestByNode(h.Ctx, w.ID, h.GetMainRunScopeID(iid), tx)
		firstRun = r
		return err
	}))
	require.NotNil(t, firstRun)
	require.Equal(t, "mutated-frame-1", firstRun.Data["value"],
		"frame 1 must persist the stub's mutated value on the run's own row")

	h.Stub.WhenType("worker").Success(map[string]any{"value": "mutated-frame-2"}, true, "ok")
	h.PostInstanceMessage(iid, "test/wake/worker", nil, fmt.Sprintf("test-wake-%s-2", t.Name()))

	deadline := time.Now().Add(15 * time.Second)
	var secondRun *persistence.NodeAttributesRow
	for time.Now().Before(deadline) {
		_ = h.InTx(func(tx persistence.Tx) error {
			r, err := h.Persist.NodeAttributes().GetLatestByNode(h.Ctx, w.ID, h.GetMainRunScopeID(iid), tx)
			secondRun = r
			return err
		})
		if secondRun != nil && secondRun.NodeRunID != firstRun.NodeRunID &&
			secondRun.Data["value"] == "mutated-frame-2" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	require.NotNil(t, secondRun)
	require.NotEqual(t, firstRun.NodeRunID, secondRun.NodeRunID,
		"the second frame must dispatch a distinct node_run")

	var secondDispatchBag map[string]any
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		bag, err := h.Persist.NodeAttributes().GetDispatchInputBag(h.Ctx, tx, secondRun.NodeRunID)
		secondDispatchBag = bag
		return err
	}))
	require.Equal(t, "factory-default", secondDispatchBag["value"],
		"attributes at frame start must be the schema defaults — frame 1's mutated value must NOT carry into frame 2's starting bag")
}
