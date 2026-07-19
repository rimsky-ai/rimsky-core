// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package postgres_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/internal/pgtest"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

// @concept: node-run
func TestSelectCandidates_ForUpdateScopedToDispatchRow_NoInstanceRowContention(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
	store := d.Tables()
	q := d.Queue()

	templateHash := "sha256-" + uuid.NewString()
	instanceID := shared.UUID(uuid.New())
	runScopeID := shared.UUID(uuid.New())
	nodeAID := shared.UUID(uuid.New())
	nodeBID := shared.UUID(uuid.New())

	tmpl := spec.TemplateSpec{
		Name: "for-update-scope-fixture", Version: "1", FrameTimeoutMs: 600000,
		Nodes: []spec.TemplateNodeDef{
			{Type: "node-a", Executor: "test-executor"},
			{Type: "node-b", Executor: "test-executor"},
		},
	}

	var frameID shared.UUID
	require.NoError(t, store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := store.Templates().Insert(ctx, persistence.TemplateInsertInput{
			ID: templateHash, Spec: tmpl, State: persistence.TemplateStateRegistered, Source: "direct",
		}, tx); err != nil {
			return err
		}
		if err := store.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID: runScopeID, GraphName: spec.MainGraphName, InstanceID: instanceID,
		}); err != nil {
			return err
		}
		if _, err := store.Instances().Create(ctx, persistence.InstanceCreateInput{
			ID: instanceID, TemplateHash: templateHash,
		}, tx); err != nil {
			return err
		}
		if _, err := store.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: nodeAID, InstanceID: instanceID, NodeType: "node-a", Executor: "test-executor",
		}, tx); err != nil {
			return err
		}
		if _, err := store.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: nodeBID, InstanceID: instanceID, NodeType: "node-b", Executor: "test-executor",
		}, tx); err != nil {
			return err
		}
		msgID := shared.UUID(uuid.New())
		if err := store.Messages().Insert(ctx, tx, persistence.EnqueueMessageRequest{
			ID: msgID, InstanceID: instanceID, Type: "fixture/message", Sender: "operator", SenderKind: "operator",
		}); err != nil {
			return err
		}
		fid, err := store.Frames().InsertRunningFrame(ctx, instanceID, msgID, runScopeID, 600000, tx)
		if err != nil {
			return err
		}
		frameID = fid
		if err := q.EnqueueInTx(ctx, persistence.DispatchRequest{
			NodeID: nodeAID, ExecutorName: "test-executor", RequiredClaimProducers: []string{},
			EnqueuedAt: time.Now().Add(-2 * time.Second), FrameID: frameID, RunScopeID: runScopeID,
		}, tx); err != nil {
			return err
		}
		return q.EnqueueInTx(ctx, persistence.DispatchRequest{
			NodeID: nodeBID, ExecutorName: "test-executor", RequiredClaimProducers: []string{},
			EnqueuedAt: time.Now().Add(-1 * time.Second), FrameID: frameID, RunScopeID: runScopeID,
		}, tx)
	}))

	holderReady := make(chan struct{})
	release := make(chan struct{})
	holderDone := make(chan error, 1)

	go func() {
		holderDone <- store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			cands, err := q.SelectCandidates(ctx, tx, persistence.SelectCandidatesRequest{
				AcceptedExecutors:      []string{"test-executor"},
				AcceptedClaimProducers: []string{},
				Limit:                  1,
			})
			close(holderReady)
			<-release
			if err != nil {
				return err
			}
			if len(cands) != 1 {
				return fmt.Errorf("holder transaction: expected exactly 1 candidate (limit=1), got %d", len(cands))
			}
			return nil
		})
	}()

	<-holderReady

	var probeCands []persistence.Candidate
	require.NoError(t, store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		cands, err := q.SelectCandidates(ctx, tx, persistence.SelectCandidatesRequest{
			AcceptedExecutors:      []string{"test-executor"},
			AcceptedClaimProducers: []string{},
			Limit:                  10,
		})
		probeCands = cands
		return err
	}), "SelectCandidates must not block while another transaction holds a FOR UPDATE lock on a "+
		"sibling dispatch row of the same instance — proof that FOR UPDATE is scoped to the dispatch "+
		"row (rimsky_node_runs), not to the joined rimsky_instances row")

	close(release)
	require.NoError(t, <-holderDone)

	foundSibling := false
	for _, c := range probeCands {
		if c.NodeID == nodeBID {
			foundSibling = true
		}
	}
	require.True(t, foundSibling,
		"the concurrent SelectCandidates call must still surface node-b (node-a's own dispatch row is "+
			"genuinely FOR UPDATE-locked by the holder transaction, so node-a being absent here is "+
			"expected either way) while the holder transaction holds its lock open — if FOR UPDATE "+
			"were widened from 'FOR UPDATE OF d' to an unscoped 'FOR UPDATE' (locking the joined "+
			"rimsky_instances row too, shared by both node-a and node-b), SKIP LOCKED would silently "+
			"drop node-b's row too since its join also touches the now-locked instance row, and this "+
			"candidate list would come back without node-b instead")
}
