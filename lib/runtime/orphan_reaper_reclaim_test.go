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

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	_ "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	tmplspec "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

// @concept: orphan-reaper
func TestSweepExecutorDeadlines_ReleasedClaimReclaimedByDifferentSupervisor(t *testing.T) {
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
		crashedSup = "sup-crashed-reclaim-fixture"
		newSup     = "sup-new-reclaim-fixture"
	)
	templateHash := "sha256-" + uuid.NewString()
	instanceID := shared.UUID(uuid.New())
	mainScopeID := shared.UUID(uuid.New())
	nodeID := shared.UUID(uuid.New())
	runID := shared.UUID(uuid.New())

	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := tables.Templates().Insert(ctx, persistence.TemplateInsertInput{
			ID: templateHash, Spec: tmplspec.TemplateSpec{Name: "reclaim-fixture", Version: "1"},
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
			ID: nodeID, InstanceID: instanceID, NodeType: "worker", Executor: "stub",
		}, tx); err != nil {
			return err
		}
		msgID := shared.UUID(uuid.New())
		if err := tables.Messages().Insert(ctx, tx, persistence.EnqueueMessageRequest{
			ID: msgID, InstanceID: instanceID, Type: "test/seed", Sender: "test", SenderKind: "operator",
		}); err != nil {
			return err
		}
		frameID, err := tables.Frames().InsertRunningFrame(ctx, uuid.UUID(instanceID), msgID, mainScopeID, tx)
		if err != nil {
			return err
		}
		if err := tables.NodeRunTree().CreateRootNodeRun(ctx, tx, persistence.CreateRootNodeRunInput{
			NodeRunID: runID, NodeID: nodeID, FrameID: frameID, RunScopeID: mainScopeID, ExecutorName: "stub",
		}); err != nil {
			return err
		}
		if _, err := d.Queue().ClaimDispatchRow(ctx, tx, runID, crashedSup); err != nil {
			return err
		}
		maxQuiet := 1
		return d.Queue().RegisterAsyncAck(ctx, tx, runID, "reclaim-fixture-ack",
			time.Now().Add(-1*time.Hour), &maxQuiet, nil, "")
	}); err != nil {
		t.Fatalf("seed fixture: %v", err)
	}

	owner, err := d.Queue().GetClaimedBy(ctx, runID)
	if err != nil {
		t.Fatalf("GetClaimedBy precondition: %v", err)
	}
	if owner.Kind != "claimed_by" || owner.SupervisorID != crashedSup {
		t.Fatalf("precondition: expected claimed_by=%s, got %+v", crashedSup, owner)
	}

	if err := SweepExecutorDeadlines(ctx, ConductorArgs{
		Persist: tables, Queue: d.Queue(), Clock: shared.SystemClock{}, Logger: shared.SilentLogger{},
	}); err != nil {
		t.Fatalf("SweepExecutorDeadlines: %v", err)
	}

	released, err := d.Queue().GetClaimedBy(ctx, runID)
	if err != nil {
		t.Fatalf("GetClaimedBy after sweep: %v", err)
	}
	if released.Kind != "unclaimed" {
		t.Fatalf("the crashed supervisor's dispatch must be released by the reaper, got %+v", released)
	}

	var reclaimed bool
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		cands, err := d.Queue().SelectCandidates(ctx, tx, persistence.SelectCandidatesRequest{
			AcceptedExecutors:      []string{"stub"},
			AcceptedClaimProducers: []string{},
			Limit:                  16,
		})
		if err != nil {
			return err
		}
		for _, c := range cands {
			if c.NodeRunID != runID {
				continue
			}
			ok, cErr := d.Queue().ClaimDispatchRow(ctx, tx, runID, newSup)
			if cErr != nil {
				return cErr
			}
			reclaimed = ok
			return nil
		}
		return nil
	}); err != nil {
		t.Fatalf("reclaim tx: %v", err)
	}
	if !reclaimed {
		t.Fatalf("a different supervisor must be able to reclaim the released dispatch via the ordinary SelectCandidates/ClaimDispatchRow path")
	}

	final, err := d.Queue().GetClaimedBy(ctx, runID)
	if err != nil {
		t.Fatalf("GetClaimedBy final: %v", err)
	}
	if final.Kind != "claimed_by" || final.SupervisorID != newSup {
		t.Fatalf("expected the dispatch to be owned by the new supervisor %s after reclaim, got %+v", newSup, final)
	}
}
