// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package lineage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks/storetest"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
)

func seedDeployedTemplate(ctx context.Context, t *testing.T, backend persistence.Tables, tag string) persistence.TemplateRow {
	t.Helper()
	sum := sha256.Sum256([]byte("lineage-forensics:" + tag))
	hash := "sha256-" + hex.EncodeToString(sum[:])
	var row *persistence.TemplateRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := backend.Templates().Insert(ctx, persistence.TemplateInsertInput{
			ID:    hash,
			Spec:  node.TemplateSpec{Name: tag, Version: "1"},
			State: persistence.TemplateStateRegistered,
		}, tx); err != nil {
			return err
		}
		if err := backend.Templates().UpdateState(ctx, hash, persistence.TemplateStateDeployed, tx); err != nil {
			return err
		}
		r, err := backend.Templates().GetByHash(ctx, hash, tx)
		if err != nil {
			return err
		}
		row = r
		return nil
	}))
	return *row
}

func seedFrameRow(ctx context.Context, t *testing.T, backend persistence.Tables, instanceID, sourceNodeID, rootScope shared.UUID) shared.UUID {
	t.Helper()
	_ = sourceNodeID
	var frameID shared.UUID
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		msgID := shared.UUID(uuid.New())
		if err := backend.Messages().Insert(ctx, persistence.EnqueueMessageRequest{
			ID:         msgID,
			InstanceID: instanceID,
			Type:       "test/seed",
			Sender:     "test",
			SenderKind: "operator",
		}, tx); err != nil {
			return err
		}
		fid, err := backend.Frames().InsertRunningFrame(ctx, instanceID, msgID, rootScope, tx)
		if err != nil {
			return err
		}
		frameID = fid
		return nil
	}))
	return frameID
}

func seedRunRow(ctx context.Context, t *testing.T, backend persistence.Tables, nodeID, frameID shared.UUID) shared.UUID {
	t.Helper()
	runID := shared.UUID(uuid.New())
	var scopeID shared.UUID
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		frameRow, err := backend.Frames().GetForObservability(ctx, frameID, tx)
		if err != nil {
			return err
		}
		if frameRow == nil {
			t.Fatalf("seedRunRow: frame %s missing", frameID)
		}
		scopeID = frameRow.RootRunScopeID
		return nil
	}))
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return backend.NodeRunTree().CreateRootNodeRun(ctx, persistence.CreateRootNodeRunInput{
			NodeRunID:    runID,
			NodeID:       nodeID,
			FrameID:      frameID,
			ExecutorName: "stub",
			RunScopeID:   scopeID,
		}, tx)
	}))
	return runID
}

func countCallsOnID(calls []storetest.FakeCall, claimID, verb string) int {
	n := 0
	for _, c := range calls {
		if string(c.ClaimID) == claimID && c.Verb == verb {
			n++
		}
	}
	return n
}
