// Substantive coverage for the §14.4 auto-terminal mechanism in
// isolation. Drives CheckAndFireResolution against a real Postgres + a
// stub-filesystem store and asserts the aggregate-outcome semantics.

package supervisor_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/internal/pgtest"
	"github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/storage"
	pgstorage "github.com/fallguy/rimsky/core/storage/postgres"
	"github.com/fallguy/rimsky/core/store"
	"github.com/fallguy/rimsky/core/store/stub"
	"github.com/fallguy/rimsky/core/supervisor"
)

// TestCheckAndFireResolution_AllCompletedFiresCommit seeds a held
// subgraph with two completed claim_holders rows and confirms
// CheckAndFireResolution invokes the on_commit verb (Commit on the
// substrate) and deletes the lock-holder row. Cascade FK removes the
// claim-holders rows.
func TestCheckAndFireResolution_AllCompletedFiresCommit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool, teardown := pgtest.StartPostgres(ctx, t)
	t.Cleanup(teardown)
	backend := pgstorage.New(pool)

	tmpl, err := backend.Templates().Deploy(ctx, node.TemplateSpec{
		Name: "auto-term-commit", Version: "1",
	}, nil)
	require.NoError(t, err)
	inst, err := backend.Instances().Create(ctx, storage.InstanceCreateInput{
		TemplateID: tmpl.ID, ConsumerKey: "ck", Params: map[string]any{},
	}, nil)
	require.NoError(t, err)
	acqNode, err := backend.Nodes().Create(ctx, storage.NodeCreateInput{
		ID: shared.UUID(uuid.New()), InstanceID: inst.ID, NodeType: "acquirer", Executor: "stub",
	}, nil)
	require.NoError(t, err)
	inhNode, err := backend.Nodes().Create(ctx, storage.NodeCreateInput{
		ID: shared.UUID(uuid.New()), InstanceID: inst.ID, NodeType: "inheritor", Executor: "stub",
	}, nil)
	require.NoError(t, err)

	// Stub-filesystem store registered locally so the resolution verb
	// has a substrate to dispatch against. The stub's Commit/Abandon
	// are in-memory state mutators — sufficient for this test.
	reg := store.NewRegistry()
	reg.Register(stub.FilesystemFactory())
	_, err = reg.BuildAll(store.StoresConfig{Stores: map[string]map[string]any{
		"workspace": {"kind": "stub_filesystem"},
	}})
	require.NoError(t, err)
	wsStore, ok := reg.GetStore("workspace")
	require.True(t, ok)
	stubStore := wsStore.(*stub.Store)

	// Seed one lock-holder row + two claim-holders rows in 'active'.
	storeName := "workspace"
	intent := "rw"
	lockHolderID := shared.UUID(uuid.New())
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx storage.Tx) error {
		if err := backend.LockHolders().Insert(ctx, storage.LockHolderInsertInput{
			ID: lockHolderID, LockKind: storage.LockKindRegion,
			StoreName: &storeName, RegionData: []byte(`"r"`), Address: []byte(`"r"`),
			Intent:             &intent,
			HolderSupervisorID: "sup-A", HolderNodeID: acqNode.ID,
			ExpiresAt: time.Now().Add(10 * time.Minute),
		}, tx); err != nil {
			return err
		}
		if err := backend.ClaimHolders().Insert(ctx, storage.ClaimHolderInsertInput{
			ID: shared.UUID(uuid.New()), LockHolderID: lockHolderID, HolderNodeID: acqNode.ID,
		}, tx); err != nil {
			return err
		}
		return backend.ClaimHolders().Insert(ctx, storage.ClaimHolderInsertInput{
			ID: shared.UUID(uuid.New()), LockHolderID: lockHolderID, HolderNodeID: inhNode.ID,
		}, tx)
	}))

	// Mark both rows completed.
	require.NoError(t, backend.ClaimHolders().CompleteByLockHolderAndNode(
		ctx, lockHolderID, acqNode.ID, storage.ClaimHolderStateCompleted, nil,
	))
	require.NoError(t, backend.ClaimHolders().CompleteByLockHolderAndNode(
		ctx, lockHolderID, inhNode.ID, storage.ClaimHolderStateCompleted, nil,
	))

	args := supervisor.RunArgs{
		Storage:       backend,
		LockHolders:   store.NewLockHoldersClient(pool),
		StoreRegistry: reg,
		Logger:        shared.SilentLogger{},
		SupervisorID:  "sup-A",
	}
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	require.NoError(t, supervisor.CheckAndFireResolution(
		ctx, args, tx, lockHolderID, "alias-A",
		map[string]node.ClaimResolution{
			"alias-A": {OnCommit: "commit", OnGiveUp: "abandon"},
		},
	))
	require.NoError(t, tx.Commit(ctx))

	// Lock-holder row is gone.
	row, err := backend.LockHolders().Get(ctx, lockHolderID, nil)
	require.NoError(t, err)
	require.Nil(t, row, "auto-terminal must delete lock-holder on aggregate-completed")

	// Verb assertion: aggregate-completed must route to Commit, never
	// Abandon. Mirror of the give-up test below.
	abandonSeen, commitSeen := false, false
	for _, c := range stubStore.Calls() {
		if c.Verb == "abandon" {
			abandonSeen = true
		}
		if c.Verb == "commit" {
			commitSeen = true
		}
	}
	require.True(t, commitSeen, "aggregate-completed must invoke Commit on the substrate")
	require.False(t, abandonSeen, "aggregate-completed must NOT invoke Abandon on the substrate")
}

