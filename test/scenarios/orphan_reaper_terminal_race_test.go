// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scenarios

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/peer"
	stubstore "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/testfixture"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

type reaperRaceFixture struct {
	h        *scenario.Harness
	store    *stubstore.Store
	producer locks.ClaimProducer
	chID     uuid.UUID
	owner    string
	args     runtime.RunArgs
}

func startReaperRaceFixture(t *testing.T) *reaperRaceFixture {
	t.Helper()
	syncCaps := claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
	}
	endpoint, store, teardown := stubfixture.Start(t, stubstore.Config{Capabilities: syncCaps})
	t.Cleanup(teardown)

	h := scenario.Start(t, scenario.HarnessOpts{NoScheduler: true, NoSupervisor: true})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "orphan-reaper-terminal-race", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "worker", Executor: "stub"}),
		},
	})
	iid := h.CreateInstance(tid, "ck-reaper-terminal-race", map[string]any{})
	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)
	frameID := h.GetRunningFrameID(iid)
	mainScopeID := h.GetLatestFrameRootRunScopeID(iid)

	const owner = "owner-supervisor"
	h.ExecSQL(`DELETE FROM rimsky_node_runs WHERE node_id = $1`, worker.ID)
	runID := uuid.New()
	h.ExecSQL(
		`INSERT INTO rimsky_node_runs (id, node_id, executor_name, required_claim_producers, enqueued_at, frame_id, run_scope_id, state, creation_reason, sequence)
		 VALUES ($1, $2, 'stub', '{}', NOW() - INTERVAL '10 minutes', $3, $4, 'fresh', 'cascade', 1)`,
		runID, worker.ID, frameID, mainScopeID,
	)
	chID := uuid.New()
	h.ExecSQL(
		`INSERT INTO rimsky_claim_handles
		   (id, node_run_id, lock_kind, producer_name, claim_scope_data, address, intent,
		    is_held, holder_supervisor_id, holder_node_id, expires_at, frame_id, state)
		 VALUES ($1, $2, 'claim_scope', 'reap-store', '"@thing"', '"@thing"', 'rw',
		         FALSE, $3, $4, NOW() - INTERVAL '1 minute', $5, 'active')`,
		chID, runID, owner, worker.ID, frameID,
	)

	client, err := peer.Dial(h.Ctx, "reap-store", "grpc://"+endpoint, peer.TLSModeOff)
	require.NoError(t, err)
	t.Cleanup(client.Close)
	registry := locks.NewRegistry()
	registry.Add("reap-store", client)

	return &reaperRaceFixture{
		h:        h,
		store:    store,
		producer: client,
		chID:     chID,
		owner:    owner,
		args: runtime.RunArgs{
			Persist:       h.Persist,
			Queue:         h.Queue,
			ClaimHandles:  h.Persist.ClaimHandles(),
			StoreRegistry: registry,
			Clock:         shared.SystemClock{},
			Logger:        shared.SilentLogger{},
			SupervisorID:  owner,
		},
	}
}

func (f *reaperRaceFixture) releaseTerminal(t *testing.T, ctx context.Context) error {
	t.Helper()
	var post func(context.Context)
	if err := f.h.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		pc, err := runtime.ResolveClaimHandleTerminal(ctx, f.args, runtime.TerminalDecision{
			ClaimHandleID: f.chID,
			SupervisorID:  f.owner,
			Source:        runtime.ActiveTerminal,
			Outcome:       runtime.OutcomeCommit,
			Producer:      f.producer,
			Scope:         []byte(`"@thing"`),
			Address:       []byte(`"@thing"`),
			ProducerName:  "reap-store",
		}, tx)
		post = pc
		return err
	}); err != nil {
		return err
	}
	if post != nil {
		post(ctx)
	}
	if _, err := runtime.FlushProducerVerbOutbox(ctx, f.args); err != nil {
		return err
	}
	return nil
}

func (f *reaperRaceFixture) countStoreVerb(verb string) int {
	n := 0
	for _, c := range f.store.Calls() {
		if c.Verb == verb {
			n++
		}
	}
	return n
}

