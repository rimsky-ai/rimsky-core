// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Forensics scenario — claim_abandon_lineage.
//
// Pin: a claim that resolves naturally as Abandon emits a
// `rimsky_lineage` row with record_kind = "claim_terminal" and
// outcome = "abandoned" (no Cause field). Distinct from a Commit
// resolution (which carries outcome = "committed") and distinct from a
// force-cancelled Abandon (which carries outcome = "force_cancelled"
// + a Cause discriminator).
//
// Spec: 2026-05-16 dispatch attached to plans/2026-05-15-data-platform-
// extensions-plan (lineage + events forensics extensions).
//
// @concept: claim-tree
// @concept: lineage

package lineage

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/fallguyconsulting/rimsky/foundation/locks"
	"github.com/fallguyconsulting/rimsky/foundation/locks/storetest"
	"github.com/fallguyconsulting/rimsky/foundation/persistence"
	"github.com/fallguyconsulting/rimsky/foundation/shared"
	"github.com/fallguyconsulting/rimsky/internal/pgtest"
	"github.com/fallguyconsulting/rimsky/runtime"
)

func TestClaimAbandonLineage_NaturalAbandonEmitsAbandonedOutcome(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
	backend := d.Tables()

	inst, frameID, claimHandleID, nodeID := seedAbandonScenario(ctx, t, backend, "abandon-natural")

	reg := locks.NewRegistry()
	store := storetest.NewFake("abandon-store", locks.Capabilities{
		WriteSemanticsAllowed: []locks.WriteSemantics{locks.WriteSemanticsSync},
	})
	reg.Add("abandon-store", store)
	args := runtime.RunArgs{
		Persist:       backend,
		ClaimHandles:  backend.ClaimHandles(),
		StoreRegistry: reg,
		Logger:        shared.SilentLogger{},
		SupervisorID:  "sup-AB",
		Clock:         shared.SystemClock{},
	}

	hint := runtime.ClaimLineageHint{
		InstanceID:   inst,
		FrameID:      frameID,
		RunID:        shared.UUID(uuid.New()),
		NodeID:       nodeID,
		ProducerName: "abandon-store",
	}

	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return runtime.ResolveClaimHandleTerminal(ctx, args, tx, runtime.TerminalDecision{
			ClaimHandleID: claimHandleID,
			SupervisorID:  args.SupervisorID,
			Source:        runtime.ActiveTerminal,
			Outcome:       runtime.AggregateAbandon,
			Producer:      store,
			Scope:         []byte(`"abandon-scope"`),
			Address:       []byte(`"abandon-addr"`),
			Lifetime:      "subgraph",
			ProducerName:  "abandon-store",
			LineageHint:   hint,
		})
	}))

	// Producer-side: one Abandon, no Commit.
	require.Equal(t, 1, countCallsOnID(store.Calls(), claimHandleID.String(), "abandon"),
		"natural Abandon must hit Producer.Abandon once")
	require.Equal(t, 0, countCallsOnID(store.Calls(), claimHandleID.String(), "commit"))

	// Lineage row: kind=claim_terminal, outcome=abandoned, no cause.
	rows, err := backend.Lineage().GetByClaimHandleID(ctx, claimHandleID)
	require.NoError(t, err)
	require.Len(t, rows, 1, "claim_terminal row must be present after Abandon")
	require.Equal(t, persistence.LineageRecordKindClaimTerminal, rows[0].RecordKind)
	require.Equal(t, persistence.LineageOutcomeAbandoned, rows[0].Outcome,
		"natural Abandon must record outcome=abandoned")

	var rec runtime.ClaimTerminalRecord
	require.NoError(t, json.Unmarshal(rows[0].Record, &rec))
	require.Equal(t, persistence.LineageOutcomeAbandoned, rec.Outcome)
	require.Empty(t, rec.Cause, "natural Abandon must not carry a Cause field")

	// Events: one claim_resolution.abandon, cause=natural.
	var page persistence.EventListResult
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		p, err := backend.Events().List(ctx, persistence.EventListFilter{
			InstanceID: &inst,
			Kind:       "claim_resolution.abandon",
		}, persistence.ListPagination{Limit: 10}, tx)
		page = p
		return err
	}))
	require.GreaterOrEqual(t, len(page.Events), 1,
		"claim_resolution.abandon event must be emitted")
	require.Equal(t, "natural", page.Events[0].Payload["cause"],
		"natural Abandon event must carry cause=natural")
}

// seedAbandonScenario inserts an instance + node + frame + node-run +
// claim-handle row and returns the identifiers needed to drive
// `ResolveClaimHandleTerminal`. The claim is NON-held (no
// rimsky_claim_holders rows) so the terminal engine routes through
// the direct Abandon path rather than via auto-terminal.
func seedAbandonScenario(
	ctx context.Context, t *testing.T, backend persistence.Tables, instanceKey string,
) (shared.UUID, shared.UUID, shared.UUID, shared.UUID) {
	t.Helper()
	tmpl := seedDeployedTemplate(ctx, t, backend, "abandon-tmpl")
	ik := instanceKey
	var inst persistence.InstanceRow
	var nodeRow persistence.NodeRow
	instID := shared.UUID(uuid.New())
	mainScopeID := shared.UUID(uuid.New())
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := backend.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID: mainScopeID, GraphName: "main", InstanceID: instID,
		}); err != nil {
			return err
		}
		i, err := backend.Instances().Create(ctx, persistence.InstanceCreateInput{
			ID: instID, TemplateHash: tmpl.ID, InstanceKey: &ik, Params: map[string]any{},
			MainRunScopeID: mainScopeID,
		}, tx)
		if err != nil {
			return err
		}
		inst = i
		n, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: inst.ID, NodeType: "writer", Executor: "stub",
		}, tx)
		if err != nil {
			return err
		}
		nodeRow = n
		return nil
	}))
	frameID := seedFrameRow(ctx, t, backend, inst.ID, nodeRow.ID)
	runID := seedRunRow(ctx, t, backend, nodeRow.ID, frameID)
	claimHandleID := shared.UUID(uuid.New())
	intent := "rw"
	producerName := "abandon-store"
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return backend.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID:                 claimHandleID,
			LockKind:           persistence.LockKindScope,
			ProducerName:       &producerName,
			ClaimScopeData:     []byte(`"abandon-scope"`),
			Address:            []byte(`"abandon-addr"`),
			Intent:             &intent,
			HolderSupervisorID: "sup-AB",
			HolderNodeID:       nodeRow.ID,
			ExpiresAt:          time.Now().Add(10 * time.Minute),
			NodeRunID:          &runID,
			FrameID:            &frameID,
			Lifetime:           "subgraph",
		}, tx)
	}))
	return inst.ID, frameID, claimHandleID, nodeRow.ID
}