// TestCheckAndFireResolution_AnyFailedFiresGiveUp seeds a held subgraph
// with one completed and one failed claim_holders row and confirms the
// aggregate-outcome path picks on_give_up and deletes the lock-holder.
func TestCheckAndFireResolution_AnyFailedFiresGiveUp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool, teardown := pgtest.StartPostgres(ctx, t)
	t.Cleanup(teardown)
	backend := pgstorage.New(pool)

	tmpl, err := backend.Templates().Deploy(ctx, node.TemplateSpec{
		Name: "auto-term-give-up", Version: "1",
	}, nil)
	require.NoError(t, err)
	inst, err := backend.Instances().Create(ctx, storage.InstanceCreateInput{
		TemplateID: tmpl.ID, ConsumerKey: "ck", Params: map[string]any{},
	}, nil)
	require.NoError(t, err)
	acqNode, err := backend.Nodes().Create(ctx, storage.NodeCreateInput{
		ID: shared.UUID(uuid.New()), InstanceID: inst.ID, NodeType: "acquirer", Executor: "stub",
	}, nil)
	require.NoError(t, err)
	inhNode, err := backend.Nodes().Create(ctx, storage.NodeCreateInput{
		ID: shared.UUID(uuid.New()), InstanceID: inst.ID, NodeType: "inheritor", Executor: "stub",
	}, nil)
	require.NoError(t, err)

	reg := store.NewRegistry()
	reg.Register(stub.FilesystemFactory())
	_, err = reg.BuildAll(store.StoresConfig{Stores: map[string]map[string]any{
		"workspace": {"kind": "stub_filesystem"},
	}})
	require.NoError(t, err)
	// Recover the *stub.Store the registry built so we can assert which
	// substrate verb the resolution path invoked. selectResolutionAction
	// is the function under test; without checking the recorder a
	// regression that always returned (commit, true) would still pass
	// the lock-holder-row assertion.
	wsStore, ok := reg.GetStore("workspace")
	require.True(t, ok)
	stubStore := wsStore.(*stub.Store)

	storeName := "workspace"
	intent := "rw"
	lockHolderID := shared.UUID(uuid.New())
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx storage.Tx) error {
		if err := backend.LockHolders().Insert(ctx, storage.LockHolderInsertInput{
			ID: lockHolderID, LockKind: storage.LockKindRegion,
			StoreName: &storeName, RegionData: []byte(`"r"`), Address: []byte(`"r"`),
			Intent:             &intent,
			HolderSupervisorID: "sup-G", HolderNodeID: acqNode.ID,
			ExpiresAt: time.Now().Add(10 * time.Minute),
		}, tx); err != nil {
			return err
		}
		if err := backend.ClaimHolders().Insert(ctx, storage.ClaimHolderInsertInput{
			ID: shared.UUID(uuid.New()), LockHolderID: lockHolderID, HolderNodeID: acqNode.ID,
		}, tx); err != nil {
			return err
		}
		return backend.ClaimHolders().Insert(ctx, storage.ClaimHolderInsertInput{
			ID: shared.UUID(uuid.New()), LockHolderID: lockHolderID, HolderNodeID: inhNode.ID,
		}, tx)
	}))
	// One completed + one failed → aggregate failed.
	require.NoError(t, backend.ClaimHolders().CompleteByLockHolderAndNode(
		ctx, lockHolderID, acqNode.ID, storage.ClaimHolderStateCompleted, nil,
	))
	require.NoError(t, backend.ClaimHolders().CompleteByLockHolderAndNode(
		ctx, lockHolderID, inhNode.ID, storage.ClaimHolderStateFailed, nil,
	))

	args := supervisor.RunArgs{
		Storage:       backend,
		LockHolders:   store.NewLockHoldersClient(pool),
		StoreRegistry: reg,
		Logger:        shared.SilentLogger{},
		SupervisorID:  "sup-G",
	}
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	require.NoError(t, supervisor.CheckAndFireResolution(
		ctx, args, tx, lockHolderID, "alias-G",
		map[string]node.ClaimResolution{
			"alias-G": {OnCommit: "commit", OnGiveUp: "abandon"},
		},
	))
	require.NoError(t, tx.Commit(ctx))

	row, err := backend.LockHolders().Get(ctx, lockHolderID, nil)
	require.NoError(t, err)
	require.Nil(t, row, "auto-terminal must delete lock-holder on aggregate-failed too")

	// Verb assertion: aggregate-failed must route to Abandon, never
	// Commit. Iterate every recorded call and assert at least one
	// abandon and zero commits — guards against a selectResolutionAction
	// regression that always returns the success path.
	abandonSeen, commitSeen := false, false
	for _, c := range stubStore.Calls() {
		if c.Verb == "abandon" {
			abandonSeen = true
		}
		if c.Verb == "commit" {
			commitSeen = true
		}
	}
	require.True(t, abandonSeen, "aggregate-failed must invoke Abandon on the substrate")
	require.False(t, commitSeen, "aggregate-failed must NOT invoke Commit on the substrate")
}
