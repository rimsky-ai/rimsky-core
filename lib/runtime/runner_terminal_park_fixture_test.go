// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
)

func seedRunningNodeForParkFixture(t *testing.T) (RunArgs, *acquisition, persistence.Tables) {
	t.Helper()
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
	q := d.Queue()

	templateHash := "sha256-" + uuid.NewString()
	instanceID := shared.UUID(uuid.New())
	mainScopeID := shared.UUID(uuid.New())
	nodeID := shared.UUID(uuid.New())
	var nodeRunID shared.UUID

	tmpl := spec.TemplateSpec{
		Name:    "park-spill-fixture",
		Version: "1",
		Nodes: []spec.TemplateNodeDef{
			{Type: "parker", Executor: "test-executor"},
		},
	}

	var frameID shared.UUID
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := tables.Templates().Insert(ctx, persistence.TemplateInsertInput{
			ID: templateHash, Spec: tmpl, State: persistence.TemplateStateRegistered, Source: "direct",
		}, tx); err != nil {
			return err
		}
		if err := tables.RunScopes().Create(ctx, persistence.RunScopeRow{
			ID: mainScopeID, GraphName: spec.MainGraphName, InstanceID: instanceID,
		}, tx); err != nil {
			return err
		}
		if _, err := tables.Instances().Create(ctx, persistence.InstanceCreateInput{TargetRoutingIdentity: "test-daemon",
			ID: instanceID, TemplateHash: templateHash,
		}, tx); err != nil {
			return err
		}
		if _, err := tables.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: nodeID, InstanceID: instanceID, NodeType: "parker", Executor: "test-executor",
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
		if err != nil {
			return err
		}
		frameID = fid
		if err := q.Enqueue(ctx, persistence.DispatchRequest{
			NodeID:                 nodeID,
			ExecutorName:           "test-executor",
			RequiredClaimProducers: []string{},
			EnqueuedAt:             time.Now().Add(-1 * time.Second),
			FrameID:                frameID,
			RunScopeID:             mainScopeID,
		}, tx); err != nil {
			return err
		}
		cands, err := q.SelectCandidates(ctx, persistence.SelectCandidatesRequest{
			Limit: 16,
		}, tx)
		if err != nil {
			return err
		}
		for _, c := range cands {
			if c.NodeID == nodeID {
				nodeRunID = c.NodeRunID
			}
		}
		if nodeRunID == (shared.UUID{}) {
			t.Fatalf("seedRunningNodeForParkFixture: candidate not surfaced for %s", nodeID)
		}
		claimed, err := q.ClaimDispatchRow(ctx, nodeRunID, "sup-park-spill", tx)
		if err != nil {
			return err
		}
		if !claimed {
			t.Fatalf("seedRunningNodeForParkFixture: run %s not claimable", nodeRunID)
		}
		promoted, err := q.PromoteClaimedToRunning(ctx, nodeRunID, "sup-park-spill", tx)
		if err != nil {
			return err
		}
		if !promoted {
			t.Fatalf("seedRunningNodeForParkFixture: run %s not promoted to running", nodeRunID)
		}
		return nil
	}); err != nil {
		t.Fatalf("seed fixture: %v", err)
	}

	args := RunArgs{
		Persist:      tables,
		Queue:        q,
		ClaimHandles: tables.ClaimHandles(),
		Logger:       shared.SilentLogger{},
		Clock:        shared.SystemClock{},
		SupervisorID: "sup-park-spill",
	}
	acq := &acquisition{
		NodeRunID:  nodeRunID,
		NodeID:     nodeID,
		InstanceID: instanceID,
		NodeType:   "parker",
		Executor:   "test-executor",
		GraphName:  spec.MainGraphName,
		RunScopeID: mainScopeID,
		FrameID:    frameID,
		NodeDef:    &node.TemplateNodeDef{Type: "parker", Executor: "test-executor"},
	}
	return args, acq, tables
}

// @decision: scratch-column
// @decision: attribute-bytes-in-the-row
func TestApplyTerminalPark_LargeScratchRoundTripsWholeThroughTheRow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	args, acq, tables := seedRunningNodeForParkFixture(t)

	scratch := make([]byte, 1<<20)
	for i := range scratch {
		scratch[i] = byte('a' + i%26)
	}

	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		_, err := applyTerminalPark(ctx, args, acq, terminalEvent{
			Kind:         terminalKindPark,
			ParkResumeAt: time.Now().Add(time.Hour),
			Scratch:      scratch,
		}, tx)
		return err
	}); err != nil {
		t.Fatalf("applyTerminalPark: %v", err)
	}

	var loaded []byte
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		var lerr error
		loaded, lerr = args.Queue.LoadScratch(ctx, acq.NodeRunID, tx)
		return lerr
	}); err != nil {
		t.Fatalf("LoadScratch: %v", err)
	}
	if string(loaded) != string(scratch) {
		t.Fatalf("round-tripped parked scratch is %d bytes; want %d", len(loaded), len(scratch))
	}
}
