// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package runtime

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/pgdbtest"
)

// @story: sequenced-preserves-cascade-rounds
// @concept: cascade-mode
func TestSequencedMode_ARoundWaitsOnItsOwnSenderNotOnAnother(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgdbtest.OpenDriver(ctx, t)
	backend := d.Tables()

	tmpl := insertDeployedTemplateInternal(ctx, t, backend, node.TemplateSpec{
		Name: "sequenced-two-senders", Version: "1",
		Nodes: []node.TemplateNodeDef{
			{Type: "a", Executor: "stub"},
			{Type: "c", Executor: "stub"},
			{
				Type:        "b",
				Executor:    "stub",
				CascadeMode: spec.CascadeModeSequenced,
				Subscribes: []node.SubscriptionEntry{
					{Node: "a", Type: "terminal/success", ForceUpstreamRefresh: node.BoolPtr(false)},
					{Node: "c", Type: "terminal/success", ForceUpstreamRefresh: node.BoolPtr(false)},
				},
			},
		},
	})

	instID := shared.UUID(uuid.New())
	scopeID := shared.UUID(uuid.New())
	ck := "ck-" + uuid.NewString()
	nodeIDs := map[string]shared.UUID{}
	var frameID shared.UUID
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := backend.RunScopes().Create(ctx, persistence.RunScopeRow{
			ID: scopeID, GraphName: "main", InstanceID: instID,
		}, tx); err != nil {
			return err
		}
		if _, err := backend.Instances().Create(ctx, persistence.InstanceCreateInput{
			TargetRoutingIdentity: "test-daemon",
			ID:                    instID, TemplateHash: tmpl.ID, InstanceKey: &ck, Params: map[string]any{},
		}, tx); err != nil {
			return err
		}
		for _, def := range tmpl.Spec.Nodes {
			n, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
				ID: shared.UUID(uuid.New()), InstanceID: instID, NodeType: def.Type,
				Executor: def.Executor, CascadeMode: def.CascadeMode,
			}, tx)
			if err != nil {
				return err
			}
			nodeIDs[def.Type] = n.ID
		}
		msgID := shared.UUID(uuid.New())
		if err := backend.Messages().Insert(ctx, persistence.EnqueueMessageRequest{
			ID: msgID, InstanceID: instID, Type: "test/seed", Sender: "test", SenderKind: "operator",
		}, tx); err != nil {
			return err
		}
		fid, err := backend.Frames().InsertRunningFrame(ctx, instID, msgID, scopeID, tx)
		frameID = fid
		return err
	}))

	round := func(receiverType, senderType string) shared.UUID {
		t.Helper()
		var receiverRunID shared.UUID
		require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			senderRunID, err := backend.Nodes().CreateCascadePending(ctx, nodeIDs[senderType], scopeID, frameID, tx)
			if err != nil {
				return err
			}
			receiverRunID, err = backend.Nodes().CreateCascadePending(ctx, nodeIDs[receiverType], scopeID, frameID, tx)
			if err != nil {
				return err
			}
			if err := backend.WaitSet().Insert(ctx, persistence.WaitSetRow{
				FrameID:           frameID,
				ReceiverNodeRunID: receiverRunID,
				SenderNodeRunID:   senderRunID,
				TopicKind:         "terminal",
			}, tx); err != nil {
				return err
			}
			if err := backend.Nodes().UpdateState(ctx, senderRunID,
				cascade.NodeStateStale, cascade.ReasonGateCleared, nil, tx); err != nil {
				return err
			}
			if err := backend.Nodes().UpdateState(ctx, senderRunID,
				cascade.NodeStateFresh, cascade.ReasonPureCascade, nil, tx); err != nil {
				return err
			}
			return backend.WaitSet().MarkDrainedBySender(ctx, frameID, senderRunID, tx)
		}))
		return receiverRunID
	}

	fromA := round("b", "a")
	fromC := round("b", "c")

	args := RunArgs{
		Persist: backend, Queue: d.Queue(), Clock: shared.SystemClock{}, Logger: shared.SilentLogger{},
	}
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return evaluateOneGate(ctx, args, fromC, tx)
	}))

	require.Equal(t, "stale", runStateInternal(ctx, t, backend, fromC),
		"a round from one sender must not wait on an older round of a different sender: "+
			"sequenced orders a receiver's rounds per sender")
	require.Equal(t, "pending", runStateInternal(ctx, t, backend, fromA),
		"the older round from the other sender is untouched by its successor's gate")
}
