// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Substantive coverage for the §4.10 invariant 13 auto-terminal mechanism in
// isolation. Drives CheckAndFireResolution against a real Postgres + a
// stub-filesystem store and asserts the aggregate-outcome semantics.

package integration_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/foundation/integration"
	"github.com/fallguy/rimsky/foundation/internal/pgtest"
	"github.com/fallguy/rimsky/foundation/locks"
	"github.com/fallguy/rimsky/foundation/locks/storetest"
	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/modeling/node"
	"github.com/fallguy/rimsky/modeling/shared"
)

// insertDeployedTemplate inserts a template row in 'deployed' state with a
// deterministic content hash derived from name+version.
func insertDeployedTemplate(ctx context.Context, t *testing.T, sb persistence.Store, spec node.TemplateSpec) persistence.TemplateRow {
	t.Helper()
	sum := sha256.Sum256([]byte(spec.Name + ":" + spec.Version))
	hash := "sha256-" + hex.EncodeToString(sum[:])
	var row *persistence.TemplateRow
	require.NoError(t, sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := sb.Templates().Insert(ctx, persistence.TemplateInsertInput{
			ID: hash, Spec: spec, State: persistence.TemplateStateRegistered,
		}, tx); err != nil {
			return err
		}
		if err := sb.Templates().UpdateState(ctx, hash, persistence.TemplateStateDeployed, tx); err != nil {
			return err
		}
		r, err := sb.Templates().GetByHash(ctx, hash, tx)
		row = r
		return err
	}))
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
	var inst persistence.InstanceRow
	var acqNode, inhNode persistence.NodeRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		i, err := backend.Instances().Create(ctx, persistence.InstanceCreateInput{
			ID: shared.UUID(uuid.New()), TemplateHash: tmpl.ID, InstanceKey: &ck, Params: map[string]any{},
		}, tx)
		if err != nil {
			return err
		}
		inst = i
		a, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: inst.ID, NodeType: "acquirer", Executor: "stub",
		}, tx)
		if err != nil {
			return err
		}
		acqNode = a
		ih, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: inst.ID, NodeType: "inheritor", Executor: "stub",
		}, tx)
		if err != nil {
			return err
		}
		inhNode = ih
		return nil
	}))

	reg := locks.NewRegistry()
	stubStore := storetest.NewFake("workspace", locks.Capabilities{WriteSemanticsEnvelope: []locks.WriteSemantics{locks.WriteSemanticsSync}})
	reg.Add("workspace", stubStore)

	storeName := "workspace"
	intent := "rw"
	lockHolderID := shared.UUID(uuid.New())
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := backend.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID: lockHolderID, LockKind: persistence.LockKindScope,
			StoreName: &storeName, ScopeData: []byte(`"r"`), Address: []byte(`"r"`),
			Intent:             &intent,
			HolderSupervisorID: "sup-A", HolderNodeID: acqNode.ID,
			ExpiresAt: time.Now().Add(10 * time.Minute),
		}, tx); err != nil {
			return err
		}
		if err := backend.ClaimHolders().Insert(ctx, persistence.ClaimHolderInsertInput{
			ID: shared.UUID(uuid.New()), ClaimHandleID: lockHolderID, HolderNodeID: acqNode.ID,
		}, tx); err != nil {
			return err
		}
		return backend.ClaimHolders().Insert(ctx, persistence.ClaimHolderInsertInput{
			ID: shared.UUID(uuid.New()), ClaimHandleID: lockHolderID, HolderNodeID: inhNode.ID,
		}, tx)
	}))

	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := backend.ClaimHolders().CompleteByLockHolderAndNode(
			ctx, lockHolderID, acqNode.ID, persistence.ClaimHolderStateCompleted, tx,
		); err != nil {
			return err
		}
		return backend.ClaimHolders().CompleteByLockHolderAndNode(
			ctx, lockHolderID, inhNode.ID, persistence.ClaimHolderStateCompleted, tx,
		)
	}))

	args := integration.RunArgs{
		Persist:       backend,
		ClaimHandles:  backend.ClaimHandles(),
		StoreRegistry: reg,
		Logger:        shared.SilentLogger{},
		SupervisorID:  "sup-A",
	}
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return integration.CheckAndFireResolution(ctx, args, tx, lockHolderID)
	}))

	var row *persistence.ClaimHandleRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := backend.ClaimHandles().Get(ctx, lockHolderID, tx)
		row = r
		return err
	}))
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
	var inst persistence.InstanceRow
	var acqNode, inhNode persistence.NodeRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		i, err := backend.Instances().Create(ctx, persistence.InstanceCreateInput{
			ID: shared.UUID(uuid.New()), TemplateHash: tmpl.ID, InstanceKey: &ck, Params: map[string]any{},
		}, tx)
		if err != nil {
			return err
		}
		inst = i
		a, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: inst.ID, NodeType: "acquirer", Executor: "stub",
		}, tx)
		if err != nil {
			return err
		}
		acqNode = a
		ih, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: inst.ID, NodeType: "inheritor", Executor: "stub",
		}, tx)
		if err != nil {
			return err
		}
		inhNode = ih
		return nil
	}))

	reg := locks.NewRegistry()
	stubStore := storetest.NewFake("workspace", locks.Capabilities{WriteSemanticsEnvelope: []locks.WriteSemantics{locks.WriteSemanticsSync}})
	reg.Add("workspace", stubStore)

	storeName := "workspace"
	intent := "rw"
	lockHolderID := shared.UUID(uuid.New())
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := backend.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID: lockHolderID, LockKind: persistence.LockKindScope,
			StoreName: &storeName, ScopeData: []byte(`"r"`), Address: []byte(`"r"`),
			Intent:             &intent,
			HolderSupervisorID: "sup-G", HolderNodeID: acqNode.ID,
			ExpiresAt: time.Now().Add(10 * time.Minute),
		}, tx); err != nil {
			return err
		}
		if err := backend.ClaimHolders().Insert(ctx, persistence.ClaimHolderInsertInput{
			ID: shared.UUID(uuid.New()), ClaimHandleID: lockHolderID, HolderNodeID: acqNode.ID,
		}, tx); err != nil {
			return err
		}
		return backend.ClaimHolders().Insert(ctx, persistence.ClaimHolderInsertInput{
			ID: shared.UUID(uuid.New()), ClaimHandleID: lockHolderID, HolderNodeID: inhNode.ID,
		}, tx)
	}))
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := backend.ClaimHolders().CompleteByLockHolderAndNode(
			ctx, lockHolderID, acqNode.ID, persistence.ClaimHolderStateCompleted, tx,
		); err != nil {
			return err
		}
		return backend.ClaimHolders().CompleteByLockHolderAndNode(
			ctx, lockHolderID, inhNode.ID, persistence.ClaimHolderStateFailed, tx,
		)
	}))

	args := integration.RunArgs{
		Persist:       backend,
		ClaimHandles:  backend.ClaimHandles(),
		StoreRegistry: reg,
		Logger:        shared.SilentLogger{},
		SupervisorID:  "sup-G",
	}
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return integration.CheckAndFireResolution(ctx, args, tx, lockHolderID)
	}))

	var row *persistence.ClaimHandleRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := backend.ClaimHandles().Get(ctx, lockHolderID, tx)
		row = r
		return err
	}))
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
