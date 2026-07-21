// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: claim-tree
// @concept: cancel-siblings
// @concept: lineage

package lineage

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
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
	"github.com/rimsky-ai/rimsky-core/test/support/pgdbtest"
)

func TestForceCancelledLineage_CancelSiblingsEmitsForceCancelledRows(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgdbtest.OpenDriver(ctx, t)
	backend := d.Tables()

	inst, frameID, parentNodeRunID, parentNodeID := seedForceCancelScenario(ctx, t, backend, "force-cancel")
	reg := locks.NewRegistry()
	store := storetest.NewFake("cancel-store", claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
	})
	reg.Add("cancel-store", store)
	args := runtime.RunArgs{
		Persist:       backend,
		ClaimHandles:  backend.ClaimHandles(),
		StoreRegistry: reg,
		Logger:        shared.SilentLogger{},
		SupervisorID:  "sup-FC",
		Clock:         shared.SystemClock{},
	}

	parentID, subIDs := seedFanOutTree(ctx, t, backend, parentNodeRunID, parentNodeID, frameID,
		"sup-FC", "cancel-store", 3,
		spec.AggregationPolicy{Kind: spec.AggregationKindStrict})

	var post func(context.Context)
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		pc, err := runtime.ResolveClaimHandleTerminal(ctx, args, runtime.TerminalDecision{
			ClaimHandleID:       subIDs[0],
			SupervisorID:        args.SupervisorID,
			Source:              runtime.ActiveTerminal,
			Outcome:             runtime.OutcomeAbandon,
			Producer:            store,
			Scope:               []byte(`"sub-scope"`),
			Address:             []byte(`"sub-addr"`),
			Lifetime:            "subgraph",
			ProducerName:        "cancel-store",
			ParentClaimHandleID: &parentID,
			LineageHint: runtime.ClaimLineageHint{
				InstanceID:   inst,
				FrameID:      frameID,
				NodeRunID:    parentNodeRunID,
				NodeID:       parentNodeID,
				ProducerName: "cancel-store",
			},
		}, tx)
		post = pc
		return err
	}))
	if post != nil {
		post(ctx)
	}
	_, ferr := runtime.FlushProducerVerbOutbox(ctx, args)
	require.NoError(t, ferr)

	for i, sid := range subIDs {
		require.Equal(t, 1, countCallsOnID(store.Calls(), sid.String(), "abandon"),
			"sub-claim %d must receive exactly one Abandon", i)
	}
	require.Equal(t, 1, countCallsOnID(store.Calls(), parentID.String(), "abandon"),
		"parent claim must receive its own Abandon (aggregator decision)")

	verifyLineageOutcome(ctx, t, backend, subIDs[0], persistence.LineageOutcomeAbandoned, "")
	verifyLineageOutcome(ctx, t, backend, subIDs[1], persistence.LineageOutcomeForceCancelled, "sibling_failed")
	verifyLineageOutcome(ctx, t, backend, subIDs[2], persistence.LineageOutcomeForceCancelled, "sibling_failed")
	verifyLineageOutcome(ctx, t, backend, parentID, persistence.LineageOutcomeAbandoned, "")

	var page persistence.EventListResult
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		p, err := backend.Events().List(ctx, persistence.EventListFilter{
			InstanceID: &inst,
			KindIn:     []string{"claim_resolution.abandon"},
		}, persistence.ListPagination{Limit: 50}, tx)
		page = p
		return err
	}))
	cancelCount := 0
	naturalCount := 0
	for _, ev := range page.Events {
		switch ev.Payload["cause"] {
		case "sibling_failed":
			cancelCount++
		case "natural":
			naturalCount++
		}
	}
	require.Equal(t, 2, cancelCount, "two siblings must emit cause=sibling_failed events")
	require.GreaterOrEqual(t, naturalCount, 1, "the triggering child + parent emit cause=natural events")
}

