// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @decision: empty-message-as-root-trigger
// @story: empty-message-wakes-roots
package scenarios

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/awaited"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func countTerminalSuccessEvents(t *testing.T, h *scenario.Harness, nodeID shared.UUID) int {
	t.Helper()
	var evs persistence.EventListResult
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Events().List(h.Ctx, persistence.EventListFilter{NodeID: &nodeID},
			persistence.ListPagination{Limit: 500}, tx)
		evs = r
		return err
	}))
	n := 0
	for _, e := range evs.Events {
		if e.KindRaw == "terminal/success" {
			n++
		}
	}
	return n
}

func waitForTerminalSuccessCount(t *testing.T, h *scenario.Harness, nodeID shared.UUID, want int) {
	t.Helper()
	awaited.Until(t, fmt.Sprintf("%d terminal/success event(s) on node %s", want, nodeID), func() bool {
		return countTerminalSuccessEvents(t, h, nodeID) >= want
	})
}

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
	h.WaitForNodeState(hub.ID, cascade.NodeStateFresh)

	round1Count := countTerminalSuccessEvents(t, h, hub.ID)
	require.Equal(t, 1, round1Count, "round 1 must commit exactly one terminal/success signal")

	h.PostInstanceMessage(iid, "test/wake/hub", nil, fmt.Sprintf("test-wake-%s-1", t.Name()))

	waitForTerminalSuccessCount(t, h, hub.ID, round1Count+1)
	h.WaitForNodeState(hub.ID, cascade.NodeStateFresh)

	require.Equal(t, round1Count+1, countTerminalSuccessEvents(t, h, hub.ID),
		"the re-invalidate/re-cascade round must commit exactly one additional terminal/success signal (not zero, not duplicated)")
}

// @decision: lineage-records-computation-only
// @concept: lineage
// @concept: lineage-record
func TestPassThroughNodeSettlementWritesNoLineageRecord(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("worker").Success(map[string]any{}, true, "worked")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "pass-through-lineage", Version: "1",
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
			scenario.MakeNode(node.TemplateNodeDef{Type: "worker", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{
					Node: "hub", Type: "terminal/success",
					ForceUpstreamRefresh: node.BoolPtr(false),
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-pass-through-lineage", map[string]any{})

	hub := h.FindNode(iid, "hub")
	require.NotNil(t, hub)
	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)

	h.PostInstanceMessage(iid, "test/wake/hub", nil, fmt.Sprintf("test-wake-%s-init", t.Name()))
	h.WaitForNodeState(worker.ID, cascade.NodeStateFresh)
	h.WaitForLeafRunLineageCount(worker.ID, 1)

	var hubRecords int
	h.QueryRowSQL(
		`SELECT count(*) FROM rimsky_lineage
		 WHERE record_kind = 'leaf_run' AND record->>'node_id' = $1`,
		[]any{hub.ID.String()},
		&hubRecords,
	)
	require.Zero(t, hubRecords,
		"the hub invokes no executor, so its settlement writes no lineage record; the worker downstream "+
			"already has its record, so the cascade reached the hub and passed through it")
}
