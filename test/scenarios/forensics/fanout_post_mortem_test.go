// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Forensics scenario — fanout_post_mortem.
//
// Pin: a fan-out parent with mixed-outcome children (some Commit, some
// natural Abandon) emits a complete forensics trail across both
// `rimsky_lineage` and `rimsky_events`:
//
//   - One `claim_terminal` lineage row per child (committed/abandoned)
//     plus one for the parent.
//   - One `claim_resolution.commit` event per committed child, one
//     `claim_resolution.abandon` per abandoned child, one for the parent.
//   - Note: `fanout.children_created` is fired by the dispatch path
//     (`runtime/fanout_dispatch.go::dispatchFanOutChildren`); this
//     scenario exercises the resolution path directly (skipping
//     dispatch) so the event is not expected here. The dispatch-side
//     event surface is exercised by the fan-out scenarios in
//     `test/scenarios/fanout/`.
//
// Spec: 2026-05-16 dispatch attached to plans/2026-05-15-data-platform-
// extensions-plan (lineage + events forensics extensions).
//
// @concept: fan-out
// @concept: claim-tree
// @concept: lineage

package forensics

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/fallguyconsulting/rimsky/foundation/locks"
	"github.com/fallguyconsulting/rimsky/foundation/locks/storetest"
	"github.com/fallguyconsulting/rimsky/foundation/persistence"
	"github.com/fallguyconsulting/rimsky/foundation/shared"
	"github.com/fallguyconsulting/rimsky/foundation/spec"
	"github.com/fallguyconsulting/rimsky/internal/pgtest"
	"github.com/fallguyconsulting/rimsky/runtime"
)

func TestFanoutPostMortem_MixedOutcomesEmitFullForensicsTrail(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
	backend := d.Tables()

	inst, frameID, parentRunID, parentNodeID := seedForensicsScenario(ctx, t, backend, "fanout-postmortem")
	reg := locks.NewRegistry()
	store := storetest.NewFake("postmortem-store", locks.Capabilities{
		WriteSemanticsAllowed: []locks.WriteSemantics{locks.WriteSemanticsSync},
	})
	reg.Add("postmortem-store", store)
	args := runtime.RunArgs{
		Persist:       backend,
		ClaimHandles:  backend.ClaimHandles(),
		StoreRegistry: reg,
		Logger:        shared.SilentLogger{},
		SupervisorID:  "sup-PM",
		Clock:         shared.SystemClock{},
	}

	// threshold(max_failures=1): tolerates one Abandon, requires the rest
	// to Commit. We exercise 4 children: 3 Commit + 1 Abandon → parent
	// Commits per threshold rule.
	parentID, subIDs := seedFanOutTree(ctx, t, backend, parentRunID, parentNodeID, frameID,
		"sup-PM", "postmortem-store", 4,
		spec.AggregationPolicy{Kind: spec.AggregationKindThreshold, MaxFailures: 1})

	// Drive sub[0..2] as Commit, sub[3] as Abandon. All resolutions
	// recurse upward via ParentClaimHandleID so the parent's aggregation
	// + counter bumps fire.
	commitOutcomes := []runtime.AggregateOutcome{
		runtime.AggregateCommit, runtime.AggregateCommit, runtime.AggregateCommit, runtime.AggregateAbandon,
	}
	for i, outcome := range commitOutcomes {
		i, outcome := i, outcome
		require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			return runtime.ResolveClaimHandleTerminal(ctx, args, tx, runtime.TerminalDecision{
				ClaimHandleID:       subIDs[i],
				SupervisorID:        args.SupervisorID,
				Source:              runtime.ActiveTerminal,
				Outcome:             outcome,
				Producer:            store,
				Scope:               []byte(`"sub-scope"`),
				Address:             []byte(`"sub-addr"`),
				Lifetime:            "subgraph",
				ProducerName:        "postmortem-store",
				ParentClaimHandleID: &parentID,
				LineageHint: runtime.ClaimLineageHint{
					InstanceID:   inst,
					FrameID:      frameID,
					RunID:        parentRunID,
					NodeID:       parentNodeID,
					ProducerName: "postmortem-store",
				},
			})
		}))
	}

	// Lineage coverage: every child + parent emits a claim_terminal row.
	for i, sid := range subIDs {
		rows, err := backend.Lineage().GetByClaimHandleID(ctx, sid)
		require.NoError(t, err)
		require.Len(t, rows, 1,
			"each child must emit exactly one claim_terminal row (idx %d)", i)
		want := persistence.LineageOutcomeCommitted
		if i == 3 {
			want = persistence.LineageOutcomeAbandoned
		}
		require.Equal(t, want, rows[0].Outcome,
			"child %d outcome: got %s want %s", i, rows[0].Outcome, want)
	}
	parentRows, err := backend.Lineage().GetByClaimHandleID(ctx, parentID)
	require.NoError(t, err)
	require.Len(t, parentRows, 1,
		"parent must emit exactly one claim_terminal row after all children resolve")
	// threshold(max_failures=1) with 3 commit + 1 abandon → Commit.
	require.Equal(t, persistence.LineageOutcomeCommitted, parentRows[0].Outcome,
		"threshold(max_failures=1) tolerates one abandon → parent Commits")

	// Events coverage: 3 commit + 1 abandon per child + 1 commit for the
	// parent.
	var commitPage persistence.EventListResult
	var abandonPage persistence.EventListResult
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		cp, err := backend.Events().List(ctx, persistence.EventListFilter{
			InstanceID: &inst,
			Kind:       "claim_resolution.commit",
		}, persistence.ListPagination{Limit: 50}, tx)
		if err != nil {
			return err
		}
		commitPage = cp
		ap, err := backend.Events().List(ctx, persistence.EventListFilter{
			InstanceID: &inst,
			Kind:       "claim_resolution.abandon",
		}, persistence.ListPagination{Limit: 50}, tx)
		if err != nil {
			return err
		}
		abandonPage = ap
		return nil
	}))
	require.Equal(t, 4, len(commitPage.Events),
		"3 child commits + 1 parent commit = 4 commit events")
	require.Equal(t, 1, len(abandonPage.Events),
		"1 child abandon = 1 abandon event")
	require.Equal(t, "natural", abandonPage.Events[0].Payload["cause"],
		"abandon event must carry cause=natural")
}

func seedForensicsScenario(
	ctx context.Context, t *testing.T, backend persistence.Tables, instanceKey string,
) (shared.UUID, shared.UUID, shared.UUID, shared.UUID) {
	t.Helper()
	tmpl := seedDeployedTemplate(ctx, t, backend, "forensics-tmpl")
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
			ID: shared.UUID(uuid.New()), InstanceID: inst.ID, NodeType: "fanout-parent", Executor: "stub",
		}, tx)
		if err != nil {
			return err
		}
		nodeRow = n
		return nil
	}))
	frameID := seedFrameRow(ctx, t, backend, inst.ID, nodeRow.ID)
	runID := seedRunRow(ctx, t, backend, nodeRow.ID, frameID)
	return inst.ID, frameID, runID, nodeRow.ID
}

// seedFanOutTree inserts a parent + n sub-claim rows with the
// supplied aggregation policy snapshotted on the parent.
//
// @source: test/scenarios/lineage/force_cancelled_lineage_test.go::seedFanOutTree
func seedFanOutTree(
	ctx context.Context, t *testing.T, backend persistence.Tables,
	parentRunID, parentNodeID, frameID shared.UUID,
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
			NodeRunID:          &parentRunID,
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
				NodeRunID:           &parentRunID,
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
