// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package runtime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	_ "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	tmplspec "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

// @decision: keepalive-endpoint
// @concept: claim-handle
func TestKeepalive_RenewsTheRunsClaimExpiries(t *testing.T) {
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

	const supervisorID = "sup-keepalive-renewal"
	templateHash := "sha256-" + uuid.NewString()
	instanceID := shared.UUID(uuid.New())
	mainScopeID := shared.UUID(uuid.New())
	nodeID := shared.UUID(uuid.New())
	runID := shared.UUID(uuid.New())
	claimID := shared.UUID(uuid.New())
	originalExpiry := time.Now().UTC().Add(30 * time.Second).Truncate(time.Second)

	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := tables.Templates().Insert(ctx, persistence.TemplateInsertInput{
			ID: templateHash, Spec: tmplspec.TemplateSpec{Name: "keepalive-renewal-fixture", Version: "1"},
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
			TargetRoutingIdentity: "test-daemon", ID: instanceID, TemplateHash: templateHash,
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
		frameID, ferr := tables.Frames().InsertRunningFrame(ctx, instanceID, msgID, mainScopeID, tx)
		if ferr != nil {
			return ferr
		}
		if err := tables.NodeRunTree().CreateRootNodeRun(ctx, persistence.CreateRootNodeRunInput{
			NodeRunID: runID, NodeID: nodeID, FrameID: frameID, RunScopeID: mainScopeID, ExecutorName: "stub",
		}, tx); err != nil {
			return err
		}
		producer := "keepalive-renewal-producer"
		runIDCopy := runID
		return tables.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID:                 claimID,
			NodeRunID:          &runIDCopy,
			LockKind:           persistence.LockKindScope,
			ProducerName:       &producer,
			ClaimScopeData:     []byte(`{"path":"/keepalive/renewal"}`),
			HolderSupervisorID: supervisorID,
			HolderNodeID:       nodeID,
			ExpiresAt:          originalExpiry,
			FrameID:            &frameID,
		}, tx)
	}); err != nil {
		t.Fatalf("seed fixture: %v", err)
	}

	clock := shared.NewControllableClock(time.Now().UTC().Add(10 * time.Minute))
	c := &CallbackServer{
		Persist:          tables,
		Queue:            d.Queue(),
		ClaimHandles:     tables.ClaimHandles(),
		Clock:            clock,
		Logger:           shared.SilentLogger{},
		SupervisorID:     supervisorID,
		LivenessInterval: 5 * time.Second,
	}

	rec := httptest.NewRecorder()
	newKeepaliveRouter(c).ServeHTTP(rec, keepaliveRequest(runID.String(), cancelTokenFor(supervisorID, runID)))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("keepalive status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}

	var row *persistence.ClaimHandleRow
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, gerr := tables.ClaimHandles().Get(ctx, claimID, tx)
		row = r
		return gerr
	}); err != nil {
		t.Fatalf("read claim handle: %v", err)
	}
	if row == nil {
		t.Fatalf("claim handle %s missing after keepalive", claimID)
	}
	if !row.ExpiresAt.After(originalExpiry) {
		t.Fatalf("claim expiry = %s, want later than the seeded %s: a keepalive renews the expiry of every "+
			"claim the run holds, so a dispatch long enough to need keepalives keeps its claims ahead of the reaper",
			row.ExpiresAt.UTC(), originalExpiry)
	}
}
