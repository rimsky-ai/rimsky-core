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
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
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
		Name:           "park-spill-fixture",
		Version:        "1",
		FrameTimeoutMs: 600000,
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
		if err := tables.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID: mainScopeID, GraphName: spec.MainGraphName, InstanceID: instanceID,
		}); err != nil {
			return err
		}
		if _, err := tables.Instances().Create(ctx, persistence.InstanceCreateInput{
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
		if err := tables.Messages().Insert(ctx, tx, persistence.EnqueueMessageRequest{
			ID: msgID, InstanceID: instanceID, Type: "test/seed", Sender: "test", SenderKind: "operator",
		}); err != nil {
			return err
		}
		fid, err := tables.Frames().InsertRunningFrame(ctx, instanceID, msgID, mainScopeID, 600000, tx)
		if err != nil {
			return err
		}
		frameID = fid
		if err := q.EnqueueInTx(ctx, persistence.DispatchRequest{
			NodeID:                 nodeID,
			ExecutorName:           "test-executor",
			RequiredClaimProducers: []string{},
			EnqueuedAt:             time.Now().Add(-1 * time.Second),
			FrameID:                frameID,
			RunScopeID:             mainScopeID,
		}, tx); err != nil {
			return err
		}
		cands, err := q.SelectCandidates(ctx, tx, persistence.SelectCandidatesRequest{
			AcceptedExecutors:      []string{"test-executor"},
			AcceptedClaimProducers: []string{},
			Limit:                  16,
		})
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
		claimed, err := q.ClaimDispatchRow(ctx, tx, nodeRunID, "sup-park-spill")
		if err != nil {
			return err
		}
		if !claimed {
			t.Fatalf("seedRunningNodeForParkFixture: run %s not claimable", nodeRunID)
		}
		promoted, err := q.PromoteClaimedToRunning(ctx, tx, nodeRunID, "sup-park-spill")
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

func TestApplyTerminalPark_OverThresholdScratchSpillsThroughParkedPayload(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	args, acq, tables := seedRunningNodeForParkFixture(t)

	blob := persistence.NewMemoryBackend()
	args.Blob = blob
	args.BlobSpillThreshold = 8

	scratch := []byte("this parked scratch payload is well over the spill threshold")

	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		_, err := applyTerminalPark(ctx, args, acq, terminalEvent{
			Kind:       terminalKindPark,
			ParkReason: genv1.ParkReason_PARK_REASON_AWAIT_CALLBACK,
			Scratch:    scratch,
		}, tx)
		return err
	}); err != nil {
		t.Fatalf("applyTerminalPark: %v", err)
	}

	var inline []byte
	var handle, handleBackend string
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		var lerr error
		inline, handle, handleBackend, lerr = args.Queue.LoadScratchInTx(ctx, tx, acq.NodeRunID)
		return lerr
	}); err != nil {
		t.Fatalf("LoadScratchInTx: %v", err)
	}

	if len(inline) != 0 {
		t.Fatalf("expected empty scratch_inline for spilled parked payload, got %q", inline)
	}
	if handle == "" {
		t.Fatalf("expected non-empty scratch_handle for spilled parked payload")
	}
	if handleBackend != blob.Name() {
		t.Fatalf("scratch_handle_backend = %q; want %q", handleBackend, blob.Name())
	}

	roundTripped, err := blob.Read(ctx, persistence.Handle(handle))
	if err != nil {
		t.Fatalf("blob.Read(%q): %v", handle, err)
	}
	if string(roundTripped) != string(scratch) {
		t.Fatalf("round-tripped parked scratch = %q; want %q", roundTripped, scratch)
	}
}

func TestApplyTerminalPark_AtOrBelowThresholdScratchStaysInline(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	args, acq, tables := seedRunningNodeForParkFixture(t)

	blob := persistence.NewMemoryBackend()
	args.Blob = blob
	args.BlobSpillThreshold = 4096

	scratch := []byte("small parked scratch")

	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		_, err := applyTerminalPark(ctx, args, acq, terminalEvent{
			Kind:       terminalKindPark,
			ParkReason: genv1.ParkReason_PARK_REASON_AWAIT_CALLBACK,
			Scratch:    scratch,
		}, tx)
		return err
	}); err != nil {
		t.Fatalf("applyTerminalPark: %v", err)
	}

	var inline []byte
	var handle, handleBackend string
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		var lerr error
		inline, handle, handleBackend, lerr = args.Queue.LoadScratchInTx(ctx, tx, acq.NodeRunID)
		return lerr
	}); err != nil {
		t.Fatalf("LoadScratchInTx: %v", err)
	}

	if string(inline) != string(scratch) {
		t.Fatalf("scratch_inline = %q; want %q", inline, scratch)
	}
	if handle != "" || handleBackend != "" {
		t.Fatalf("expected empty scratch_handle/backend below threshold, got handle=%q backend=%q",
			handle, handleBackend)
	}
}
