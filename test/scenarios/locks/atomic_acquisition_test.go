// Substantive scenario coverage for the lock-acquisition path under
// stores-redesign-v2.
//
// Two tests:
//
//   - TestLockHolderRowDeletedAfterTerminal — drives the supervisor
//     through one full execute cycle and asserts the per-spec
//     lock-holder row is deleted at terminal time. Targets the
//     claimant-guarded release predicate (blessed invariant 4) and the
//     "lock state lives only in postgres" invariant (9a).
//
//   - TestAcquisitionTxRollsBackOnStoreOpenError — fault-injects an
//     error from Store.Open inside the acquisition tx and asserts the
//     tx rolled back: no rimsky_lock_holders row was committed for the
//     candidate, and rimsky_dispatch.claimed_by is still NULL.
//     Targets blessed invariant 10 (atomic acquisition).
package locks

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/scenario"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/store"
)

// TestLockHolderRowDeletedAfterTerminal drives one node through a
// complete execute cycle and asserts the per-spec lock-holder rows are
// deleted at terminal time (claimant-guarded release per blessed
// invariant 4).
//
// We use a stub store because the harness ships factories for it and
// the node-template grammar requires every claim's store to be in the
// configured registry.
func TestLockHolderRowDeletedAfterTerminal(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{
		StoresConfig: store.StoresConfig{Stores: map[string]map[string]any{
			"local-fs": {"kind": "stub_filesystem"},
		}},
	})
	h.Stub.WhenType("worker").Complete(map[string]any{}, true, "ok")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "claim-released", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "worker", Executor: "stub"},
				scenario.WithStores(scenario.WriteClaimRef("local-fs", "tenant-A/path")),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-claim-released", map[string]any{})

	w := h.FindNode(iid, "worker")
	require.NotNil(t, w)
	require.True(t, h.WaitForNodeState(w.ID, shared.NodeStateFresh, 15*time.Second))

	// Post-terminal, the lock-holder row for this node must be gone
	// (release tx commits the DELETE alongside the state transition).
	rows, err := h.Storage.LockHolders().ListByHolderNode(h.Ctx, w.ID, nil)
	require.NoError(t, err)
	require.Empty(t, rows, "lock-holder rows should be cleared at terminal")
}

// TestAcquisitionTxRollsBackOnStoreOpenError exercises blessed
// invariant 10: the §13.3 acquisition transaction either claims the
// dispatch row AND inserts every required rimsky_lock_holders row AND
// completes every Store.Open mutation, or none of these.
//
// We register a fault-injecting store factory whose Open returns an
// error on every call. The supervisor enqueues one node that needs the
// store; the acquisition tx attempts to insert a lock-holder row, then
// calls Store.Open, gets the error, and rolls back. We then assert:
//   - no rimsky_lock_holders row exists for the candidate node
//   - rimsky_dispatch.claimed_by IS NULL (the row is unclaimed and
//     ready for re-acquisition on the next tick)
//
// If the acquisition path were not atomic (e.g. lock-holder row
// committed before Open ran), the lock-holder row would persist after
// rollback and this test would fail.
func TestAcquisitionTxRollsBackOnStoreOpenError(t *testing.T) {
	t.Parallel()
	calls := &atomic.Int64{}
	h := scenario.Start(t, scenario.HarnessOpts{
		ExtraStoreFactories: []store.Factory{openErrorFactory{calls: calls}},
		StoresConfig: store.StoresConfig{Stores: map[string]map[string]any{
			"flaky-fs": {"kind": "open_error_store"},
		}},
	})
	// The node never actually runs (Open fails inside the acquisition
	// tx), so no executor stub registration is required.

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "atomic-acq-rollback", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "worker", Executor: "stub"},
				scenario.WithStores(scenario.WriteClaimRef("flaky-fs", "tenant-A/path")),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-atomic-acq-rollback", map[string]any{})

	w := h.FindNode(iid, "worker")
	require.NotNil(t, w)

	// Wait for the supervisor to attempt acquisition at least once.
	// open_error_store.Open is the inner-most call; we know acquisition
	// reached step 4 once `calls` is non-zero.
	require.Eventually(t, func() bool {
		return calls.Load() > 0
	}, 10*time.Second, 50*time.Millisecond,
		"supervisor should have attempted acquisition (Store.Open) at least once")

	// Atomicity assertion 1: no lock-holder row was committed for the
	// candidate node. If the acquisition tx weren't atomic with
	// Store.Open, the Insert that ran before Open would have
	// committed and we'd see a row here.
	rows, err := h.Storage.LockHolders().ListByHolderNode(h.Ctx, w.ID, nil)
	require.NoError(t, err)
	require.Empty(t, rows,
		"acquisition tx must roll back the lock-holder Insert when Open errors (blessed invariant 10)")

	// Atomicity assertion 2: every dispatch row for this node has
	// claimed_by IS NULL. The ClaimDispatchRow UPDATE that ran inside
	// the same tx as the failed Open must have rolled back too.
	rowsClaimed := 0
	require.NoError(t, h.Pool.QueryRow(h.Ctx,
		`SELECT count(*) FROM rimsky_dispatch WHERE node_id = $1 AND claimed_by IS NOT NULL`,
		w.ID,
	).Scan(&rowsClaimed))
	require.Equal(t, 0, rowsClaimed,
		"acquisition tx must roll back the dispatch claim when Open errors (blessed invariant 10)")
}

// openErrorFactory builds an in-process store whose Open always errors.
// Used by TestAcquisitionTxRollsBackOnStoreOpenError to drive the
// rollback path.
type openErrorFactory struct {
	calls *atomic.Int64
}

func (openErrorFactory) Kind() string                            { return "open_error_store" }
func (openErrorFactory) MaxWriteSemantics() store.WriteSemantics { return store.WriteSemanticsDirect }

func (f openErrorFactory) Build(name string, _ map[string]any) (store.Store, error) {
	return &openErrorStore{name: name, calls: f.calls}, nil
}

// openErrorStore satisfies store.Store. Open errors; the other verbs
// are unreachable on the rollback path (the supervisor never reaches
// terminal state for a node whose acquisition rolls back).
type openErrorStore struct {
	name  string
	calls *atomic.Int64
}

func (s *openErrorStore) Name() string { return s.name }
func (*openErrorStore) Kind() string   { return "open_error_store" }
func (*openErrorStore) Capabilities() store.Capabilities {
	return store.Capabilities{WriteSemantics: store.WriteSemanticsDirect}
}
func (*openErrorStore) RegionsConflict(a, b []byte) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	return string(a) == string(b)
}
func (*openErrorStore) UnmarshalRegion(raw []byte) ([]byte, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := make([]byte, len(raw))
	copy(out, raw)
	return out, nil
}
func (s *openErrorStore) Open(_ context.Context, _ store.ClaimSpec) (store.ClaimResult, error) {
	s.calls.Add(1)
	return store.ClaimResult{}, errors.New("open_error_store: synthetic error for atomicity test")
}
func (*openErrorStore) Commit(_ context.Context, _ []byte, _ []byte, _ string) error  { return nil }
func (*openErrorStore) Abandon(_ context.Context, _ []byte, _ []byte, _ string) error { return nil }
func (*openErrorStore) Delete(_ context.Context, _ []byte) error                      { return nil }
func (*openErrorStore) Release(_ context.Context, _ []byte, _ []byte) error           { return nil }

// Compile-time interface assertion.
var _ store.Store = (*openErrorStore)(nil)
