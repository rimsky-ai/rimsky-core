// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/lifecycle"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks/storetest"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	_ "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
)

func openLifecycleFanoutTestDB(t *testing.T) persistence.Database {
	t.Helper()
	ctx := context.Background()
	d, err := persistence.Open(ctx, persistence.Config{
		Driver: "sqlite",
		SQLite: &persistence.SQLiteConfig{Path: filepath.Join(t.TempDir(), "state.db")},
	})
	require.NoError(t, err)
	require.NoError(t, d.Migrate(ctx, shared.SilentLogger{}))
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func fanOutRunScopeEventInTx(
	t *testing.T, d persistence.Database, lifecycleSubs *lifecycle.Registry,
	peersForSpec func(node.TemplateSpec) []string, tplSpec node.TemplateSpec,
	runScopeID, instanceID shared.UUID, terminalReason string,
) {
	t.Helper()
	persist := d.Tables()
	require.NoError(t, persist.Transaction(context.Background(), func(ctx context.Context, tx persistence.Tx) error {
		FanOutRunScopeEvent(ctx, persist, d.AdvisoryLocker(), lifecycleSubs, peersForSpec, tplSpec, runScopeID, instanceID, terminalReason, nil, tx)
		return nil
	}))
}

func TestFanOutRunScopeEvent_ReplayIsNoOp(t *testing.T) {
	d := openLifecycleFanoutTestDB(t)

	fake := storetest.NewFake("peer-a", claimproducer.Capabilities{})
	lcReg := lifecycle.NewRegistry()
	lcReg.Add("peer-a", fake)
	peersForSpec := func(node.TemplateSpec) []string { return []string{"peer-a"} }
	tplSpec := node.TemplateSpec{Name: "fanout-replay", Version: "v1"}

	runScopeID := shared.UUID(uuid.New())
	instanceID := shared.UUID(uuid.New())

	fanOutRunScopeEventInTx(t, d, lcReg, peersForSpec, tplSpec, runScopeID, instanceID, "subgraph_exit")
	require.Len(t, fake.Calls(), 1, "the first fan-out must deliver on_run_scope_terminal exactly once")
	require.Equal(t, "on_run_scope_terminal", fake.Calls()[0].Verb)

	fanOutRunScopeEventInTx(t, d, lcReg, peersForSpec, tplSpec, runScopeID, instanceID, "subgraph_exit")
	require.Len(t, fake.Calls(), 1,
		"replaying a fan-out for the same (peer, run-scope) pair must be a no-op: the idempotency row from "+
			"the first delivery is already at run-scope-terminal, so no second downstream delivery may occur")
}

func TestFanOutRunScopeEvent_DistinctRunScopesEachDeliverOnce(t *testing.T) {
	d := openLifecycleFanoutTestDB(t)

	fake := storetest.NewFake("peer-a", claimproducer.Capabilities{})
	lcReg := lifecycle.NewRegistry()
	lcReg.Add("peer-a", fake)
	peersForSpec := func(node.TemplateSpec) []string { return []string{"peer-a"} }
	tplSpec := node.TemplateSpec{Name: "fanout-distinct", Version: "v1"}
	instanceID := shared.UUID(uuid.New())

	scopeA := shared.UUID(uuid.New())
	scopeB := shared.UUID(uuid.New())

	fanOutRunScopeEventInTx(t, d, lcReg, peersForSpec, tplSpec, scopeA, instanceID, "subgraph_exit")
	fanOutRunScopeEventInTx(t, d, lcReg, peersForSpec, tplSpec, scopeB, instanceID, "subgraph_exit")

	require.Len(t, fake.Calls(), 2,
		"two distinct run scopes for the same peer must each deliver independently; "+
			"idempotency must be keyed by run-scope id, not collapsed across scopes")
}

func TestFanOutRunScopeEvent_NilTxCommitsIndependently(t *testing.T) {
	d := openLifecycleFanoutTestDB(t)

	fake := storetest.NewFake("peer-a", claimproducer.Capabilities{})
	lcReg := lifecycle.NewRegistry()
	lcReg.Add("peer-a", fake)
	peersForSpec := func(node.TemplateSpec) []string { return []string{"peer-a"} }
	tplSpec := node.TemplateSpec{Name: "fanout-niltx", Version: "v1"}

	runScopeID := shared.UUID(uuid.New())
	instanceID := shared.UUID(uuid.New())

	FanOutRunScopeEvent(context.Background(), d.Tables(), d.AdvisoryLocker(), lcReg, peersForSpec, tplSpec,
		runScopeID, instanceID, "subgraph_exit", nil, nil)
	require.Len(t, fake.Calls(), 1,
		"a nil tx must still deliver to the peer, managing its own short transactions for the idempotency read/write")

	require.NoError(t, d.Tables().Transaction(context.Background(), func(ctx context.Context, tx persistence.Tx) error {
		row, err := d.Tables().LifecycleIdempotency().Get(ctx, "peer-a",
			persistence.LifecycleIdempotencyScopeRunScope, runScopeID.String(), tx)
		require.NoError(t, err)
		require.NotNil(t, row, "the idempotency upsert must be durably committed by the time FanOutRunScopeEvent returns, "+
			"independent of any caller transaction that may later fail — a nil tx must not leave the row uncommitted")
		require.Equal(t, persistence.LifecycleIdempotencyStateRunScopeTerminal, row.State)
		return nil
	}))
}

func TestFanOutRunScopeEventPostCommit_DeliversToPeer(t *testing.T) {
	d := openLifecycleFanoutTestDB(t)

	fake := storetest.NewFake("peer-a", claimproducer.Capabilities{})
	lcReg := lifecycle.NewRegistry()
	lcReg.Add("peer-a", fake)
	tplSpec := node.TemplateSpec{Name: "fanout-postcommit", Version: "v1"}
	runScopeID := shared.UUID(uuid.New())
	instanceID := shared.UUID(uuid.New())

	args := RunArgs{
		Persist:               d.Tables(),
		AdvisoryLocker:        d.AdvisoryLocker(),
		LifecycleSubs:         lcReg,
		LifecyclePeersForSpec: func(node.TemplateSpec) []string { return []string{"peer-a"} },
		Logger:                shared.SilentLogger{},
	}

	pc := fanOutRunScopeEventPostCommit(args, tplSpec, runScopeID, instanceID, "fanout_partition_terminal")
	require.NotNil(t, pc, "SettleFromFanoutChild/SettleFromDelegate must be able to chain this into their returned postCommitFn")
	require.Empty(t, fake.Calls(), "constructing the post-commit closure must not deliver anything before it is invoked")

	pc(context.Background())
	require.Len(t, fake.Calls(), 1,
		"invoking the returned postCommitFn (as callers do only after their settle transaction has committed) "+
			"must deliver on_run_scope_terminal to the peer exactly once")
}
