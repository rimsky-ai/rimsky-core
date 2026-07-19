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

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	_ "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	tmplspec "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

// @concept: parked-state
func TestAcquireOneLock_ParkedHolderBlocksContenderWithoutPreemption(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

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

	const (
		lockName = "deploy-mutex"
		supMe    = "sup-park-lock"
	)
	templateHash := "sha256-" + uuid.NewString()
	instanceID := shared.UUID(uuid.New())
	mainScopeID := shared.UUID(uuid.New())
	holderNodeID := shared.UUID(uuid.New())
	holderRunID := shared.UUID(uuid.New())
	contenderNodeID := shared.UUID(uuid.New())
	contenderRunID := shared.UUID(uuid.New())
	holderClaimID := shared.UUID(uuid.New())

	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := tables.Templates().Insert(ctx, persistence.TemplateInsertInput{
			ID: templateHash, Spec: tmplspec.TemplateSpec{Name: "park-lock-fixture", Version: "1", FrameTimeoutMs: 600000},
			State: persistence.TemplateStateRegistered, Source: "direct",
		}, tx); err != nil {
			return err
		}
		if err := tables.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID: mainScopeID, GraphName: tmplspec.MainGraphName, InstanceID: instanceID,
		}); err != nil {
			return err
		}
		if _, err := tables.Instances().Create(ctx, persistence.InstanceCreateInput{
			ID: instanceID, TemplateHash: templateHash,
		}, tx); err != nil {
			return err
		}
		if _, err := tables.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: holderNodeID, InstanceID: instanceID, NodeType: "holder", Executor: "stub",
		}, tx); err != nil {
			return err
		}
		if _, err := tables.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: contenderNodeID, InstanceID: instanceID, NodeType: "contender", Executor: "stub",
		}, tx); err != nil {
			return err
		}
		msgID := shared.UUID(uuid.New())
		if err := tables.Messages().Insert(ctx, tx, persistence.EnqueueMessageRequest{
			ID: msgID, InstanceID: instanceID, Type: "test/seed", Sender: "test", SenderKind: "operator",
		}); err != nil {
			return err
		}
		frameID, err := tables.Frames().InsertRunningFrame(ctx, instanceID, msgID, mainScopeID, 600000, tx)
		if err != nil {
			return err
		}
		if err := tables.NodeRunTree().CreateRootNodeRun(ctx, tx, persistence.CreateRootNodeRunInput{
			NodeRunID: holderRunID, NodeID: holderNodeID, FrameID: frameID, RunScopeID: mainScopeID, ExecutorName: "stub",
		}); err != nil {
			return err
		}
		if err := tables.NodeRunTree().CreateRootNodeRun(ctx, tx, persistence.CreateRootNodeRunInput{
			NodeRunID: contenderRunID, NodeID: contenderNodeID, FrameID: frameID, RunScopeID: mainScopeID, ExecutorName: "stub",
		}); err != nil {
			return err
		}
		nameCopy := lockName
		holderRunIDCopy := holderRunID
		if err := tables.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID:                 holderClaimID,
			NodeRunID:          &holderRunIDCopy,
			LockKind:           persistence.LockKindNamed,
			LockName:           &nameCopy,
			HolderSupervisorID: supMe,
			HolderNodeID:       holderNodeID,
			ExpiresAt:          time.Now().Add(time.Hour),
			FrameID:            &frameID,
			IsHeld:             false,
		}, tx); err != nil {
			return err
		}
		return tables.NodeRunTree().UpdateStateAndOutcome(ctx, tx, holderRunID, cascade.NodeStateParked, nil)
	}); err != nil {
		t.Fatalf("seed fixture: %v", err)
	}

	var holderRow *persistence.NodeRunTreeRow
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		var gErr error
		holderRow, gErr = tables.NodeRunTree().GetByID(ctx, tx, holderRunID)
		return gErr
	}); err != nil {
		t.Fatalf("GetByID holder: %v", err)
	}
	if holderRow == nil || holderRow.State != cascade.NodeStateParked {
		t.Fatalf("fixture setup failed: holder run must be parked, got %+v", holderRow)
	}

	args := RunArgs{
		Persist:        tables,
		Queue:          d.Queue(),
		AdvisoryLocker: d.AdvisoryLocker(),
		ClaimHandles:   tables.ClaimHandles(),
		StoreRegistry:  locks.NewRegistry(),
		NamedLocks:     locks.NamedLocksConfig{Locks: map[string]locks.NamedLockConfig{lockName: {Limit: 1}}},
		Clock:          shared.SystemClock{},
		Logger:         shared.SilentLogger{},
		SupervisorID:   "sup-contender",
	}
	spec := locks.NamedLockSpec{Name: lockName}
	cand := persistence.Candidate{NodeRunID: contenderRunID, NodeID: contenderNodeID, NodeType: "contender"}

	var (
		lock AcquiredLock
		res  openResult
	)
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		var aErr error
		lock, res, aErr = acquireOneLock(ctx, args, tx, instanceID, spec, cand, 5*time.Second, nil, nil)
		return aErr
	}); err != nil {
		t.Fatalf("acquireOneLock: %v", err)
	}

	if res != openResultBail {
		t.Fatalf("contention for a lock held by a parked node must queue (openResultBail), got %v with lock %+v", res, lock)
	}

	var holderRows []persistence.ClaimHandleRow
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		rows, lErr := tables.ClaimHandles().ListByNodeRun(ctx, holderRunID, tx)
		holderRows = rows
		return lErr
	}); err != nil {
		t.Fatalf("ListByNodeRun holder: %v", err)
	}
	if len(holderRows) != 1 {
		t.Fatalf("parked holder's named lock claim must not be touched by contention, found %d rows", len(holderRows))
	}
	if holderRows[0].ID != holderClaimID || holderRows[0].State != tmplspec.ClaimHandleStateActive {
		t.Fatalf("parked holder's named lock claim must remain active and unpreempted, got %+v", holderRows[0])
	}

	var contenderRows []persistence.ClaimHandleRow
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		rows, lErr := tables.ClaimHandles().ListByNodeRun(ctx, contenderRunID, tx)
		contenderRows = rows
		return lErr
	}); err != nil {
		t.Fatalf("ListByNodeRun contender: %v", err)
	}
	if len(contenderRows) != 0 {
		t.Fatalf("blocked contender must not have been granted a claim handle, found %d rows", len(contenderRows))
	}
}
