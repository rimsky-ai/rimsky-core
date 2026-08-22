// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package runtime_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
	"github.com/rimsky-ai/rimsky-core/test/support/pgdbtest"
)

type resolvedHolder struct {
	runID  shared.UUID
	nodeID shared.UUID
}

func seedResolvedHeldHolder(
	ctx context.Context, t *testing.T, d persistence.Database, backend persistence.Tables,
	instanceID, frameID shared.UUID, nodeType, holderState string,
) resolvedHolder {
	t.Helper()
	var n persistence.NodeRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		row, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: instanceID, NodeType: nodeType, Executor: "stub",
		}, tx)
		n = row
		return err
	}))
	runID := seedRunForNode(ctx, t, backend, d.Queue(), n.ID, frameID)

	producerName := "workspace"
	intent := "rw"
	claimHandleID := shared.UUID(uuid.New())
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := backend.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID: claimHandleID, LockKind: persistence.LockKindScope,
			ProducerName: &producerName, ClaimScopeData: []byte(`"r"`), Address: []byte(`"r"`),
			Intent:             &intent,
			HolderSupervisorID: "sup-A", HolderNodeID: n.ID,
			ExpiresAt: time.Now().Add(10 * time.Minute),
		}, tx); err != nil {
			return err
		}
		if err := backend.ClaimHolders().Insert(ctx, persistence.ClaimHolderInsertInput{
			ID: shared.UUID(uuid.New()), ClaimHandleID: claimHandleID, HolderNodeRunID: runID,
		}, tx); err != nil {
			return err
		}
		return backend.ClaimHolders().CompleteByClaimHandleAndRun(
			ctx, claimHandleID, runID, persistence.ClaimHolderStateCompleted, tx)
	}))
	pgdbtest.ExecForTest(ctx, t, d,
		`UPDATE rimsky_claim_handles SET state = 'committed', resolved_at = NOW(), holder_supervisor_id = NULL WHERE id = $1`,
		claimHandleID)
	pgdbtest.ExecForTest(ctx, t, d,
		`UPDATE rimsky_node_runs SET state = $2 WHERE id = $1`, runID, holderState)
	return resolvedHolder{runID: runID, nodeID: n.ID}
}

// @concept: auto-terminal
func TestHeldHolderTransition_SkipsARunAnotherTerminalWriterAlreadySettled(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgdbtest.OpenDriver(ctx, t)
	backend := d.Tables()

	tmpl := insertDeployedTemplate(ctx, t, backend, node.TemplateSpec{
		Name: "held-holder-settled-race", Version: "1",
		Nodes: []node.TemplateNodeDef{
			{Type: "still-held", Executor: "stub"},
			{Type: "already-settled", Executor: "stub"},
		},
	})
	ck := "ck-" + uuid.NewString()
	var inst persistence.InstanceRow
	var mainScopeID shared.UUID
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		i, ms := seedInstanceWithMainScope(ctx, t, backend, tmpl.ID, &ck, tx)
		inst = i
		mainScopeID = ms
		return nil
	}))
	frameID := seedFrame(ctx, t, backend, inst.ID, mainScopeID)

	held := seedResolvedHeldHolder(ctx, t, d, backend, inst.ID, frameID, "still-held", "held")
	settled := seedResolvedHeldHolder(ctx, t, d, backend, inst.ID, frameID, "already-settled", "failed")

	args := runtime.RunArgs{
		Persist:      backend,
		ClaimHandles: backend.ClaimHandles(),
		Clock:        shared.SystemClock{},
		Logger:       shared.SilentLogger{},
		SupervisorID: "sup-A",
	}
	transition := func(runID shared.UUID) error {
		return backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			_, err := runtime.TransitionHolderIfFullyResolvedForTest(ctx, args, runID, tx)
			return err
		})
	}
	stateOf := func(runID shared.UUID) string {
		t.Helper()
		var row *persistence.NodeRunForGate
		require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			r, err := backend.Nodes().GetRunForGate(ctx, runID, tx)
			row = r
			return err
		}))
		require.NotNil(t, row)
		return string(row.State)
	}

	require.NoError(t, transition(held.runID))
	require.Equal(t, "fresh", stateOf(held.runID),
		"a held holder whose portfolio has fully resolved settles to fresh, so the fixture is genuinely resolvable")

	require.NoError(t, transition(settled.runID),
		"a holder whose run another terminal writer already settled must skip without an error")
	require.Equal(t, "failed", stateOf(settled.runID),
		"the first writer's verdict stands; auto-terminal must not overwrite it")
}
