// Substantive coverage for the §4.10 invariant 13 auto-terminal mechanism in
// isolation. Drives CheckAndFireResolution against a real Postgres + a
// stub-filesystem store and asserts the aggregate-outcome semantics.

package supervisor_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/internal/pgtest"
	"github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/persistence"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/store"
	"github.com/fallguy/rimsky/core/store/storetest"
	"github.com/fallguy/rimsky/core/supervisor"
)

// insertDeployedTemplate inserts a template row in 'deployed' state with a
// deterministic content hash derived from name+version.
func insertDeployedTemplate(ctx context.Context, t *testing.T, sb persistence.Store, spec node.TemplateSpec) persistence.TemplateRow {
	t.Helper()
	sum := sha256.Sum256([]byte(spec.Name + ":" + spec.Version))
	hash := "sha256-" + hex.EncodeToString(sum[:])
	require.NoError(t, sb.Templates().Insert(ctx, persistence.TemplateInsertInput{
		ID: hash, Spec: spec, State: persistence.TemplateStateRegistered,
	}, nil))
	require.NoError(t, sb.Templates().UpdateState(ctx, hash, persistence.TemplateStateDeployed, nil))
	row, err := sb.Templates().GetByHash(ctx, hash, nil)
	require.NoError(t, err)
	require.NotNil(t, row)
	return *row
}

// TestCheckAndFireResolution_AllCompletedFiresCommit seeds a held
// subgraph with two completed claim_holders rows and confirms
// CheckAndFireResolution invokes Commit on the store and
// deletes the lock-holder row. Cascade FK removes the claim-holders
// rows.
func TestCheckAndFireResolution_AllCompletedFiresCommit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
	backend := d.Store()

	tmpl := insertDeployedTemplate(ctx, t, backend, node.TemplateSpec{
		Name: "auto-term-commit", Version: "1",
	})
	ck := "ck"
	inst, err := backend.Instances().Create(ctx, persistence.InstanceCreateInput{
		ID: shared.UUID(uuid.New()), TemplateHash: tmpl.ID, InstanceKey: &ck, Params: map[string]any{},
	}, nil)
	require.NoError(t, err)
	acqNode, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
		ID: shared.UUID(uuid.New()), InstanceID: inst.ID, NodeType: "acquirer", Executor: "stub",
	}, nil)
	require.NoError(t, err)
	inhNode, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
		ID: shared.UUID(uuid.New()), InstanceID: inst.ID, NodeType: "inheritor", Executor: "stub",
	}, nil)
	require.NoError(t, err)

	reg := store.NewRegistry()
	stubStore := storetest.NewFake("workspace", store.Capabilities{WriteSemantics: store.WriteSemanticsDirect})
	reg.Add("workspace", stubStore)

	storeName := "workspace"
	intent := "rw"
	lockHolderID := shared.UUID(uuid.New())
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := backend.LockHolders().Insert(ctx, persistence.LockHolderInsertInput{
			ID: lockHolderID, LockKind: persistence.LockKindRegion,
			StoreName: &storeName, RegionData: []byte(`"r"`), Address: []byte(`"r"`),
			Intent:             &intent,
			HolderSupervisorID: "sup-A", HolderNodeID: acqNode.ID,
			ExpiresAt: time.Now().Add(10 * time.Minute),
		}, tx); err != nil {
			return err
		}
		if err := backend.ClaimHolders().Insert(ctx, persistence.ClaimHolderInsertInput{
			ID: shared.UUID(uuid.New()), LockHolderID: lockHolderID, HolderNodeID: acqNode.ID,
		}, tx); err != nil {
			return err
		}
		return backend.ClaimHolders().Insert(ctx, persistence.ClaimHolderInsertInput{
			ID: shared.UUID(uuid.New()), LockHolderID: lockHolderID, HolderNodeID: inhNode.ID,
		}, tx)
	}))

	require.NoError(t, backend.ClaimHolders().CompleteByLockHolderAndNode(
		ctx, lockHolderID, acqNode.ID, persistence.ClaimHolderStateCompleted, nil,
	))
	require.NoError(t, backend.ClaimHolders().CompleteByLockHolderAndNode(
		ctx, lockHolderID, inhNode.ID, persistence.ClaimHolderStateCompleted, nil,
	))

	args := supervisor.RunArgs{
		Persist:       backend,
		LockHolders:   backend.LockHolders(),
		StoreRegistry: reg,
		Logger:        shared.SilentLogger{},
		SupervisorID:  "sup-A",
	}
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return supervisor.CheckAndFireResolution(ctx, args, tx, lockHolderID)
	}))

	row, err := backend.LockHolders().Get(ctx, lockHolderID, nil)
	require.NoError(t, err)
	require.Nil(t, row, "auto-terminal must delete lock-holder on aggregate-completed")

	abandonSeen, commitSeen := false, false
	for _, c := range stubStore.Calls() {
		if c.Verb == "abandon" {
			abandonSeen = true
		}
		if c.Verb == "commit" {
			commitSeen = true
		}
	}
	require.True(t, commitSeen, "aggregate-completed must invoke Commit on the store")
	require.False(t, abandonSeen, "aggregate-completed must NOT invoke Abandon on the store")
}