func verifyLineageOutcome(
	ctx context.Context, t *testing.T, backend persistence.Tables,
	claimID shared.UUID, expectedOutcome, expectedCause string,
) {
	t.Helper()
	rows, err := backend.Lineage().GetByClaimHandleID(ctx, claimID)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(rows), 1, "claim_terminal row must exist for %s", claimID)
	row := rows[len(rows)-1]
	require.Equal(t, persistence.LineageRecordKindClaimTerminal, row.RecordKind,
		"row for %s must be claim_terminal", claimID)
	require.Equal(t, expectedOutcome, row.Outcome,
		"outcome for %s: got %q want %q", claimID, row.Outcome, expectedOutcome)
	if expectedCause == "" {
		return
	}
	var rec runtime.ClaimTerminalRecord
	require.NoError(t, json.Unmarshal(row.Record, &rec))
	require.Equal(t, expectedCause, rec.Cause,
		"cause for %s: got %q want %q", claimID, rec.Cause, expectedCause)
}

func seedForceCancelScenario(
	ctx context.Context, t *testing.T, backend persistence.Tables, instanceKey string,
) (shared.UUID, shared.UUID, shared.UUID, shared.UUID) {
	t.Helper()
	tmpl := seedDeployedTemplate(ctx, t, backend, "force-cancel-tmpl")
	ik := instanceKey
	var inst persistence.InstanceRow
	var nodeRow persistence.NodeRow
	instID := shared.UUID(uuid.New())
	mainScopeID := shared.UUID(uuid.New())
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := backend.RunScopes().Create(ctx, persistence.RunScopeRow{
			ID: mainScopeID, GraphName: "main", InstanceID: instID,
		}, tx); err != nil {
			return err
		}
		i, err := backend.Instances().Create(ctx, persistence.InstanceCreateInput{
			ID: instID, TemplateHash: tmpl.ID, InstanceKey: &ik, Params: map[string]any{},
		}, tx)
		if err != nil {
			return err
		}
		inst = i
		n, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: inst.ID, NodeType: "parent", Executor: "stub",
		}, tx)
		if err != nil {
			return err
		}
		nodeRow = n
		return nil
	}))
	frameID := seedFrameRow(ctx, t, backend, inst.ID, nodeRow.ID, mainScopeID)
	runID := seedRunRow(ctx, t, backend, nodeRow.ID, frameID)
	return inst.ID, frameID, runID, nodeRow.ID
}

func seedFanOutTree(
	ctx context.Context, t *testing.T, backend persistence.Tables,
	parentNodeRunID, parentNodeID, frameID shared.UUID,
	supervisorID, producerName string, n int,
	policy spec.AggregationPolicy,
) (shared.UUID, []shared.UUID) {
	t.Helper()
	parentID := shared.UUID(uuid.New())
	subIDs := make([]shared.UUID, 0, n)
	policyBytes, mErr := persistence.MarshalAggregationPolicy(policy)
	require.NoError(t, mErr)
	intent := "rw"
	pName := producerName
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := backend.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID:                 parentID,
			LockKind:           persistence.LockKindScope,
			ProducerName:       &pName,
			ClaimScopeData:     []byte(`"parent-scope"`),
			Address:            []byte(`"parent-addr"`),
			Intent:             &intent,
			HolderSupervisorID: supervisorID,
			HolderNodeID:       parentNodeID,
			ExpiresAt:          time.Now().Add(10 * time.Minute),
			NodeRunID:          &parentNodeRunID,
			FrameID:            &frameID,
			AggregationPolicy:  policyBytes,
		}, tx); err != nil {
			return err
		}
		for i := 0; i < n; i++ {
			sid := shared.UUID(uuid.New())
			parent := parentID
			if err := backend.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
				ID:                  sid,
				LockKind:            persistence.LockKindScope,
				ProducerName:        &pName,
				ClaimScopeData:      []byte(`"sub-scope"`),
				Address:             []byte(`"sub-addr"`),
				Intent:              &intent,
				HolderSupervisorID:  supervisorID,
				HolderNodeID:        parentNodeID,
				ExpiresAt:           time.Now().Add(10 * time.Minute),
				NodeRunID:           &parentNodeRunID,
				FrameID:             &frameID,
				ParentClaimHandleID: &parent,
			}, tx); err != nil {
				return err
			}
			subIDs = append(subIDs, sid)
			if err := backend.ClaimHandles().BumpExpectedChildrenCount(ctx, parentID, supervisorID, 1, tx); err != nil {
				return err
			}
		}
		return nil
	}))
	return parentID, subIDs
}
