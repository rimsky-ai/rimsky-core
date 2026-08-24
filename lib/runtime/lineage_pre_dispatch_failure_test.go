// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package runtime

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	attributes "github.com/rimsky-ai/rimsky-core/lib/graph/attribute"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/pgdbtest"
)

// @decision: lineage-records-computation-only
func TestAttributeResolutionFailureBeforeDispatchWritesNoLineageRecord(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgdbtest.OpenDriver(ctx, t)
	backend := d.Tables()
	q := d.Queue()

	tmpl := insertDeployedTemplateInternal(ctx, t, backend, node.TemplateSpec{
		Name: "attribute-failure-no-lineage", Version: "1",
		Nodes: []node.TemplateNodeDef{{Type: "leaf", Executor: "stub"}},
	})
	nodeID, frameID, nodeRunID, runScopeID, instanceID := seedStaleCandidateInternal(ctx, t, backend, q, tmpl.ID, "leaf")

	acq := &acquisition{
		NodeID: nodeID, NodeRunID: nodeRunID, InstanceID: instanceID, NodeType: "leaf",
		NodeDef:    &node.TemplateNodeDef{Type: "leaf", Executor: "stub"},
		RunScopeID: runScopeID, FrameID: frameID, Executor: "stub",
	}
	args := RunArgs{
		Persist: backend, Queue: q, Logger: shared.SilentLogger{}, Clock: shared.SystemClock{},
		SupervisorID: "sup-attribute-failure-no-lineage",
	}

	subErr := &attributes.ErrMissingSource{Directive: "deps.missing.field", Reason: "no upstream"}
	require.NoError(t, applyAttributeFailure(ctx, args, acq, subErr))

	require.Equal(t, "failed", runStateInternal(ctx, t, backend, nodeRunID),
		"the terminal path ran: the run settles to failed")
	requireNoLeafRunLineage(ctx, t, backend, nodeRunID)
}

// @decision: lineage-records-computation-only
func TestGateSubstitutionFailureWritesNoLineageRecord(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgdbtest.OpenDriver(ctx, t)
	backend := d.Tables()

	tmpl := insertDeployedTemplateInternal(ctx, t, backend, node.TemplateSpec{
		Name: "gate-substitution-failure-no-lineage", Version: "1",
		Nodes: []node.TemplateNodeDef{{Type: "leaf", Executor: "stub"}},
	})

	instID := shared.UUID(uuid.New())
	scopeID := shared.UUID(uuid.New())
	ck := "ck-" + uuid.NewString()
	var leafNodeID, frameID shared.UUID
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
		n, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: instID, NodeType: "leaf", Executor: "stub",
		}, tx)
		if err != nil {
			return err
		}
		leafNodeID = n.ID
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

	nodeRunID := shared.UUID(uuid.New())
	pgdbtest.ExecForTest(ctx, t, d, `
		INSERT INTO rimsky_node_runs
			(id, node_id, executor_name, required_claim_producers, enqueued_at, state, sequence, creation_reason, frame_id, run_scope_id)
		VALUES ($1, $2, 'stub', ARRAY[]::text[], NOW(), 'pending', 10, 'cascade', $3, $4)
	`, nodeRunID, leafNodeID, frameID, scopeID)

	args := RunArgs{
		Persist: backend, Queue: d.Queue(), Logger: shared.SilentLogger{}, Clock: shared.SystemClock{},
		SupervisorID: "sup-gate-substitution-failure",
	}
	subErr := &attributes.ErrMissingSource{Directive: "deps.missing.field", Reason: "no upstream"}
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		row, err := backend.Nodes().GetRunForGate(ctx, nodeRunID, tx)
		if err != nil {
			return err
		}
		return routeSubstitutionFailureAtGate(ctx, args, row, subErr, tx)
	}))

	require.Equal(t, "failed", runStateInternal(ctx, t, backend, nodeRunID),
		"the terminal path ran: the round settles to failed at the gate")
	requireNoLeafRunLineage(ctx, t, backend, nodeRunID)
}

func requireNoLeafRunLineage(
	ctx context.Context, t *testing.T, backend persistence.Tables, nodeRunID shared.UUID,
) {
	t.Helper()
	rows, err := backend.Lineage().GetByRunID(ctx, nodeRunID)
	require.NoError(t, err)
	for _, row := range rows {
		require.NotEqual(t, persistence.LineageRecordKindLeafRun, row.RecordKind,
			"a run that invoked no executor must write no leaf-run lineage record")
	}
}