func (f *reaperRaceFixture) reapEvents(t *testing.T) int {
	t.Helper()
	var n int
	require.NoError(t, f.h.Pool.QueryRow(f.h.Ctx,
		`SELECT count(*) FROM rimsky_events
		  WHERE kind = 'lock_orphan_reaped' AND payload->>'claim_handle_id' = $1`,
		f.chID.String(),
	).Scan(&n))
	return n
}

func TestOrphanReaperVsTerminalRelease_ReleaseWinsInsideSweepWindow(t *testing.T) {
	t.Parallel()
	f := startReaperRaceFixture(t)

	var hooked atomic.Bool
	require.NoError(t, runtime.SweepOrphanedClaimHandles(f.h.Ctx, runtime.OrphanReaperArgs{
		Persist:      f.h.Persist,
		ClaimHandles: f.h.Persist.ClaimHandles(),
		Logger:       shared.SilentLogger{},
		PreReapHook: func(ctx context.Context, claimHandleID shared.UUID) {
			require.Equal(t, f.chID, uuid.UUID(claimHandleID),
				"the reaper's window must hold exactly the seeded expired row")
			require.Zero(t, f.countStoreVerb("commit"),
				"at hook time the terminal release has not fired yet")
			require.NoError(t, f.releaseTerminal(t, ctx),
				"the owning supervisor's terminal release must succeed inside the reaper's window")
			hooked.Store(true)
		},
	}))
	require.True(t, hooked.Load(), "the PreReapHook seam must have fired")

	var rowCount int
	var state string
	f.h.QueryRowSQL(`SELECT count(*) FROM rimsky_claim_handles WHERE id = $1`, []any{f.chID}, &rowCount)
	require.Equal(t, 1, rowCount, "the reaper must NOT have deleted the row the release already resolved")
	f.h.QueryRowSQL(`SELECT state FROM rimsky_claim_handles WHERE id = $1`, []any{f.chID}, &state)
	require.Equal(t, "committed", state, "the release's Promote is the single resolution")

	require.Equal(t, 1, f.countStoreVerb("commit"),
		"exactly one producer Commit — the terminal release's")
	require.Zero(t, f.countStoreVerb("abandon"), "no Abandon may fire")

	require.Zero(t, f.reapEvents(t),
		"no lock_orphan_reaped event may be emitted when the reaper lost the race")
}

func TestOrphanReaperVsTerminalRelease_ReaperWinsThenLateRelease(t *testing.T) {
	t.Parallel()
	f := startReaperRaceFixture(t)

	require.NoError(t, runtime.SweepOrphanedClaimHandles(f.h.Ctx, runtime.OrphanReaperArgs{
		Persist:      f.h.Persist,
		ClaimHandles: f.h.Persist.ClaimHandles(),
		Logger:       shared.SilentLogger{},
	}))
	var rowCount int
	f.h.QueryRowSQL(`SELECT count(*) FROM rimsky_claim_handles WHERE id = $1`, []any{f.chID}, &rowCount)
	require.Zero(t, rowCount, "the reaper must have deleted the expired row")
	require.Equal(t, 1, f.reapEvents(t), "the winning reaper emits exactly one lock_orphan_reaped event")
	require.Zero(t, f.countStoreVerb("commit"), "the reaper fires no producer verb")
	require.Zero(t, f.countStoreVerb("abandon"), "the reaper fires no producer verb")

	require.NoError(t, f.releaseTerminal(t, f.h.Ctx),
		"the losing terminal release must no-op cleanly, not error")
	f.h.QueryRowSQL(`SELECT count(*) FROM rimsky_claim_handles WHERE id = $1`, []any{f.chID}, &rowCount)
	require.Zero(t, rowCount, "the late release must not resurrect the reaped row")
	require.Equal(t, 1, f.reapEvents(t), "still exactly one lock_orphan_reaped event")
	require.Equal(t, 1, f.countStoreVerb("commit"),
		"exactly one producer Commit total — the release's at-least-once verb, none from the reaper")
}
