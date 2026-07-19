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

	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks/storetest"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	_ "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
)

func openLifecycleFanoutTestTables(t *testing.T) persistence.Tables {
	t.Helper()
	ctx := context.Background()
	d, err := persistence.Open(ctx, persistence.Config{
		Driver: "sqlite",
		SQLite: &persistence.SQLiteConfig{Path: filepath.Join(t.TempDir(), "state.db")},
	})
	require.NoError(t, err)
	require.NoError(t, d.Migrate(ctx, shared.SilentLogger{}))
	t.Cleanup(func() { _ = d.Close() })
	return d.Tables()
}

func fanOutRunScopeEventInTx(
	t *testing.T, persist persistence.Tables, lifecycleSubs *locks.LifecycleRegistry,
	peersForSpec func(node.TemplateSpec) []string, tplSpec node.TemplateSpec,
	runScopeID, instanceID shared.UUID, terminalReason string,
) {
	t.Helper()
	require.NoError(t, persist.Transaction(context.Background(), func(ctx context.Context, tx persistence.Tx) error {
		FanOutRunScopeEvent(ctx, persist, lifecycleSubs, peersForSpec, tplSpec, runScopeID, instanceID, terminalReason, tx)
		return nil
	}))
}

func TestFanOutRunScopeEvent_ReplayIsNoOp(t *testing.T) {
	persist := openLifecycleFanoutTestTables(t)

	fake := storetest.NewFake("peer-a", claimproducer.Capabilities{})
	lcReg := locks.NewLifecycleRegistry()
	lcReg.Add("peer-a", fake)
	peersForSpec := func(node.TemplateSpec) []string { return []string{"peer-a"} }
	tplSpec := node.TemplateSpec{Name: "fanout-replay", Version: "v1"}

	runScopeID := shared.UUID(uuid.New())
	instanceID := shared.UUID(uuid.New())

	fanOutRunScopeEventInTx(t, persist, lcReg, peersForSpec, tplSpec, runScopeID, instanceID, "subgraph_exit")
	require.Len(t, fake.Calls(), 1, "the first fan-out must deliver on_run_scope_terminal exactly once")
	require.Equal(t, "on_run_scope_terminal", fake.Calls()[0].Verb)

	fanOutRunScopeEventInTx(t, persist, lcReg, peersForSpec, tplSpec, runScopeID, instanceID, "subgraph_exit")
	require.Len(t, fake.Calls(), 1,
		"replaying a fan-out for the same (peer, run-scope) pair must be a no-op: the idempotency row from "+
			"the first delivery is already at run-scope-terminal, so no second downstream delivery may occur")
}

func TestFanOutRunScopeEvent_DistinctRunScopesEachDeliverOnce(t *testing.T) {
	persist := openLifecycleFanoutTestTables(t)

	fake := storetest.NewFake("peer-a", claimproducer.Capabilities{})
	lcReg := locks.NewLifecycleRegistry()
	lcReg.Add("peer-a", fake)
	peersForSpec := func(node.TemplateSpec) []string { return []string{"peer-a"} }
	tplSpec := node.TemplateSpec{Name: "fanout-distinct", Version: "v1"}
	instanceID := shared.UUID(uuid.New())

	scopeA := shared.UUID(uuid.New())
	scopeB := shared.UUID(uuid.New())

	fanOutRunScopeEventInTx(t, persist, lcReg, peersForSpec, tplSpec, scopeA, instanceID, "subgraph_exit")
	fanOutRunScopeEventInTx(t, persist, lcReg, peersForSpec, tplSpec, scopeB, instanceID, "subgraph_exit")

	require.Len(t, fake.Calls(), 2,
		"two distinct run scopes for the same peer must each deliver independently; "+
			"idempotency must be keyed by run-scope id, not collapsed across scopes")
}
