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
	"github.com/rimsky-ai/rimsky-core/test/support/eventwait"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestParkedResumeDoesNotSpuriouslyCascadeSuccessSubscriberOnError(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	resumeAt := time.Now().Add(8 * time.Second)
	h.Stub.WhenType("worker").
		Park(resumeAt)
	h.Stub.WhenType("downstream").Success(map[string]any{}, true, "must-not-run")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "parked-resume-spurious-cascade", Version: "1",
		Messages: []spec.MessageSchema{
			{Type: "test/wake/worker"},
		},
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "worker",
					Executor: "stub",
					ErrorTypes: map[string]node.ErrorTypePolicy{
						"stub/boom": {
							Action: "give_up",
						},
					},
				},
				scenario.WithSubscribes(node.SubscriptionEntry{
					Node: "test/wake/worker", Type: "terminal/success",
					ForceUpstreamRefresh: node.BoolPtr(false),
				}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "downstream",
					Executor: "stub",
				},
				scenario.WithSubscribes(node.SubscriptionEntry{
					Node: "worker", Type: "terminal/success",
					ForceUpstreamRefresh: node.BoolPtr(false),
				}),
			),
		},
	})

	iid := h.CreateInstance(tid, "ck-parked-resume-spurious", map[string]any{})
	worker := h.FindNode(iid, "worker")
	downstream := h.FindNode(iid, "downstream")
	require.NotNil(t, worker)
	require.NotNil(t, downstream)

	h.PostInstanceMessage(iid, "test/wake/worker", nil,
		fmt.Sprintf("test-wake-%s-init", t.Name()))

	h.WaitForNodeState(worker.ID, cascade.NodeStateParked)

	h.Stub.WhenType("worker").Error("boom", map[string]any{"why": "post-resume-error"})

	h.WaitForEventKind(worker.ID, "parked_resume_started")

	h.WaitForNodeState(worker.ID, cascade.NodeStateFailed)
	h.WaitForEventKind(worker.ID, "terminal/error/stub/boom")

	time.Sleep(2 * time.Second)

	var downstreamLatest *persistence.NodeRunLatest
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Nodes().GetLatestRunForNode(h.Ctx, tx, downstream.ID)
		downstreamLatest = r
		return err
	}))
	if downstreamLatest != nil {
		require.Equal(t, cascade.NodeStateFresh, downstreamLatest.State,
			"downstream subscribes only to terminal/success on worker. Worker never emitted "+
				"terminal/success — it parked then emitted terminal/error/stub/boom. "+
				"Downstream must remain in its initial fresh state (no dispatch). "+
				"If downstream is running/stale/failed, the synthetic terminal/success cascade "+
				"on parked-resume spuriously routed the error settlement into downstream.")
	}

	dsID := downstream.ID
	require.Empty(t,
		eventwait.Events(h.Ctx, t, h.Persist,
			eventwait.Matcher{NodeID: &dsID, KindPrefix: "terminal/"}),
		"downstream must leave no terminal/* events on the ledger when worker emits "+
			"terminal/error/<class> — any such event proves a spurious dispatch driven "+
			"by the synthetic terminal/success cascade on parked-resume")
	require.Empty(t,
		eventwait.Events(h.Ctx, t, h.Persist,
			eventwait.Matcher{NodeID: &dsID, Kind: "work_started"}),
		"downstream must record no work_started event")

	for _, o := range h.Stub.Observed() {
		require.NotEqual(t, "downstream", o.NodeType,
			"downstream executor must not be invoked — worker's post-resume terminal/error "+
				"must not cascade to a terminal/success-only subscriber")
	}
}
