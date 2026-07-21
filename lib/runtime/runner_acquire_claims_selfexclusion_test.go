// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks/storetest"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	_ "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	tmplspec "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
)

type selfExclusionFixture struct {
	tables      persistence.Tables
	instanceID  shared.UUID
	mainScopeID shared.UUID
	frameID     shared.UUID
	nodeID      shared.UUID
}

func openSelfExclusionFixture(ctx context.Context, t *testing.T) selfExclusionFixture {
	t.Helper()
	d, err := persistence.Open(ctx, persistence.Config{
		Driver: "sqlite",
		SQLite: &persistence.SQLiteConfig{Path: filepath.Join(t.TempDir(), "state.db")},
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := d.Migrate(ctx, shared.SilentLogger{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	tables := d.Tables()

	templateHash := "sha256-" + uuid.NewString()
	instanceID := shared.UUID(uuid.New())
	mainScopeID := shared.UUID(uuid.New())
	nodeID := shared.UUID(uuid.New())
	var frameID shared.UUID

	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := tables.Templates().Insert(ctx, persistence.TemplateInsertInput{
			ID: templateHash, Spec: tmplspec.TemplateSpec{Name: "self-exclusion-fixture", Version: "1"},
			State: persistence.TemplateStateRegistered, Source: "direct",
		}, tx); err != nil {
			return err
		}
		if err := tables.RunScopes().Create(ctx, persistence.RunScopeRow{
			ID: mainScopeID, GraphName: tmplspec.MainGraphName, InstanceID: instanceID,
		}, tx); err != nil {
			return err
		}
		if _, err := tables.Instances().Create(ctx, persistence.InstanceCreateInput{
			ID: instanceID, TemplateHash: templateHash,
		}, tx); err != nil {
			return err
		}
		if _, err := tables.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: nodeID, InstanceID: instanceID, NodeType: "worker", Executor: "stub",
		}, tx); err != nil {
			return err
		}
		msgID := shared.UUID(uuid.New())
		if err := tables.Messages().Insert(ctx, persistence.EnqueueMessageRequest{
			ID: msgID, InstanceID: instanceID, Type: "test/seed", Sender: "test", SenderKind: "operator",
		}, tx); err != nil {
			return err
		}
		fid, err := tables.Frames().InsertRunningFrame(ctx, instanceID, msgID, mainScopeID, tx)
		frameID = fid
		return err
	}); err != nil {
		t.Fatalf("seed fixture: %v", err)
	}
	return selfExclusionFixture{
		tables: tables, instanceID: instanceID, mainScopeID: mainScopeID, frameID: frameID, nodeID: nodeID,
	}
}

func seedSelfExclusionRun(ctx context.Context, t *testing.T, fx selfExclusionFixture) shared.UUID {
	t.Helper()
	runID := shared.UUID(uuid.New())
	require.NoError(t, fx.tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return fx.tables.NodeRunTree().CreateRootNodeRun(ctx, persistence.CreateRootNodeRunInput{
			NodeRunID: runID, NodeID: fx.nodeID, FrameID: fx.frameID, RunScopeID: fx.mainScopeID, ExecutorName: "stub",
		}, tx)
	}))
	return runID
}

func insertConflictCandidateClaim(
	ctx context.Context, t *testing.T, tables persistence.Tables,
	producer, supervisorID string, holderNodeID, holderRunID shared.UUID,
) {
	t.Helper()
	intent := "rw"
	require.NoError(t, tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return tables.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID:                     shared.UUID(uuid.New()),
			NodeRunID:              &holderRunID,
			LockKind:               persistence.LockKindScope,
			ProducerName:           &producer,
			ClaimScopeData:         []byte(`"shared-scope"`),
			Intent:                 &intent,
			RealizedWriteSemantics: string(claimproducer.WriteSemanticsSync),
			HolderSupervisorID:     supervisorID,
			HolderNodeID:           holderNodeID,
			ExpiresAt:              time.Now().Add(time.Hour),
		}, tx)
	}))
}

// @story: claim-handoff-durable
func TestEvaluateClaimScopeConflict_PriorRunOfSameNodeOnSameSupervisorIsAConflict(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fx := openSelfExclusionFixture(ctx, t)
	tables := fx.tables

	const producer = "self-exclusion-store"
	const supervisorID = "sup-A"
	priorRunID := seedSelfExclusionRun(ctx, t, fx)
	candidateRunID := seedSelfExclusionRun(ctx, t, fx)

	insertConflictCandidateClaim(ctx, t, tables, producer, supervisorID, fx.nodeID, priorRunID)

	fake := storetest.NewFake(producer, claimproducer.Capabilities{})
	args := RunArgs{Persist: tables, ClaimHandles: tables.ClaimHandles(), SupervisorID: supervisorID}
	spec := claimproducer.ClaimSpec{ProducerName: producer, Selector: "shared-scope", Intent: "rw"}
	cand := persistence.Candidate{NodeID: fx.nodeID, NodeRunID: candidateRunID}

	var conflicted, persistentConflict bool
	require.NoError(t, tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		var err error
		conflicted, persistentConflict, err = evaluateClaimScopeConflict(ctx, args, fake, spec, cand, tx)
		return err
	}))
	require.True(t, conflicted,
		"a still-active claim held by a PRIOR run of the same node, on the same supervisor, must still "+
			"conflict — self-exclusion must be keyed on NodeRunID, not (HolderNodeID, HolderSupervisorID)")
	require.False(t, persistentConflict)
}

// @story: claim-handoff-durable
func TestEvaluateClaimScopeConflict_SameRunOwnPriorClaimIsExcluded(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fx := openSelfExclusionFixture(ctx, t)
	tables := fx.tables

	const producer = "self-exclusion-store"
	const supervisorID = "sup-A"
	candidateRunID := seedSelfExclusionRun(ctx, t, fx)

	insertConflictCandidateClaim(ctx, t, tables, producer, supervisorID, fx.nodeID, candidateRunID)

	fake := storetest.NewFake(producer, claimproducer.Capabilities{})
	args := RunArgs{Persist: tables, ClaimHandles: tables.ClaimHandles(), SupervisorID: supervisorID}
	spec := claimproducer.ClaimSpec{ProducerName: producer, Selector: "shared-scope", Intent: "rw"}
	cand := persistence.Candidate{NodeID: fx.nodeID, NodeRunID: candidateRunID}

	var conflicted bool
	require.NoError(t, tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		var err error
		conflicted, _, err = evaluateClaimScopeConflict(ctx, args, fake, spec, cand, tx)
		return err
	}))
	require.False(t, conflicted,
		"a claim already held by THIS SAME run must remain self-excluded")
}
