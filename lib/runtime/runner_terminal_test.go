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
	signalpkg "github.com/rimsky-ai/rimsky-core/lib/foundation/signal"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
)

func TestWaitSetTopicKindFor_FullTaxonomy(t *testing.T) {
	cases := []struct {
		name    string
		pattern signalpkg.TypePath
		want    string
	}{
		{"terminal/success", signalpkg.TypePath("terminal/success"), "terminal"},
		{"transient/await_async", signalpkg.TypePath("transient/await_async"), "transient"},
		{"attribute/x/changed", signalpkg.TypePath("attribute/x/changed"), "attribute"},
	}

	got := make(map[string]string, len(cases))
	for _, tc := range cases {
		bucket := waitSetTopicKindFor(tc.pattern)
		if bucket != tc.want {
			t.Errorf("waitSetTopicKindFor(%q) = %q, want %q "+
				"(each top-level signal kind must map to its own taxonomy value, "+
				"not a collapsed bucket)", tc.pattern, bucket, tc.want)
		}
		got[tc.name] = bucket
	}

	seen := make(map[string]string, len(got))
	for class, bucket := range got {
		if prior, dup := seen[bucket]; dup {
			t.Errorf("signal classes %q and %q both map to topic_kind %q; "+
				"distinct signal classes must not collapse onto the same wait-set bucket",
				prior, class, bucket)
			continue
		}
		seen[bucket] = class
	}
}

func TestWaitSetTopicKindFor_MessageRetired(t *testing.T) {
	bucket := waitSetTopicKindFor(signalpkg.TypePath("message/invalidate/operator/n"))
	if bucket == "message" {
		t.Fatalf("waitSetTopicKindFor(message/...) = %q, but `message` topic_kind retired", bucket)
	}
}

func seedPoisonPortfolioFixture(
	t *testing.T, claimState spec.ClaimHandleState,
) (RunArgs, *acquisition, persistence.Tables) {
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

	templateHash := "sha256-" + uuid.NewString()
	instanceID := shared.UUID(uuid.New())
	mainScopeID := shared.UUID(uuid.New())
	holderNodeID := shared.UUID(uuid.New())
	var holderNodeRunID shared.UUID
	claimID := shared.UUID(uuid.New())
	q := d.Queue()

	tmpl := spec.TemplateSpec{
		Name:    "poison-portfolio-fixture",
		Version: "1",
		Nodes: []spec.TemplateNodeDef{
			{Type: "holder", Executor: "test-executor"},
		},
	}

	intent := "rw"
	pName := "poison-store"
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
		if _, err := tables.Instances().Create(ctx, persistence.InstanceCreateInput{
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
			AcceptedExecutors:      []string{"test-executor"},
			AcceptedClaimProducers: []string{},
			Limit:                  16,
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
			return fmt.Errorf("seedPoisonPortfolioFixture: candidate not surfaced for %s", holderNodeID)
		}
		claimed, err := q.ClaimDispatchRow(ctx, holderNodeRunID, "sup-poison", tx)
		if err != nil {
			return err
		}
		if !claimed {
			return fmt.Errorf("seedPoisonPortfolioFixture: run %s not claimable", holderNodeRunID)
		}
		promoted, err := q.PromoteClaimedToRunning(ctx, holderNodeRunID, "sup-poison", tx)
		if err != nil {
			return err
		}
		if !promoted {
			return fmt.Errorf("seedPoisonPortfolioFixture: run %s not promoted to running", holderNodeRunID)
		}
		if err := tables.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID: claimID, LockKind: persistence.LockKindScope,
			ProducerName: &pName, ClaimScopeData: []byte(`"x-scope"`), Address: []byte(`"x-addr"`),
			Intent: &intent, HolderSupervisorID: "sup-poison", HolderNodeID: holderNodeID,
			ExpiresAt: time.Now().Add(10 * time.Minute),
		}, tx); err != nil {
			return err
		}
		if err := tables.ClaimHandles().Promote(ctx, claimID, "sup-poison", claimState, tx); err != nil {
			return err
		}
		holderRowID := shared.UUID(uuid.New())
		if err := tables.ClaimHolders().Insert(ctx, persistence.ClaimHolderInsertInput{
			ID: holderRowID, ClaimHandleID: claimID, HolderNodeRunID: holderNodeRunID,
		}, tx); err != nil {
			return err
		}
		settledState := persistence.ClaimHolderStateCompleted
		if claimState == spec.ClaimHandleStateAbandoned {
			settledState = persistence.ClaimHolderStateFailed
		}
		return tables.ClaimHolders().Complete(ctx, holderRowID, settledState, tx)
	}); err != nil {
		t.Fatalf("seed fixture: %v", err)
	}

	args := RunArgs{
		Persist:      tables,
		Queue:        d.Queue(),
		ClaimHandles: tables.ClaimHandles(),
		Logger:       shared.SilentLogger{},
		Clock:        shared.SystemClock{},
		SupervisorID: "sup-poison",
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
		NodeDef:    &node.TemplateNodeDef{Type: "holder", Executor: "test-executor"},
	}
	return args, acq, tables
}

func TestApplyTerminalComplete_HolderPortfolioPoisoned_RoutesToFailedAbandoned(t *testing.T) {
	t.Parallel()

	run := func(t *testing.T, claimState spec.ClaimHandleState) cascade.NodeState {
		t.Helper()
		ctx := context.Background()
		args, acq, tables := seedPoisonPortfolioFixture(t, claimState)

		if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			_, err := applyTerminalComplete(ctx, args, acq, map[string]any{}, nil,
				terminalEvent{Kind: terminalKindComplete, Changed: true}, tx)
			return err
		}); err != nil {
			t.Fatalf("applyTerminalComplete: %v", err)
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
			t.Fatalf("node run %s missing after applyTerminalComplete", acq.NodeRunID)
		}
		return runRow.State
	}

	t.Run("late-settling holder of an already-abandoned claim fails despite its own successful terminal", func(t *testing.T) {
		t.Parallel()
		state := run(t, spec.ClaimHandleStateAbandoned)
		if state != cascade.NodeStateFailed {
			t.Fatalf("run state = %v, want %v (poison rule: a portfolio poisoned by an abandoned claim must route to failed even though this terminal reported success)",
				state, cascade.NodeStateFailed)
		}
	})

	t.Run("late-settling holder of an already-committed claim settles fresh on its own successful terminal", func(t *testing.T) {
		t.Parallel()
		state := run(t, spec.ClaimHandleStateCommitted)
		if state != cascade.NodeStateFresh {
			t.Fatalf("run state = %v, want %v (control: a non-poisoned portfolio must take the ordinary success path)",
				state, cascade.NodeStateFresh)
		}
	})
}
