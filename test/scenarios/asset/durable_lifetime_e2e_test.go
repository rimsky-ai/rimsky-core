// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// N6 scenario — durable_lifetime_e2e.
//
// Drives the full durable-claim lifecycle against a real Postgres:
//
//  1. A `lifetime: durable` claim handle is acquired and its holding
//     subgraph completes.
//  2. `CheckAndFireResolution` fires the producer Commit and promotes
//     the row to state='committed' — the row survives the auto-terminal
//     Delete (held-durable Promote contract per @blessed-invariant 22).
//  3. `ListByInstanceAndState(committed, durable)` surfaces the row for
//     the instance.
//  4. `ReleaseHeldDurableClaims` fires `producer.Release` and drops
//     the row at instance termination.
//
// Issue 1 / fixer cycle 3: the companion scenario
// `durable_lifetime_persistence_test.go` only pins the
// foundation/spec constants + `ClaimHandleInsertInput` shape; this
// scenario closes the end-to-end gap.
//
// @concept: held-durable
// @concept: auto-terminal
// @concept: claim-handle

package asset

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/foundation/locks"
	"github.com/fallguy/rimsky/foundation/locks/storetest"
	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/foundation/shared"
	"github.com/fallguy/rimsky/foundation/spec"
	"github.com/fallguy/rimsky/graph/node"
	"github.com/fallguy/rimsky/internal/pgtest"
	"github.com/fallguy/rimsky/runtime"
)

func TestDurableLifetimeE2E(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
	backend := d.Tables()

	tmpl := insertDeployedTemplateAsset(ctx, t, backend, node.TemplateSpec{
		Name: "durable-lifetime-e2e", Version: "1",
	})
	ck := "ck-durable-e2e"
	var inst persistence.InstanceRow
	var acqNode persistence.NodeRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		i, err := backend.Instances().Create(ctx, persistence.InstanceCreateInput{
			ID: shared.UUID(uuid.New()), TemplateHash: tmpl.ID,
			InstanceKey: &ck, Params: map[string]any{},
		}, tx)
		if err != nil {
			return err
		}
		inst = i
		a, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: inst.ID,
			NodeType: "acquirer", Executor: "stub",
		}, tx)
		if err != nil {
			return err
		}
		acqNode = a
		return nil
	}))

	reg := locks.NewRegistry()
	storeName := "workspace"
	stubStore := storetest.NewFake(storeName, locks.Capabilities{
		WriteSemanticsAllowed: []locks.WriteSemantics{locks.WriteSemanticsSync},
	})
	reg.Add(storeName, stubStore)

	frameID := seedFrameAsset(ctx, t, backend, inst.ID, acqNode.ID)
	acqRunID := seedRunForNodeAsset(ctx, t, backend, d.Queue(), acqNode.ID, frameID)

	intent := "rw"
	claimHandleID := shared.UUID(uuid.New())
	prodName := storeName
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := backend.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID: claimHandleID, LockKind: persistence.LockKindScope,
			ProducerName: &prodName, ScopeData: []byte(`"durable"`), Address: []byte(`"durable-addr"`),
			Intent:             &intent,
			HolderSupervisorID: "sup-E2E", HolderNodeID: acqNode.ID,
			ExpiresAt: time.Now().Add(10 * time.Minute),
			IsHeld:    true,
			Lifetime:  "durable",
		}, tx); err != nil {
			return err
		}
		return backend.ClaimHolders().Insert(ctx, persistence.ClaimHolderInsertInput{
			ID: shared.UUID(uuid.New()), ClaimHandleID: claimHandleID, HolderRunID: acqRunID,
		}, tx)
	}))

	// Drive the holder to 'completed' so auto-terminal sees a complete
	// holding subgraph.
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return backend.ClaimHolders().CompleteByClaimHandleAndRun(
			ctx, claimHandleID, acqRunID, persistence.ClaimHolderStateCompleted, tx,
		)
	}))

	args := runtime.RunArgs{
		Persist:       backend,
		ClaimHandles:  backend.ClaimHandles(),
		StoreRegistry: reg,
		Logger:        shared.SilentLogger{},
		SupervisorID:  "sup-E2E",
	}
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return runtime.CheckAndFireResolution(ctx, args, tx, claimHandleID)
	}))

	// Row MUST survive — durable promotion flipped state to 'committed'
	// and skipped the Delete (held-durable Promote contract per
	// @blessed-invariant 22).
	var row *persistence.ClaimHandleRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := backend.ClaimHandles().Get(ctx, claimHandleID, tx)
		row = r
		return err
	}))
	require.NotNil(t, row, "durable claim must survive auto-terminal")
	// Post-refactor: durable-Commit promotes the row to state='committed'
	// (Promote-not-delete). The durable property comes from
	// `lifetime='durable'` on the row.
	require.Equal(t, spec.ClaimHandleStateCommitted, row.State,
		"durable claim must be promoted to state=committed at auto-terminal")
	require.Equal(t, spec.ClaimLifetimeDurable, row.Lifetime,
		"durable claim must carry lifetime=durable")

	// ListByInstanceAndState(committed, durable) surfaces the row.
	var durables []persistence.ClaimHandleRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		rows, err := backend.ClaimHandles().ListByInstanceAndState(
			ctx, inst.ID, spec.ClaimHandleStateCommitted, spec.ClaimLifetimeDurable, tx,
		)
		durables = rows
		return err
	}))
	require.Len(t, durables, 1)
	require.Equal(t, claimHandleID, durables[0].ID)

	// Instance termination cleanup fires producer.Release on every
	// durable row + deletes the row.
	var report runtime.HeldDurableReleaseReport
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := runtime.ReleaseHeldDurableClaims(ctx, args, tx, inst.ID, shared.SilentLogger{})
		report = r
		return err
	}))
	require.Equal(t, 1, report.Attempted)
	require.Equal(t, 1, report.Succeeded)
	require.Empty(t, report.Failures)

	// Row MUST be gone.
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := backend.ClaimHandles().Get(ctx, claimHandleID, tx)
		row = r
		return err
	}))
	require.Nil(t, row, "ReleaseHeldDurableClaims must drop the row")

	// Producer.Release MUST have fired.
	releaseSeen := false
	for _, c := range stubStore.Calls() {
		if c.Verb == "release" {
			releaseSeen = true
		}
	}
	require.True(t, releaseSeen, "producer.Release must fire during instance termination cleanup")
}

