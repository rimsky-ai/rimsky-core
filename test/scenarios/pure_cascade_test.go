// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @decision: empty-message-as-root-trigger
// @story: empty-message-wakes-roots
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

func TestPureCascadeNode(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "pure-cascade", Version: "1",
		Messages: []spec.MessageSchema{
			{Type: "test/wake/hub"},
		},
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "hub"},
				scenario.WithSubscribes(node.SubscriptionEntry{
					Node: "test/wake/hub", Type: "terminal/success",
					ForceUpstreamRefresh: node.BoolPtr(false),
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-pc", map[string]any{})

	hub := h.FindNode(iid, "hub")
	require.NotNil(t, hub)
	h.PostInstanceMessage(iid, "test/wake/hub", nil, fmt.Sprintf("test-wake-%s-init", t.Name()))
	require.True(t, h.WaitForNodeState(hub.ID, cascade.NodeStateFresh, 10*time.Second),
		"hub did not reach fresh after the typed-message wake")

	h.PostInstanceMessage(iid, "test/wake/hub", nil, fmt.Sprintf("test-wake-%s-1", t.Name()))

	require.True(t, h.WaitForNodeState(hub.ID, cascade.NodeStateFresh, 10*time.Second),
		"hub did not return to fresh after invalidate")

	nid := hub.ID
	var evs persistence.EventListResult
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Events().List(h.Ctx, persistence.EventListFilter{NodeID: &nid},
			persistence.ListPagination{Limit: 500}, tx)
		evs = r
		return err
	}))
	var sawCommit bool
	for _, e := range evs.Events {
		if e.KindRaw == "terminal/success" {
			sawCommit = true
			break
		}
	}
	require.True(t, sawCommit, "expected terminal/success signal event for pure-cascade transition")
}
