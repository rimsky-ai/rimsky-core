// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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

// @concept: parked-state
func TestAcquireClaim_ResumeReusesHeldRunClaimWithoutReopen(t *testing.T) {
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
		producer = "reuse-store"
		supMe    = "sup-reuse"
	)
	templateHash := "sha256-" + uuid.NewString()
	instanceID := shared.UUID(uuid.New())
	mainScopeID := shared.UUID(uuid.New())
	nodeID := shared.UUID(uuid.New())
	runID := shared.UUID(uuid.New())
	existingClaimID := shared.UUID(uuid.New())

	scope := json.RawMessage(`"s1"`)
	address := json.RawMessage(`"addr-1"`)
	intent := "rw"
	producerName := producer

	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := tables.Templates().Insert(ctx, persistence.TemplateInsertInput{
			ID: templateHash, Spec: tmplspec.TemplateSpec{Name: "reuse-fixture", Version: "1"},
			State: persistence.TemplateStateRegistered, Source: "direct",
		}, tx); err != nil {
			return err
		}
		if err := tables.RunScopes().Create(ctx, persistence.RunScopeRow{
			ID: mainScopeID, GraphName: tmplspec.MainGraphName, InstanceID: instanceID,
		}, tx); err != nil {
			return err
		}
		if _, err := tables.Instances().Create(ctx, persistence.InstanceCreateInput{TargetRoutingIdentity: "test-daemon",
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
		if err := tables.NodeRunTree().CreateRootNodeRun(ctx, persistence.CreateRootNodeRunInput{
			NodeRunID: runID, NodeID: nodeID, FrameID: frameID, RunScopeID: mainScopeID, ExecutorName: "stub",
		}, tx); err != nil {
			return err
		}
		runIDCopy := runID
		return tables.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID:                     existingClaimID,
			NodeRunID:              &runIDCopy,
			LockKind:               persistence.LockKindScope,
			ProducerName:           &producerName,
			ClaimScopeData:         scope,
			Address:                address,
			Intent:                 &intent,
			HolderSupervisorID:     supMe,
			HolderNodeID:           nodeID,
			ExpiresAt:              time.Now().Add(-1 * time.Hour),
			IsHeld:                 true,
			RealizedWriteSemantics: string(claimproducer.WriteSemanticsSync),
		}, tx)
	}); err != nil {
		t.Fatalf("seed fixture: %v", err)
	}

	fake := storetest.NewFake(producer, claimproducer.Capabilities{})
	reg := locks.NewRegistry()
	reg.Add(producer, fake)

	args := RunArgs{
		Persist:               tables,
		Queue:                 d.Queue(),
		AdvisoryLocker:        d.AdvisoryLocker(),
		ClaimHandles:          tables.ClaimHandles(),
		ClaimProducerRegistry: reg,
		Clock:                 shared.SystemClock{},
		Logger:                shared.SilentLogger{},
		SupervisorID:          supMe,
	}
	spec := claimproducer.ClaimSpec{ProducerName: producer, Selector: "s1", Intent: "rw", Alias: "held"}
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
		t.Fatalf("resume re-acquire must reuse the held claim (openResultAcquired), got %v", res)
	}
	if lock.ClaimHandleID != existingClaimID {
		t.Fatalf("reuse must return the existing claim handle %s, got %s", existingClaimID, lock.ClaimHandleID)
	}
	for _, c := range fake.Calls() {
		if c.Verb == "open" {
			t.Fatalf("reuse must NOT re-Open the producer; got an open call for %s", c.ClaimID)
		}
	}

	var row *persistence.ClaimHandleRow
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, gErr := tables.ClaimHandles().Get(ctx, existingClaimID, tx)
		row = r
		return gErr
	}); err != nil {
		t.Fatalf("get reused handle: %v", err)
	}
	if row == nil {
		t.Fatalf("reused claim handle must still exist")
	}
	if !row.ExpiresAt.After(time.Now()) {
		t.Fatalf("reuse must renew the claim's expiry into the future so the reaper cannot reap the now-running holder; expires_at=%s", row.ExpiresAt)
	}

	var handles []persistence.ClaimHandleRow
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		rows, lErr := tables.ClaimHandles().ListByNodeRun(ctx, runID, tx)
		handles = rows
		return lErr
	}); err != nil {
		t.Fatalf("ListByNodeRun: %v", err)
	}
	if len(handles) != 1 {
		t.Fatalf("reuse must not create a duplicate claim handle for the run, found %d", len(handles))
	}
}
