// Substantive scenario coverage for the §14.4 auto-terminal mechanism
// under stores-redesign-v2: when all members of a held subgraph reach
// a terminal state, exactly one resolution fires (aggregate-outcome:
// any-failed → on_give_up; all-completed → on_commit) and the
// lock-holder row is deleted, cascading the claim-holders rows.
//
// Targets blessed invariant 13 (held-claim resolution is auto-terminal,
// single, and aggregate-outcome-driven).
//
// We drive the auto-terminal mechanism via the supervisor's public
// CheckAndFireResolution helper rather than orchestrating a full
// holding-subgraph through the executor — the public helper is the
// supervisor's own entry point for this code path, and isolates the
// test from changes to the dispatch flow.
package claim_stores

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/executor"
	"github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/scenario"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/storage"
	"github.com/fallguy/rimsky/core/store"
	"github.com/fallguy/rimsky/core/supervisor"
)

// TestAutoTerminalFiresOnAllCompleted seeds a held subgraph with two
// completed claim_holders rows and confirms CheckAndFireResolution
// deletes the lock-holder row and (via cascade FK) the claim_holders
// rows.
func TestAutoTerminalFiresOnAllCompleted(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{
		NoScheduler:  true,
		NoSupervisor: true,
		StoresConfig: store.StoresConfig{Stores: map[string]map[string]any{
			"workspace": {"kind": "stub_filesystem"},
		}},
	})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "auto-terminal", Version: "1",
		FrameResolution: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "acquirer", Executor: "stub"},
				scenario.WithStores(scenario.AliasedClaimRef("workspace", "tenant-A/path", "rw", "alias-A")),
				scenario.WithClaimResolutions(map[string]node.ClaimResolution{
					"alias-A": {OnCommit: "commit", OnGiveUp: "abandon"},
				}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "inheritor", Executor: "stub", Dependencies: []string{"acquirer"}},
				scenario.WithInherits(scenario.Inherit("alias-A")),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-auto-terminal", map[string]any{})
	acq := h.FindNode(iid, "acquirer")
	inh := h.FindNode(iid, "inheritor")
	require.NotNil(t, acq)
	require.NotNil(t, inh)

	// Seed lock-holder + two claim-holders rows directly. supervisor_id
	// matches what the supervisor below will use; address/region are
	// the substituted-selector bytes.
	storeName := "workspace"
	intent := "rw"
	lockHolderID := shared.UUID(uuid.New())
	require.NoError(t, h.Storage.Transaction(h.Ctx, func(ctx context.Context, tx storage.Tx) error {
		if err := h.Storage.LockHolders().Insert(ctx, storage.LockHolderInsertInput{
			ID: lockHolderID, LockKind: storage.LockKindRegion,
			StoreName:          &storeName,
			RegionData:         []byte(`"tenant-A/path"`),
			Address:            []byte(`"tenant-A/path"`),
			Intent:             &intent,
			HolderSupervisorID: "auto-term-sup",
			HolderNodeID:       acq.ID,
			ExpiresAt:          time.Now().Add(10 * time.Minute),
		}, tx); err != nil {
			return err
		}
		if err := h.Storage.ClaimHolders().Insert(ctx, storage.ClaimHolderInsertInput{
			ID: shared.UUID(uuid.New()), LockHolderID: lockHolderID, HolderNodeID: acq.ID,
		}, tx); err != nil {
			return err
		}
		return h.Storage.ClaimHolders().Insert(ctx, storage.ClaimHolderInsertInput{
			ID: shared.UUID(uuid.New()), LockHolderID: lockHolderID, HolderNodeID: inh.ID,
		}, tx)
	}))

	// Mark both claim-holders rows completed (we're simulating the
	// state the supervisor's release path produces just before the
	// auto-terminal check).
	require.NoError(t, h.Storage.ClaimHolders().CompleteByLockHolderAndNode(
		h.Ctx, lockHolderID, acq.ID, storage.ClaimHolderStateCompleted, nil,
	))
	require.NoError(t, h.Storage.ClaimHolders().CompleteByLockHolderAndNode(
		h.Ctx, lockHolderID, inh.ID, storage.ClaimHolderStateCompleted, nil,
	))

	// Drive CheckAndFireResolution under a fresh tx as the supervisor
	// would — same supervisor_id as the lock-holder row.
	clientPool := executor.NewClientPool()
	t.Cleanup(func() { _ = clientPool.Close() })
	args := supervisor.RunArgs{
		Storage:       h.Storage,
		LockHolders:   store.NewLockHoldersClient(h.Pool),
		StoreRegistry: h.Stores,
		Logger:        shared.SilentLogger{},
		SupervisorID:  "auto-term-sup",
	}
	tx, err := h.Pool.Begin(h.Ctx)
	require.NoError(t, err)
	require.NoError(t, supervisor.CheckAndFireResolution(
		h.Ctx, args, tx, lockHolderID, "alias-A",
		map[string]node.ClaimResolution{
			"alias-A": {OnCommit: "commit", OnGiveUp: "abandon"},
		},
	))
	require.NoError(t, tx.Commit(h.Ctx))

	// Lock-holder row gone; cascade FK removed the claim-holders rows.
	gone, err := h.Storage.LockHolders().Get(h.Ctx, lockHolderID, nil)
	require.NoError(t, err)
	require.Nil(t, gone, "auto-terminal must delete the lock-holder row")
	rows, err := h.Storage.ClaimHolders().ListByLockHolderID(h.Ctx, lockHolderID, nil)
	require.NoError(t, err)
	require.Empty(t, rows, "cascade FK should remove claim-holders rows")
}

