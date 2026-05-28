// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Shared helpers for the post-mortem forensics scenarios.
//
// @source: test/scenarios/lineage/helpers_test.go. Tracked duplication —
// the two scenario packages cannot import each other's test-only types.

package forensics

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
)

// seedDeployedTemplate inserts a template row in 'deployed' state with a
// deterministic content hash derived from the supplied tag.
func seedDeployedTemplate(ctx context.Context, t *testing.T, backend persistence.Tables, tag string) persistence.TemplateRow {
	t.Helper()
	sum := sha256.Sum256([]byte("forensics:" + tag))
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

// seedFrameRow enqueues a running frame for the instance + source node.
func seedFrameRow(ctx context.Context, t *testing.T, backend persistence.Tables, instanceID, sourceNodeID shared.UUID) shared.UUID {
	t.Helper()
	var frameID shared.UUID
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		fid, err := backend.Frames().EnqueueSerialFrame(ctx, instanceID, sourceNodeID, 600000, tx)
		if err != nil {
			return err
		}
		if _, err := backend.Frames().PromoteQueuedFrameToRunning(ctx, fid, tx); err != nil {
			return err
		}
		frameID = fid
		return nil
	}))
	return frameID
}

// seedRunRow creates a fresh `rimsky_node_runs` row for the given node.
func seedRunRow(ctx context.Context, t *testing.T, backend persistence.Tables, nodeID, frameID shared.UUID) shared.UUID {
	t.Helper()
	runID := shared.UUID(uuid.New())
	var scopeID shared.UUID
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		nd, err := backend.Nodes().Get(ctx, nodeID, tx)
		if err != nil {
			return err
		}
		if nd == nil {
			t.Fatalf("seedRunRow: node %s missing", nodeID)
		}
		inst, err := backend.Instances().Get(ctx, nd.InstanceID, tx)
		if err != nil {
			return err
		}
		if inst == nil {
			t.Fatalf("seedRunRow: instance %s missing", nd.InstanceID)
		}
		scopeID = inst.MainRunScopeID
		return nil
	}))
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return backend.RunTree().CreateRootRun(ctx, tx, persistence.CreateRootRunInput{
			RunID:        runID,
			NodeID:       nodeID,
			FrameID:      frameID,
			ExecutorName: "stub",
			RunScopeID:   scopeID,
		})
	}))
	return runID
}
