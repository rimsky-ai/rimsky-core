// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	_ "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
)

func seedHeldErrorFixture(t *testing.T, curState cascade.NodeState, nodeDef *node.TemplateNodeDef) (RunArgs, *acquisition, persistence.Tables) {
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
	holderNodeID := shared.UUID(uuid.New())
	var holderNodeRunID shared.UUID

	tmpl := spec.TemplateSpec{
		Name:    "held-error-shield-fixture",
		Version: "1",
		Nodes: []spec.TemplateNodeDef{
			{Type: "holder", Executor: "test-executor"},
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
		if _, err := tables.Instances().Create(ctx, persistence.InstanceCreateInput{TargetRoutingIdentity: "test-agent",
			ID: instanceID, TemplateHash: templateHash,
		}, tx); err != nil {
			return err
		}
		if _, err := tables.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: holderNodeID, InstanceID: instanceID, NodeType: "holder", Executor: "test-executor",
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
			NodeID:                 holderNodeID,
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
			if c.NodeID == holderNodeID {
				holderNodeRunID = c.NodeRunID
			}
		}
		if holderNodeRunID == (shared.UUID{}) {
			return fmt.Errorf("seedHeldErrorFixture: candidate not surfaced for %s", holderNodeID)
		}
		claimed, err := q.ClaimDispatchRow(ctx, holderNodeRunID, "sup-held-shield", tx)
		if err != nil {
			return err
		}
		if !claimed {
			return fmt.Errorf("seedHeldErrorFixture: run %s not claimable", holderNodeRunID)
		}
		promoted, err := q.PromoteClaimedToRunning(ctx, holderNodeRunID, "sup-held-shield", tx)
		if err != nil {
			return err
		}
		if !promoted {
			return fmt.Errorf("seedHeldErrorFixture: run %s not promoted to running", holderNodeRunID)
		}
		if curState == cascade.NodeStateHeld {
			if err := tables.Nodes().UpdateState(ctx, holderNodeRunID,
				cascade.NodeStateHeld, cascade.ReasonHandlerHeld, nil, tx); err != nil {
				return fmt.Errorf("seedHeldErrorFixture: transition to held: %w", err)
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("seed fixture: %v", err)
	}

	args := RunArgs{
		Persist:      tables,
		Queue:        d.Queue(),
		ClaimHandles: tables.ClaimHandles(),
		Logger:       shared.SilentLogger{},
		Clock:        shared.SystemClock{},
		SupervisorID: "sup-held-shield",
	}
	if nodeDef == nil {
		nodeDef = &node.TemplateNodeDef{Type: "holder", Executor: "test-executor"}
	}
	acq := &acquisition{
		NodeRunID:  holderNodeRunID,
		NodeID:     holderNodeID,
		InstanceID: instanceID,
		NodeType:   "holder",
		Executor:   "test-executor",
		GraphName:  spec.MainGraphName,
		RunScopeID: mainScopeID,
		FrameID:    frameID,
		NodeDef:    nodeDef,
	}
	return args, acq, tables
}

// @concept: claim-handle
func TestApplyErrorPolicy_HeldRunShieldedFromErrorTransition(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	run := func(t *testing.T, curState cascade.NodeState) cascade.NodeState {
		t.Helper()
		args, acq, tables := seedHeldErrorFixture(t, curState, nil)

		if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			_, err := applyErrorPolicyWithScratch(ctx, args, acq, "boom", "", nil, nil, nil, nil, tx)
			return err
		}); err != nil {
			t.Fatalf("applyErrorPolicyWithScratch: %v", err)
		}

		var runRow *persistence.NodeRunForGate
		if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			r, err := tables.Nodes().GetRunForGate(ctx, acq.NodeRunID, tx)
			runRow = r
			return err
		}); err != nil {
			t.Fatalf("load run: %v", err)
		}
		if runRow == nil {
			t.Fatalf("node run %s missing after applyErrorPolicyWithScratch", acq.NodeRunID)
		}
		return runRow.State
	}

	t.Run("running run fails on error (control)", func(t *testing.T) {
		t.Parallel()
		state := run(t, cascade.NodeStateRunning)
		if state != cascade.NodeStateFailed {
			t.Fatalf("running run state after error = %v, want %v "+
				"(control case: proves the harness actually drives a give-up failure transition)",
				state, cascade.NodeStateFailed)
		}
	})

	t.Run("held run is shielded from the error transition", func(t *testing.T) {
		t.Parallel()
		state := run(t, cascade.NodeStateHeld)
		if state != cascade.NodeStateHeld {
			t.Fatalf("held run state after error = %v, want %v "+
				"(a held run is post-executor idle; the error path must never move it to failed)",
				state, cascade.NodeStateHeld)
		}
	})
}