// insertDeployedTemplateAsset inserts a template row in 'deployed' state
// with a deterministic content hash. Duplicated from the auto_terminal
// fixture so the scenario package stays self-contained.
//
// @source: runtime/auto_terminal_test.go::insertDeployedTemplate
// @diverged: false
func insertDeployedTemplateAsset(ctx context.Context, t *testing.T, sb persistence.Tables, tmplSpec node.TemplateSpec) persistence.TemplateRow {
	t.Helper()
	sum := sha256.Sum256([]byte(tmplSpec.Name + ":" + tmplSpec.Version))
	hash := "sha256-" + hex.EncodeToString(sum[:])
	var row *persistence.TemplateRow
	require.NoError(t, sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := sb.Templates().Insert(ctx, persistence.TemplateInsertInput{
			ID: hash, Spec: tmplSpec, State: persistence.TemplateStateRegistered,
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

// seedFrameAsset creates a 'running' frame for the instance.
//
// @source: runtime/auto_terminal_test.go::seedFrame
// @diverged: false
func seedFrameAsset(ctx context.Context, t *testing.T, sb persistence.Tables, instanceID, sourceNodeID shared.UUID) shared.UUID {
	t.Helper()
	var frameID shared.UUID
	require.NoError(t, sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		fid, err := sb.Frames().EnqueueSerialFrame(ctx, instanceID, sourceNodeID, 600000, tx)
		if err != nil {
			return err
		}
		if _, err := sb.Frames().PromoteQueuedFrameToRunning(ctx, fid, tx); err != nil {
			return err
		}
		frameID = fid
		return nil
	}))
	return frameID
}

// seedRunForNodeAsset enqueues a fresh rimsky_node_runs row for the
// given node and returns the run id.
//
// @source: runtime/auto_terminal_test.go::seedRunForNode
// @diverged: false
func seedRunForNodeAsset(
	ctx context.Context, t *testing.T, sb persistence.Tables, q persistence.Queue,
	nodeID, frameID shared.UUID,
) shared.UUID {
	t.Helper()
	var out shared.UUID
	require.NoError(t, sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := q.EnqueueInTx(ctx, persistence.DispatchRequest{
			NodeID:         nodeID,
			ExecutorName:   "stub",
			RequiredStores: []string{},
			EnqueuedAt:     time.Now().Add(-1 * time.Second),
			FrameID:        frameID,
		}, tx); err != nil {
			return err
		}
		cands, err := q.SelectCandidates(ctx, tx, persistence.SelectCandidatesRequest{
			AcceptedExecutors: []string{"stub"},
			AcceptedStores:    []string{},
			Limit:             16,
		})
		if err != nil {
			return err
		}
		for _, c := range cands {
			if c.NodeID == nodeID {
				out = c.DispatchID
				return nil
			}
		}
		t.Fatalf("seedRunForNodeAsset: candidate not surfaced for %s", nodeID)
		return nil
	}))
	return out
}
