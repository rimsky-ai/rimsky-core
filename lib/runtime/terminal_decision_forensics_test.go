// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks/storetest"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
	"github.com/rimsky-ai/rimsky-core/test/support/pgdbtest"
)

func TestResolveClaimHandleTerminal_LineageRecordsTerminatingSupervisorAfterPromoteNullsHandle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgdbtest.OpenDriver(ctx, t)
	backend := d.Tables()

	tmpl := insertDeployedTemplate(ctx, t, backend, node.TemplateSpec{
		Name: "lineage-terminating-supervisor", Version: "1",
	})
	ck := "ck-lineage-sup"
	var mainScopeID shared.UUID
	var inst persistence.InstanceRow
	var workerNode persistence.NodeRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		i, ms := seedInstanceWithMainScope(ctx, t, backend, tmpl.ID, &ck, tx)
		inst = i
		mainScopeID = ms
		n, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: inst.ID, NodeType: "worker", Executor: "stub",
		}, tx)
		if err != nil {
			return err
		}
		workerNode = n
		return nil
	}))

	frameID := seedFrame(ctx, t, backend, inst.ID, mainScopeID)
	nodeRunID := seedRunForNode(ctx, t, backend, d.Queue(), workerNode.ID, frameID)

	const terminatingSupervisor = "sup-lineage-terminator"
	claimID := shared.UUID(uuid.New())
	producerName := "lineage-sup-store"
	intent := "rw"
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return backend.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID:                 claimID,
			LockKind:           persistence.LockKindScope,
			ProducerName:       &producerName,
			ClaimScopeData:     []byte(`"lineage-sup-scope"`),
			Address:            []byte(`"lineage-sup-addr"`),
			Intent:             &intent,
			HolderSupervisorID: terminatingSupervisor,
			HolderNodeID:       workerNode.ID,
			ExpiresAt:          time.Now().Add(10 * time.Minute),
			NodeRunID:          &nodeRunID,
		}, tx)
	}))

	reg := locks.NewRegistry()
	store := storetest.NewFake(producerName, claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
	})
	reg.Add(producerName, store)
	args := runtime.RunArgs{
		Persist:       backend,
		ClaimHandles:  backend.ClaimHandles(),
		StoreRegistry: reg,
		Logger:        shared.SilentLogger{},
		Clock:         shared.SystemClock{},
		SupervisorID:  terminatingSupervisor,
	}

	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		_, err := runtime.ResolveClaimHandleTerminal(ctx, args, runtime.TerminalDecision{
			ClaimHandleID: claimID,
			SupervisorID:  terminatingSupervisor,
			Source:        runtime.ActiveTerminal,
			Outcome:       runtime.OutcomeCommit,
			Producer:      store,
			Scope:         []byte(`"lineage-sup-scope"`),
			Address:       []byte(`"lineage-sup-addr"`),
			ProducerName:  producerName,
			LineageHint: runtime.ClaimLineageHint{
				InstanceID:   inst.ID,
				FrameID:      frameID,
				NodeRunID:    nodeRunID,
				NodeID:       workerNode.ID,
				ProducerName: producerName,
			},
		}, tx)
		return err
	}))

	var handleAfter *persistence.ClaimHandleRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		row, err := backend.ClaimHandles().Get(ctx, claimID, tx)
		handleAfter = row
		return err
	}))
	require.NotNil(t, handleAfter)
	require.Nil(t, handleAfter.HolderSupervisorID,
		"Promote must null holder_supervisor_id on the claim-handle row after termination")

	rows, err := backend.Lineage().GetByClaimHandleID(ctx, claimID)
	require.NoError(t, err)
	var claimTerminalRow *persistence.LineageRow
	for i := range rows {
		if rows[i].RecordKind == persistence.LineageRecordKindClaimTerminal {
			claimTerminalRow = &rows[i]
			break
		}
	}
	require.NotNil(t, claimTerminalRow, "expected a claim_terminal lineage row for %s", claimID)

	var rec runtime.ClaimTerminalRecord
	require.NoError(t, json.Unmarshal(claimTerminalRow.Record, &rec))
	require.Equal(t, terminatingSupervisor, rec.TerminatingSupervisorID,
		"lineage claim_terminal record must name the terminating supervisor now that the claim-handle row no longer does")
}
