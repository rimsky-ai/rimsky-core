// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package scenarios

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/action"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/service"
	stubstore "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/testfixture"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestVerifyBeforeRun_BailResolvesThroughEngine(t *testing.T) {
	t.Parallel()

	syncCaps := claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
	}

	endpointA, storeA, teardownA := stubfixture.Start(t, stubstore.Config{
		Capabilities: syncCaps,
		PickPolicies: map[string]stubstore.PickPolicyConfig{
			"@items": {
				OnCommit:     action.Action{Kind: action.Pop},
				OnGiveUp:     action.Action{Kind: action.Recycle},
				InitialItems: []json.RawMessage{json.RawMessage(`{"k":"v"}`)},
			},
		},
	})
	t.Cleanup(teardownA)

	h := scenario.Start(t, scenario.HarnessOpts{
		NoSupervisor: true,
		ClaimProducers: config.RemoteClaimProducersConfig{
			ClaimProducers: map[string]config.ClaimProducerEntry{
				"store-a": {Endpoint: "grpc://" + endpointA, Capabilities: syncCaps},
			},
		},
	})
	h.Stub.WhenType("worker").Success(map[string]any{}, true, "should-not-run")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "bail-engine-route", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "worker", Executor: "stub"},
				scenario.WithClaimProducers(scenario.WriteClaimRef("store-a", "@items")),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-bail-engine-"+uuid.NewString()[:8], map[string]any{})

	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)
	h.WaitForDispatchCount(worker.ID, 1)

	var nodeRunID uuid.UUID
	require.NoError(t, h.Pool.QueryRow(h.Ctx,
		`SELECT id FROM rimsky_node_runs WHERE node_id = $1`, worker.ID,
	).Scan(&nodeRunID))

	decoyID := uuid.New()
	decoyName := "bail-decoy-lock"
	require.NoError(t, h.Persist.Transaction(h.Ctx, func(ctx context.Context, tx persistence.Tx) error {
		return h.Persist.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID:                 decoyID,
			LockKind:           persistence.LockKindNamed,
			LockName:           &decoyName,
			HolderSupervisorID: "other-supervisor",
			HolderNodeID:       worker.ID,
			ExpiresAt:          time.Now().Add(10 * time.Minute),
		}, tx)
	}))

	clientA, err := service.Dial(h.Ctx, "store-a", "grpc://"+endpointA, service.TLSModeOff)
	require.NoError(t, err)
	t.Cleanup(clientA.Close)
	registry := locks.NewRegistry()
	registry.Add("store-a", clientA)

	pool := executor.NewClientPool()
	t.Cleanup(func() { _ = pool.Close() })

	var stolen atomic.Bool
	args := runtime.RunArgs{
		Persist:               h.Persist,
		Queue:                 h.Queue,
		ClaimHandles:          h.Persist.ClaimHandles(),
		AdvisoryLocker:        h.Driver.AdvisoryLocker(),
		ClaimProducerRegistry: registry,
		Clock:                 shared.SystemClock{},
		Logger:                shared.SilentLogger{},
		SupervisorID:          "scenario-runner",
		Pool:                  pool,
		Resolver: executor.NewStaticResolver(map[string]executor.Endpoint{
			"stub": {Transport: "grpc", URL: h.StubAddr},
		}),
		LivenessInterval: 100 * time.Millisecond,
		PostCommitHook: func(ctx context.Context) {
			var held int
			require.NoError(t, h.Pool.QueryRow(ctx,
				`SELECT count(*) FROM rimsky_claim_handles
				  WHERE holder_node_id = $1 AND holder_supervisor_id = 'scenario-runner'`,
				worker.ID,
			).Scan(&held))
			require.Equal(t, 1, held,
				"at hook time the committed acquisition's claim-handle row must exist, held by this supervisor")
			require.Zero(t, countCalls(storeA.Calls(), "abandon"),
				"at hook time the producer must not have seen an Abandon yet (verb fires in the bail, after the steal)")
			tag, uerr := h.Pool.Exec(ctx,
				`UPDATE rimsky_node_runs SET claimed_by = 'thief-supervisor', claimed_at = NOW() WHERE id = $1`,
				nodeRunID,
			)
			require.NoError(t, uerr)
			require.Equal(t, int64(1), tag.RowsAffected(),
				"post-commit hook should flip ownership of exactly the committed dispatch row")
			stolen.Store(true)
		},
	}

	out, err := runtime.RunNode(h.Ctx, args, nil)
	require.NoError(t, err)
	require.True(t, stolen.Load(),
		"post-commit hook must have fired — the acquisition tx committed and the verify window was reached")
	require.False(t, out.Ran,
		"verify-before-run must bail (Ran=false) when the claim was stolen between commit and the verify-read")

	_, ferr := runtime.FlushProducerVerbOutbox(h.Ctx, args)
	require.NoError(t, ferr)

	callsA := storeA.Calls()
	require.Equal(t, 1, countCalls(callsA, "open"),
		"store-a must have been Open'd exactly once")
	require.Equal(t, 1, countCalls(callsA, "abandon"),
		"the bail must fire exactly one Abandon per acquired claim — no double-fire, no skip")
	var openClaimID, abandonClaimID string
	for _, c := range callsA {
		switch c.Verb {
		case "open":
			openClaimID = c.ClaimID
		case "abandon":
			abandonClaimID = c.ClaimID
		}
	}
	require.Equal(t, openClaimID, abandonClaimID,
		"the Abandon must target the claim the Open minted")
	require.Zero(t, countCalls(callsA, "commit"),
		"store-a must never see a Commit for a bailed acquisition")

	var survivors []uuid.UUID
	rows, qerr := h.Pool.Query(h.Ctx,
		`SELECT id FROM rimsky_claim_handles WHERE holder_node_id = $1`, worker.ID)
	require.NoError(t, qerr)
	for rows.Next() {
		var id uuid.UUID
		require.NoError(t, rows.Scan(&id))
		survivors = append(survivors, id)
	}
	rows.Close()
	require.NoError(t, rows.Err())
	require.Equal(t, []uuid.UUID{decoyID}, survivors,
		"the bail's own claim-handle row must be deleted (no abandoned-state residue) and the other supervisor's decoy must survive — the deletion is claimant-guarded")

	var latest *persistence.NodeRunLatest
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, gerr := h.Persist.Nodes().GetLatestRunForNode(h.Ctx, worker.ID, tx)
		latest = r
		return gerr
	}))
	require.NotNil(t, latest)
	require.Equal(t, cascade.NodeStateStale, latest.State,
		"node must remain stale — the bail fired before the running transition / dispatch")

	var signalCount int
	require.NoError(t, h.Pool.QueryRow(h.Ctx,
		`SELECT count(*) FROM rimsky_events
		  WHERE node_id = $1 AND (kind LIKE 'terminal/%' OR kind LIKE 'claim_resolution%')`,
		worker.ID,
	).Scan(&signalCount))
	require.Zero(t, signalCount,
		"the bail is an admin path — it must emit no terminal/* signal and no claim_resolution.* forensics")

	var orphanCount int
	require.NoError(t, h.Pool.QueryRow(h.Ctx,
		`SELECT count(*) FROM rimsky_events
		  WHERE kind = 'orphaned_claim_lost_race'
		    AND payload->>'dispatch_id' = $1`,
		nodeRunID.String(),
	).Scan(&orphanCount))
	require.Equal(t, 1, orphanCount,
		"the bail must emit exactly one orphaned_claim_lost_race event for the stolen dispatch")

	require.Empty(t, h.Stub.Observed(),
		"the executor must not be invoked when the dispatch was stolen pre-verify")
}
