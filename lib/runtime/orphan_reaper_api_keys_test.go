// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	_ "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	tmplspec "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

// @concept: orphan-reaper
func TestReapers_NeverTouchAPIKeys(t *testing.T) {
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
		deadSup = "sup-dead-reaper-fixture"
		liveSup = "sup-live-reaper-fixture"
	)
	templateHash := "sha256-" + uuid.NewString()
	instanceID := shared.UUID(uuid.New())
	mainScopeID := shared.UUID(uuid.New())
	nodeID := shared.UUID(uuid.New())
	expiredRunID := shared.UUID(uuid.New())
	orphanedRunID := shared.UUID(uuid.New())
	expiredClaimID := shared.UUID(uuid.New())

	apiKeyID := shared.UUID(uuid.New())
	original := persistence.APIKey{
		ID:          apiKeyID,
		Name:        "reaper-fixture-key",
		KeyHash:     []byte("fixture-key-hash-bytes-0123456789"),
		Permissions: []byte(`[{"action":"*"}]`),
		CreatedAt:   time.Now().Add(-1 * time.Hour).UTC().Truncate(time.Second),
	}

	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := tables.Templates().Insert(ctx, persistence.TemplateInsertInput{
			ID: templateHash, Spec: tmplspec.TemplateSpec{Name: "reaper-api-key-fixture", Version: "1"},
			State: persistence.TemplateStateRegistered, Source: "direct",
		}, tx); err != nil {
			return err
		}
		if err := tables.RunScopes().Create(ctx, persistence.RunScopeRow{
			ID: mainScopeID, GraphName: tmplspec.MainGraphName, InstanceID: instanceID,
		}, tx); err != nil {
			return err
		}
		if _, err := tables.Instances().Create(ctx, persistence.InstanceCreateInput{TargetRoutingIdentity: "test-agent",
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
		frameID, err := tables.Frames().InsertRunningFrame(ctx, uuid.UUID(instanceID), msgID, mainScopeID, tx)
		if err != nil {
			return err
		}
		if err := tables.NodeRunTree().CreateRootNodeRun(ctx, persistence.CreateRootNodeRunInput{
			NodeRunID: expiredRunID, NodeID: nodeID, FrameID: frameID, RunScopeID: mainScopeID, ExecutorName: "stub",
		}, tx); err != nil {
			return err
		}
		runIDCopy := expiredRunID
		if err := tables.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID:                 expiredClaimID,
			NodeRunID:          &runIDCopy,
			LockKind:           persistence.LockKindNamed,
			LockName:           stringPtr("reaper-fixture-lock"),
			HolderSupervisorID: deadSup,
			HolderNodeID:       nodeID,
			ExpiresAt:          time.Now().Add(-1 * time.Hour),
		}, tx); err != nil {
			return err
		}
		if err := tables.NodeRunTree().CreateRootNodeRun(ctx, persistence.CreateRootNodeRunInput{
			NodeRunID: orphanedRunID, NodeID: nodeID, FrameID: frameID, RunScopeID: mainScopeID, ExecutorName: "stub",
		}, tx); err != nil {
			return err
		}
		if _, err := d.Queue().ClaimDispatchRow(ctx, orphanedRunID, liveSup, tx); err != nil {
			return err
		}
		maxQuiet := 1
		if err := d.Queue().RegisterAsyncAck(ctx, orphanedRunID, "reaper-fixture-ack", time.Now().Add(-1*time.Hour), &maxQuiet, nil, "", tx); err != nil {
			return err
		}
		return tables.APIKeys().Insert(ctx, original, tx)
	}); err != nil {
		t.Fatalf("seed fixture: %v", err)
	}

	if err := SweepOrphanedClaimHandles(ctx, OrphanReaperArgs{
		Persist: tables, ClaimHandles: tables.ClaimHandles(), Logger: shared.SilentLogger{},
	}); err != nil {
		t.Fatalf("SweepOrphanedClaimHandles: %v", err)
	}
	if err := SweepExecutorDeadlines(ctx, ConductorArgs{
		Persist: tables, Queue: d.Queue(), Clock: shared.SystemClock{}, Logger: shared.SilentLogger{},
	}); err != nil {
		t.Fatalf("SweepExecutorDeadlines: %v", err)
	}

	var claimAfter *persistence.ClaimHandleRow
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		row, gErr := tables.ClaimHandles().Get(ctx, expiredClaimID, tx)
		claimAfter = row
		return gErr
	}); err != nil {
		t.Fatalf("get claim after sweep: %v", err)
	}
	if claimAfter != nil {
		t.Fatalf("sanity: the expired claim fixture should have been reaped by SweepOrphanedClaimHandles, got %+v", claimAfter)
	}

	owner, err := d.Queue().GetClaimedBy(ctx, orphanedRunID)
	if err != nil {
		t.Fatalf("GetClaimedBy after sweep: %v", err)
	}
	if owner.Kind != "unclaimed" {
		t.Fatalf("sanity: the orphaned dispatch fixture should have been released by SweepExecutorDeadlines, got %+v", owner)
	}

	got, found, err := tables.APIKeys().GetByID(ctx, apiKeyID, nil)
	if err != nil {
		t.Fatalf("APIKeys().GetByID: %v", err)
	}
	if !found {
		t.Fatalf("no reaper may delete an api_keys row; the fixture key is gone")
	}
	got.CreatedAt = got.CreatedAt.UTC().Truncate(time.Second)
	if !reflect.DeepEqual(got, original) {
		t.Fatalf("no reaper may modify an api_keys row; before=%+v after=%+v", original, got)
	}
}

func stringPtr(s string) *string { return &s }
