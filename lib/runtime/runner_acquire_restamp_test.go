// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	_ "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	tmplspec "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

func TestRestampLinkedSubClaimHolders_MovesParentStampedRowsOnly(t *testing.T) {
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

	const supParent = "sup-parent-acquirer"
	const supLeaf = "sup-leaf-acquirer"

	templateHash := "sha256-" + uuid.NewString()
	instanceID := shared.UUID(uuid.New())
	mainScopeID := shared.UUID(uuid.New())
	nodeID := shared.UUID(uuid.New())
	leafRunID := shared.UUID(uuid.New())
	parentClaimID := shared.UUID(uuid.New())
	subClaimID := shared.UUID(uuid.New())
	ownClaimID := shared.UUID(uuid.New())

	producer := "restamp-store"
	intent := "rw"
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := tables.Templates().Insert(ctx, persistence.TemplateInsertInput{
			ID:     templateHash,
			Spec:   tmplspec.TemplateSpec{Name: "restamp-fixture", Version: "1", FrameTimeoutMs: 600000},
			State:  persistence.TemplateStateRegistered,
			Source: "direct",
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
			ID: nodeID, InstanceID: instanceID, NodeType: "leaf", Executor: "stub",
		}, tx); err != nil {
			return err
		}
		msgID := shared.UUID(uuid.New())
		if err := tables.Messages().Insert(ctx, tx, persistence.EnqueueMessageRequest{
			ID:         msgID,
			InstanceID: instanceID,
			Type:       "test/seed",
			Sender:     "test",
			SenderKind: "operator",
		}); err != nil {
			return err
		}
		frameID, err := tables.Frames().InsertRunningFrame(ctx, instanceID, msgID, mainScopeID, 600000, tx)
		if err != nil {
			return err
		}
		if err := tables.NodeRunTree().CreateRootNodeRun(ctx, tx, persistence.CreateRootNodeRunInput{
			NodeRunID: leafRunID, NodeID: nodeID, FrameID: frameID,
			RunScopeID: mainScopeID, ExecutorName: "stub",
		}); err != nil {
			return err
		}
		if err := tables.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID: parentClaimID, LockKind: persistence.LockKindScope,
			ProducerName: &producer, ClaimScopeData: json.RawMessage(`{"p":"root"}`),
			Intent: &intent, HolderSupervisorID: supParent, HolderNodeID: nodeID,
			ExpiresAt: time.Now().Add(10 * time.Minute),
		}, tx); err != nil {
			return err
		}
		leafRunIDCopy := leafRunID
		parentClaimIDCopy := parentClaimID
		if err := tables.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID: subClaimID, LockKind: persistence.LockKindScope,
			NodeRunID:    &leafRunIDCopy,
			ProducerName: &producer, ClaimScopeData: json.RawMessage(`{"p":"alpha"}`),
			Intent: &intent, HolderSupervisorID: supParent, HolderNodeID: nodeID,
			ExpiresAt:           time.Now().Add(10 * time.Minute),
			ParentClaimHandleID: &parentClaimIDCopy,
		}, tx); err != nil {
			return err
		}
		return tables.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID: ownClaimID, LockKind: persistence.LockKindScope,
			NodeRunID:    &leafRunIDCopy,
			ProducerName: &producer, ClaimScopeData: json.RawMessage(`{"p":"own"}`),
			Intent: &intent, HolderSupervisorID: supLeaf, HolderNodeID: nodeID,
			ExpiresAt: time.Now().Add(10 * time.Minute),
		}, tx)
	}); err != nil {
		t.Fatalf("seed fixture: %v", err)
	}

	args := RunArgs{
		Persist:      tables,
		ClaimHandles: tables.ClaimHandles(),
		Logger:       shared.SilentLogger{},
		Clock:        shared.SystemClock{},
		SupervisorID: supLeaf,
	}
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return restampLinkedSubClaimHolders(ctx, args, tx, persistence.Candidate{
			NodeRunID: leafRunID, NodeID: nodeID, NodeType: "leaf",
		})
	}); err != nil {
		t.Fatalf("restampLinkedSubClaimHolders: %v", err)
	}

	holderOf := func(id shared.UUID) string {
		var row *persistence.ClaimHandleRow
		if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			var err error
			row, err = tables.ClaimHandles().Get(ctx, id, tx)
			return err
		}); err != nil {
			t.Fatalf("load claim handle %s: %v", id, err)
		}
		if row == nil || row.HolderSupervisorID == nil {
			t.Fatalf("claim handle %s missing or holderless", id)
		}
		return *row.HolderSupervisorID
	}

	if got := holderOf(subClaimID); got != supLeaf {
		t.Errorf("linked sub-claim holder = %q, want the acquiring supervisor %q", got, supLeaf)
	}
	if got := holderOf(ownClaimID); got != supLeaf {
		t.Errorf("the leaf's own claim must be untouched (already %q), got %q", supLeaf, got)
	}
	if got := holderOf(parentClaimID); got != supParent {
		t.Errorf("the parent claim (not linked to the leaf run) must keep its holder %q, got %q", supParent, got)
	}
}
