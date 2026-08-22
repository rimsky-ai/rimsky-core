// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package runtime_test

import (
	"context"
	"encoding/json"
	"fmt"
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
		Persist:               backend,
		ClaimHandles:          backend.ClaimHandles(),
		ClaimProducerRegistry: reg,
		Logger:                shared.SilentLogger{},
		Clock:                 shared.SystemClock{},
		SupervisorID:          terminatingSupervisor,
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

// @decision: promotion-lineage-record-after-commit
// @concept: lineage-record
// @concept: data-processing
func TestResolveClaimHandleTerminal_PromotionLineageRecordWaitsForTheCommitResponseAndCarriesTheVersion(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgdbtest.OpenDriver(ctx, t)
	backend := d.Tables()

	tmpl := insertDeployedTemplate(ctx, t, backend, node.TemplateSpec{
		Name: "lineage-promotion-version", Version: "1",
	})
	ck := "ck-lineage-promotion"
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

	const supervisorID = "sup-lineage-promotion"
	claimID := shared.UUID(uuid.New())
	producerName := "lineage-promotion-store"
	intent := "rw"
	candidateHandle := []byte(`"candidate-1"`)
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return backend.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID:                      claimID,
			LockKind:                persistence.LockKindScope,
			ProducerName:            &producerName,
			ClaimScopeData:          []byte(`"promotion-scope"`),
			Address:                 []byte(`"promotion-addr"`),
			Intent:                  &intent,
			HolderSupervisorID:      supervisorID,
			HolderNodeID:            workerNode.ID,
			ExpiresAt:               time.Now().Add(10 * time.Minute),
			NodeRunID:               &nodeRunID,
			ProducerCandidateHandle: candidateHandle,
		}, tx)
	}))

	reg := locks.NewRegistry()
	store := storetest.NewFake(producerName, claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
	})
	store.CommitResult = claimproducer.CommitResult{VersionID: "v-42"}
	reg.Add(producerName, store)
	args := runtime.RunArgs{
		Persist:               backend,
		ClaimHandles:          backend.ClaimHandles(),
		ClaimProducerRegistry: reg,
		Logger:                shared.SilentLogger{},
		Clock:                 shared.SystemClock{},
		SupervisorID:          supervisorID,
	}

	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		_, err := runtime.ResolveClaimHandleTerminal(ctx, args, runtime.TerminalDecision{
			ClaimHandleID:   claimID,
			SupervisorID:    supervisorID,
			Source:          runtime.ActiveTerminal,
			Outcome:         runtime.OutcomeCommit,
			Producer:        store,
			Scope:           []byte(`"promotion-scope"`),
			Address:         []byte(`"promotion-addr"`),
			ProducerName:    producerName,
			CandidateHandle: candidateHandle,
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

	require.Empty(t, claimTerminalRowsFor(ctx, t, backend, claimID),
		"a promotion's lineage record must wait for the producer's commit response")

	delivered, err := runtime.FlushProducerVerbOutbox(ctx, args)
	require.NoError(t, err)
	require.Equal(t, 1, delivered)

	settled := claimTerminalRowsFor(ctx, t, backend, claimID)
	require.Len(t, settled, 1, "the ledger must hold exactly one claim_terminal record for the promotion")

	var rec runtime.ClaimTerminalRecord
	require.NoError(t, json.Unmarshal(settled[0].Record, &rec))
	require.Equal(t, "v-42", rec.VersionID,
		"the promotion's lineage record must carry the version the commit response returned")
	require.Equal(t, persistence.LineageOutcomeCommitted, rec.Outcome)
	require.Equal(t, supervisorID, rec.TerminatingSupervisorID)
	require.Equal(t, claimID, rec.ClaimHandleID)
}

func claimTerminalRowsFor(
	ctx context.Context, t *testing.T, backend persistence.Tables, claimID shared.UUID,
) []persistence.LineageRow {
	t.Helper()
	rows, err := backend.Lineage().GetByClaimHandleID(ctx, claimID)
	require.NoError(t, err)
	out := make([]persistence.LineageRow, 0, len(rows))
	for _, r := range rows {
		if r.RecordKind == persistence.LineageRecordKindClaimTerminal {
			out = append(out, r)
		}
	}
	return out
}

// @decision: promotion-lineage-record-after-commit
// @concept: lineage-record
func TestProducerVerbDispatch_ARetriedDeliveryLeavesOneClaimTerminalRecord(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgdbtest.OpenDriver(ctx, t)
	backend := d.Tables()

	tmpl := insertDeployedTemplate(ctx, t, backend, node.TemplateSpec{
		Name: "lineage-promotion-retry", Version: "1",
	})
	ck := "ck-lineage-promotion-retry"
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

	const supervisorID = "sup-lineage-promotion-retry"
	claimID := shared.UUID(uuid.New())
	producerName := "lineage-promotion-retry-store"
	intent := "rw"
	candidateHandle := []byte(`"candidate-retry"`)
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return backend.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID:                      claimID,
			LockKind:                persistence.LockKindScope,
			ProducerName:            &producerName,
			ClaimScopeData:          []byte(`"promotion-retry-scope"`),
			Address:                 []byte(`"promotion-retry-addr"`),
			Intent:                  &intent,
			HolderSupervisorID:      supervisorID,
			HolderNodeID:            workerNode.ID,
			ExpiresAt:               time.Now().Add(10 * time.Minute),
			NodeRunID:               &nodeRunID,
			ProducerCandidateHandle: candidateHandle,
		}, tx)
	}))

	reg := locks.NewRegistry()
	store := storetest.NewFake(producerName, claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
	})
	store.CommitResult = claimproducer.CommitResult{VersionID: "v-77"}
	reg.Add(producerName, store)

	provider, ok := backend.(interface {
		ProducerVerbOutbox() persistence.ProducerVerbOutboxTable
	})
	require.True(t, ok, "the driver must expose the producer-verb outbox")
	outbox := &deleteRefusingOutbox{ProducerVerbOutboxTable: provider.ProducerVerbOutbox(), refusals: 1}

	clock := shared.NewControllableClock(time.Now())
	args := runtime.RunArgs{
		Persist:               backend,
		ClaimHandles:          backend.ClaimHandles(),
		ClaimProducerRegistry: reg,
		Logger:                shared.SilentLogger{},
		Clock:                 clock,
		SupervisorID:          supervisorID,
		VerbOutbox:            outbox,
	}

	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		_, err := runtime.ResolveClaimHandleTerminal(ctx, args, runtime.TerminalDecision{
			ClaimHandleID:   claimID,
			SupervisorID:    supervisorID,
			Source:          runtime.ActiveTerminal,
			Outcome:         runtime.OutcomeCommit,
			Producer:        store,
			Scope:           []byte(`"promotion-retry-scope"`),
			Address:         []byte(`"promotion-retry-addr"`),
			ProducerName:    producerName,
			CandidateHandle: candidateHandle,
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

	dispatcher := runtime.NewProducerVerbDispatcher(outbox, backend, reg, clock, shared.SilentLogger{})

	delivered, err := dispatcher.DispatchOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, delivered, "a pass that cannot clear its outbox row delivers nothing")
	require.Empty(t, claimTerminalRowsFor(ctx, t, backend, claimID),
		"a pass that cannot clear its outbox row writes no lineage record")

	clock.Advance(2 * time.Minute)
	delivered, err = dispatcher.DispatchOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, delivered)

	settled := claimTerminalRowsFor(ctx, t, backend, claimID)
	require.Len(t, settled, 1, "a retried delivery leaves exactly one claim_terminal record")

	var rec runtime.ClaimTerminalRecord
	require.NoError(t, json.Unmarshal(settled[0].Record, &rec))
	require.Equal(t, "v-77", rec.VersionID)

	remaining, err := outbox.ListAll(ctx, nil)
	require.NoError(t, err)
	require.Empty(t, remaining, "the dispatcher deletes the delivered row")
}

type deleteRefusingOutbox struct {
	persistence.ProducerVerbOutboxTable
	refusals int
}

func (o *deleteRefusingOutbox) Delete(ctx context.Context, seq int64, tx persistence.Tx) error {
	if o.refusals > 0 {
		o.refusals--
		return fmt.Errorf("outbox delete refused on this attempt (seq=%d)", seq)
	}
	return o.ProducerVerbOutboxTable.Delete(ctx, seq, tx)
}
