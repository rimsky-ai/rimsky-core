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

	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks/storetest"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	_ "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	tmplspec "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
)

// @concept: claim-scope
func TestAcquireClaim_EmptyOpenClaimScopeFallsBackToSeededSelector(t *testing.T) {
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
		producer = "empty-scope-store"
		supMe    = "sup-empty-scope"
	)
	templateHash := "sha256-" + uuid.NewString()
	instanceID := shared.UUID(uuid.New())
	mainScopeID := shared.UUID(uuid.New())
	nodeID := shared.UUID(uuid.New())
	runID := shared.UUID(uuid.New())

	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := tables.Templates().Insert(ctx, persistence.TemplateInsertInput{
			ID: templateHash, Spec: tmplspec.TemplateSpec{Name: "empty-scope-fixture", Version: "1"},
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
		frameID, err := tables.Frames().InsertRunningFrame(ctx, instanceID, msgID, mainScopeID, tx)
		if err != nil {
			return err
		}
		return tables.NodeRunTree().CreateRootNodeRun(ctx, persistence.CreateRootNodeRunInput{
			NodeRunID: runID, NodeID: nodeID, FrameID: frameID, RunScopeID: mainScopeID, ExecutorName: "stub",
		}, tx)
	}); err != nil {
		t.Fatalf("seed fixture: %v", err)
	}

	fake := storetest.NewFake(producer, claimproducer.Capabilities{})
	fake.OpenFunc = func(_ claimproducer.ClaimID, spec claimproducer.ClaimSpec) (claimproducer.OpenOutcome, error) {
		addr, _ := json.Marshal(spec.Selector)
		return claimproducer.OpenOutcome{
			Available: true,
			Result: claimproducer.ClaimResult{
				Address: addr,
			},
		}, nil
	}
	reg := locks.NewRegistry()
	reg.Add(producer, fake)

	args := RunArgs{
		Persist:        tables,
		Queue:          d.Queue(),
		AdvisoryLocker: d.AdvisoryLocker(),
		ClaimHandles:   tables.ClaimHandles(),
		StoreRegistry:  reg,
		Clock:          shared.SystemClock{},
		Logger:         shared.SilentLogger{},
		SupervisorID:   supMe,
	}
	spec := claimproducer.ClaimSpec{ProducerName: producer, Selector: "/empty-scope-selector", Intent: "rw", Alias: "data"}
	cand := persistence.Candidate{NodeRunID: runID, NodeID: nodeID, NodeType: "worker"}

	var (
		lock AcquiredLock
		res  openResult
	)
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		var aErr error
		lock, res, aErr = acquireClaim(ctx, args, instanceID, spec, cand, 5*time.Second, nil, nil, tx)
		return aErr
	}); err != nil {
		t.Fatalf("acquireClaim: %v", err)
	}
	if res != openResultAcquired {
		t.Fatalf("acquireClaim result = %v, want openResultAcquired", res)
	}

	wantScope, err := json.Marshal(spec.Selector)
	if err != nil {
		t.Fatalf("marshal selector: %v", err)
	}

	var row *persistence.ClaimHandleRow
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, gErr := tables.ClaimHandles().Get(ctx, lock.ClaimHandleID, tx)
		row = r
		return gErr
	}); err != nil {
		t.Fatalf("load claim handle: %v", err)
	}
	if row == nil {
		t.Fatalf("claim handle %s missing after acquireClaim", lock.ClaimHandleID)
	}
	if string(row.ClaimScopeData) != string(wantScope) {
		t.Fatalf("claim_scope_data = %s, want the seeded JSON-marshaled selector %s "+
			"(Open returned an empty ClaimScope; the seeded selector must be left in place, not overwritten with empty bytes)",
			row.ClaimScopeData, wantScope)
	}
}

// @concept: write-semantics
func TestEvaluateClaimScopeConflict_HolderWithUnrealizedWriteSemanticsBailsInsteadOfPanicking(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fx := openSelfExclusionFixture(ctx, t)
	tables := fx.tables

	producer := "in-flight-open-store"
	const supervisorID = "sup-A"
	holderRunID := seedSelfExclusionRun(ctx, t, fx)
	candidateRunID := seedSelfExclusionRun(ctx, t, fx)

	intent := "rw"
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return tables.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID:                 shared.UUID(uuid.New()),
			NodeRunID:          &holderRunID,
			LockKind:           persistence.LockKindScope,
			ProducerName:       &producer,
			ClaimScopeData:     []byte(`"shared-scope"`),
			Intent:             &intent,
			HolderSupervisorID: supervisorID,
			HolderNodeID:       fx.nodeID,
			ExpiresAt:          time.Now().Add(time.Hour),
		}, tx)
	}); err != nil {
		t.Fatalf("insert in-flight holder claim: %v", err)
	}

	fake := storetest.NewFake(producer, claimproducer.Capabilities{})
	args := RunArgs{Persist: tables, ClaimHandles: tables.ClaimHandles(), SupervisorID: supervisorID}
	spec := claimproducer.ClaimSpec{ProducerName: producer, Selector: "shared-scope", Intent: "rw"}
	cand := persistence.Candidate{NodeID: fx.nodeID, NodeRunID: candidateRunID}

	err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		_, _, err := evaluateClaimScopeConflict(ctx, args, fake, spec, cand, tx)
		return err
	})
	if err == nil {
		t.Fatal("evaluateClaimScopeConflict against a holder whose write-semantics is not yet realized " +
			"should bail with an error, not silently coexist or panic")
	}
}