// TestAutoTerminalNoFireWhileActiveRowsRemain verifies the
// auto-terminal trigger gate: if any claim-holders row for the
// lock-holder is still 'active', CheckAndFireResolution is a no-op.
func TestAutoTerminalNoFireWhileActiveRowsRemain(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{
		NoScheduler:  true,
		NoSupervisor: true,
		StoresConfig: store.StoresConfig{Stores: map[string]map[string]any{
			"workspace": {"kind": "stub_filesystem"},
		}},
	})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "auto-terminal-active", Version: "1",
		FrameResolution: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "acquirer", Executor: "stub"},
				scenario.WithStores(scenario.AliasedClaimRef("workspace", "tenant-B/path", "rw", "alias-B")),
				scenario.WithClaimResolutions(map[string]node.ClaimResolution{
					"alias-B": {OnCommit: "commit", OnGiveUp: "abandon"},
				}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "inheritor", Executor: "stub", Dependencies: []string{"acquirer"}},
				scenario.WithInherits(scenario.Inherit("alias-B")),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-auto-terminal-active", map[string]any{})
	acq := h.FindNode(iid, "acquirer")
	inh := h.FindNode(iid, "inheritor")

	storeName := "workspace"
	intent := "rw"
	lockHolderID := shared.UUID(uuid.New())
	require.NoError(t, h.Storage.Transaction(h.Ctx, func(ctx context.Context, tx storage.Tx) error {
		if err := h.Storage.LockHolders().Insert(ctx, storage.LockHolderInsertInput{
			ID: lockHolderID, LockKind: storage.LockKindRegion,
			StoreName: &storeName, RegionData: []byte(`"tenant-B/path"`),
			Address: []byte(`"tenant-B/path"`), Intent: &intent,
			HolderSupervisorID: "auto-term-sup-2",
			HolderNodeID:       acq.ID,
			ExpiresAt:          time.Now().Add(10 * time.Minute),
		}, tx); err != nil {
			return err
		}
		if err := h.Storage.ClaimHolders().Insert(ctx, storage.ClaimHolderInsertInput{
			ID: shared.UUID(uuid.New()), LockHolderID: lockHolderID, HolderNodeID: acq.ID,
		}, tx); err != nil {
			return err
		}
		return h.Storage.ClaimHolders().Insert(ctx, storage.ClaimHolderInsertInput{
			ID: shared.UUID(uuid.New()), LockHolderID: lockHolderID, HolderNodeID: inh.ID,
		}, tx)
	}))

	// Only one row marked completed; the inheritor's row is still
	// 'active'. CheckAndFireResolution must no-op.
	require.NoError(t, h.Storage.ClaimHolders().CompleteByLockHolderAndNode(
		h.Ctx, lockHolderID, acq.ID, storage.ClaimHolderStateCompleted, nil,
	))

	clientPool := executor.NewClientPool()
	t.Cleanup(func() { _ = clientPool.Close() })
	args := supervisor.RunArgs{
		Storage:       h.Storage,
		LockHolders:   store.NewLockHoldersClient(h.Pool),
		StoreRegistry: h.Stores,
		Logger:        shared.SilentLogger{},
		SupervisorID:  "auto-term-sup-2",
	}
	tx, err := h.Pool.Begin(h.Ctx)
	require.NoError(t, err)
	require.NoError(t, supervisor.CheckAndFireResolution(
		h.Ctx, args, tx, lockHolderID, "alias-B",
		map[string]node.ClaimResolution{"alias-B": {OnCommit: "commit", OnGiveUp: "abandon"}},
	))
	require.NoError(t, tx.Commit(h.Ctx))

	// Lock-holder still present.
	row, err := h.Storage.LockHolders().Get(h.Ctx, lockHolderID, nil)
	require.NoError(t, err)
	require.NotNil(t, row, "auto-terminal must NOT fire while any claim_holders row is active")
}