// TestCheckAndFireResolution_AnyFailedFiresGiveUp seeds a held subgraph
// with one completed and one failed claim_holders row and confirms the
// aggregate-outcome path picks Abandon and deletes the lock-holder.
func TestCheckAndFireResolution_AnyFailedFiresGiveUp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
	backend := d.Store()

	tmpl := insertDeployedTemplate(ctx, t, backend, node.TemplateSpec{
		Name: "auto-term-give-up", Version: "1",
	})
	ck := "ck"
	inst, err := backend.Instances().Create(ctx, persistence.InstanceCreateInput{
		ID: shared.UUID(uuid.New()), TemplateHash: tmpl.ID, InstanceKey: &ck, Params: map[string]any{},
	}, nil)
	require.NoError(t, err)
	acqNode, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
		ID: shared.UUID(uuid.New()), InstanceID: inst.ID, NodeType: "acquirer", Executor: "stub",
	}, nil)
	require.NoError(t, err)
	inhNode, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
		ID: shared.UUID(uuid.New()), InstanceID: inst.ID, NodeType: "inheritor", Executor: "stub",
	}, nil)
	require.NoError(t, err)

	reg := store.NewRegistry()
	stubStore := storetest.NewFake("workspace", store.Capabilities{WriteSemantics: store.WriteSemanticsDirect})
	reg.Add("workspace", stubStore)

	storeName := "workspace"
	intent := "rw"
	lockHolderID := shared.UUID(uuid.New())
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := backend.LockHolders().Insert(ctx, persistence.LockHolderInsertInput{
			ID: lockHolderID, LockKind: persistence.LockKindRegion,
			StoreName: &storeName, RegionData: []byte(`"r"`), Address: []byte(`"r"`),
			Intent:             &intent,
			HolderSupervisorID: "sup-G", HolderNodeID: acqNode.ID,
			ExpiresAt: time.Now().Add(10 * time.Minute),
		}, tx); err != nil {
			return err
		}
		if err := backend.ClaimHolders().Insert(ctx, persistence.ClaimHolderInsertInput{
			ID: shared.UUID(uuid.New()), LockHolderID: lockHolderID, HolderNodeID: acqNode.ID,
		}, tx); err != nil {
			return err
		}
		return backend.ClaimHolders().Insert(ctx, persistence.ClaimHolderInsertInput{
			ID: shared.UUID(uuid.New()), LockHolderID: lockHolderID, HolderNodeID: inhNode.ID,
		}, tx)
	}))
	require.NoError(t, backend.ClaimHolders().CompleteByLockHolderAndNode(
		ctx, lockHolderID, acqNode.ID, persistence.ClaimHolderStateCompleted, nil,
	))
	require.NoError(t, backend.ClaimHolders().CompleteByLockHolderAndNode(
		ctx, lockHolderID, inhNode.ID, persistence.ClaimHolderStateFailed, nil,
	))

	args := supervisor.RunArgs{
		Persist:       backend,
		LockHolders:   backend.LockHolders(),
		StoreRegistry: reg,
		Logger:        shared.SilentLogger{},
		SupervisorID:  "sup-G",
	}
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return supervisor.CheckAndFireResolution(ctx, args, tx, lockHolderID)
	}))

	row, err := backend.LockHolders().Get(ctx, lockHolderID, nil)
	require.NoError(t, err)
	require.Nil(t, row, "auto-terminal must delete lock-holder on aggregate-failed too")

	abandonSeen, commitSeen := false, false
	for _, c := range stubStore.Calls() {
		if c.Verb == "abandon" {
			abandonSeen = true
		}
		if c.Verb == "commit" {
			commitSeen = true
		}
	}
	require.True(t, abandonSeen, "aggregate-failed must invoke Abandon on the store")
	require.False(t, commitSeen, "aggregate-failed must NOT invoke Commit on the store")
}
