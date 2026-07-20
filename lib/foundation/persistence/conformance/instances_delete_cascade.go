// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: instance
// @concept: run-scope
package conformance

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

// @concept: run-scope
func testCreateChildNodeRunRefusesClosedScope(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	mainScopeRunID := seedConformanceRunForNode(ctx, t, d, fix.NodeID, fix.FrameID)

	fanoutScopeID := shared.UUID(uuid.New())
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID:               fanoutScopeID,
			ParentRunScopeID: &fix.MainRunScopeID,
			ParentNodeRunID:  &mainScopeRunID,
			GraphName:        spec.MainGraphName,
			InstanceID:       fix.InstanceID,
			PartitionKey:     "closed-part",
		})
	}); err != nil {
		t.Fatalf("Create fanout RunScope: %v", err)
	}
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.RunScopes().Close(ctx, tx, fanoutScopeID)
	}); err != nil {
		t.Fatalf("Close fanout RunScope: %v", err)
	}

	childRunID := shared.UUID(uuid.New())
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.NodeRunTree().CreateChildNodeRun(ctx, tx, persistence.CreateChildNodeRunInput{
			NodeRunID:    childRunID,
			NodeID:       fix.NodeID,
			FrameID:      fix.FrameID,
			RunScopeID:   fanoutScopeID,
			ExecutorName: "test-executor",
		})
	}); err != nil {
		t.Fatalf("CreateChildNodeRun into closed scope: %v", err)
	}

	var got *persistence.NodeRunTreeRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, err := store.NodeRunTree().GetByID(ctx, tx, childRunID)
		got = r
		return err
	}); err != nil {
		t.Fatalf("GetByID after closed-scope CreateChildNodeRun: %v", err)
	}
	if got != nil {
		t.Fatalf("CreateChildNodeRun inserted a row into a closed run scope: %+v", got)
	}
}

func testInstancesDeleteCascadeRunScopeTree(
	t *testing.T, d persistence.Database,
	rawQuery func(t *testing.T, d persistence.Database, sql string, args ...any) []RawQueryRow,
) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	mainScopeRunID := seedConformanceRunForNode(ctx, t, d, fix.NodeID, fix.FrameID)

	fanoutScopeID := shared.UUID(uuid.New())
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID:               fanoutScopeID,
			ParentRunScopeID: &fix.MainRunScopeID,
			ParentNodeRunID:  &mainScopeRunID,
			GraphName:        spec.MainGraphName,
			InstanceID:       fix.InstanceID,
			PartitionKey:     "part-a",
		})
	}); err != nil {
		t.Fatalf("Create fanout RunScope: %v", err)
	}

	fanoutRunID := shared.UUID(uuid.New())
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.NodeRunTree().CreateChildNodeRun(ctx, tx, persistence.CreateChildNodeRunInput{
			NodeRunID:    fanoutRunID,
			NodeID:       fix.NodeID,
			FrameID:      fix.FrameID,
			RunScopeID:   fanoutScopeID,
			ExecutorName: "test-executor",
		})
	}); err != nil {
		t.Fatalf("CreateChildNodeRun fanout: %v", err)
	}

	subgraphScopeID := shared.UUID(uuid.New())
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID:               subgraphScopeID,
			ParentRunScopeID: &fanoutScopeID,
			ParentNodeRunID:  &fanoutRunID,
			GraphName:        "subgraph",
			InstanceID:       fix.InstanceID,
			PartitionKey:     "",
		})
	}); err != nil {
		t.Fatalf("Create subgraph RunScope: %v", err)
	}

	subgraphRunID := shared.UUID(uuid.New())
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.NodeRunTree().CreateChildNodeRun(ctx, tx, persistence.CreateChildNodeRunInput{
			NodeRunID:    subgraphRunID,
			NodeID:       fix.NodeID,
			FrameID:      fix.FrameID,
			RunScopeID:   subgraphScopeID,
			ExecutorName: "test-executor",
		})
	}); err != nil {
		t.Fatalf("CreateChildNodeRun subgraph: %v", err)
	}

	parentClaimHandleID := shared.UUID(uuid.New())
	childClaimHandleID := shared.UUID(uuid.New())
	holderID := shared.UUID(uuid.New())
	parentSup := "del-cascade-supervisor-parent"
	childSup := "del-cascade-supervisor-child"
	parentLockName := "del-cascade-parent-lock"
	childLockName := "del-cascade-child-lock"
	expires := time.Now().Add(1 * time.Hour)
	address := json.RawMessage(`{"k":"delete-cascade"}`)
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		if err := store.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID:                 parentClaimHandleID,
			NodeRunID:          &fanoutRunID,
			LockKind:           persistence.LockKindNamed,
			LockName:           &parentLockName,
			Address:            address,
			HolderSupervisorID: parentSup,
			HolderNodeID:       fix.NodeID,
			ExpiresAt:          expires,
		}, tx); err != nil {
			return err
		}
		if err := store.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID:                  childClaimHandleID,
			NodeRunID:           &subgraphRunID,
			LockKind:            persistence.LockKindNamed,
			LockName:            &childLockName,
			Address:             address,
			HolderSupervisorID:  childSup,
			HolderNodeID:        fix.NodeID,
			ExpiresAt:           expires,
			ParentClaimHandleID: &parentClaimHandleID,
		}, tx); err != nil {
			return err
		}
		return store.ClaimHolders().Insert(ctx, persistence.ClaimHolderInsertInput{
			ID:              holderID,
			ClaimHandleID:   childClaimHandleID,
			HolderNodeRunID: subgraphRunID,
		}, tx)
	}); err != nil {
		t.Fatalf("seed claim handles + holder: %v", err)
	}

	if preScopes := countScopesByInstance(t, d, rawQuery, fix.InstanceID); preScopes != 3 {
		t.Fatalf("pre-delete: expected exactly 3 RunScopes (main+fanout+subgraph), got %d", preScopes)
	}
	if preRuns := countRunsByInstanceScopes(t, d, rawQuery, fix.InstanceID); preRuns != 3 {
		t.Fatalf("pre-delete: expected exactly 3 node_runs (main+fanout+subgraph), got %d", preRuns)
	}
	if preHandles := countClaimHandlesByInstance(t, d, rawQuery, fix.InstanceID); preHandles != 2 {
		t.Fatalf("pre-delete: expected exactly 2 claim_handles (parent+child), got %d", preHandles)
	}
	if preHolders := countClaimHoldersByInstance(t, d, rawQuery, fix.InstanceID); preHolders != 1 {
		t.Fatalf("pre-delete: expected exactly 1 claim_holder, got %d", preHolders)
	}
	if preFrames := countFramesByInstance(t, d, rawQuery, fix.InstanceID); preFrames != 1 {
		t.Fatalf("pre-delete: expected exactly 1 frame, got %d", preFrames)
	}
	if preMessages := countMessagesByInstance(t, d, rawQuery, fix.InstanceID); preMessages != 1 {
		t.Fatalf("pre-delete: expected exactly 1 message, got %d", preMessages)
	}
	if preNodes := countNodesByInstance(t, d, rawQuery, fix.InstanceID); preNodes != 1 {
		t.Fatalf("pre-delete: expected exactly 1 node, got %d", preNodes)
	}

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.Instances().Delete(ctx, fix.InstanceID, tx)
	}); err != nil {
		t.Fatalf("Instances.Delete: %v", err)
	}

	if n := countScopesByInstance(t, d, rawQuery, fix.InstanceID); n != 0 {
		t.Fatalf("post-delete: %d RunScope rows remain for instance %s, want 0",
			n, fix.InstanceID)
	}
	if n := countRunsByInstanceScopes(t, d, rawQuery, fix.InstanceID); n != 0 {
		t.Fatalf("post-delete: %d node_run rows remain for instance %s, want 0",
			n, fix.InstanceID)
	}
	if n := countClaimHandlesByID(t, d, rawQuery, parentClaimHandleID); n != 0 {
		t.Fatalf("post-delete: parent claim_handle remains, want 0 got %d", n)
	}
	if n := countClaimHandlesByID(t, d, rawQuery, childClaimHandleID); n != 0 {
		t.Fatalf("post-delete: child claim_handle remains, want 0 got %d", n)
	}
	if n := countClaimHoldersByID(t, d, rawQuery, holderID); n != 0 {
		t.Fatalf("post-delete: claim_holder remains, want 0 got %d", n)
	}
	if n := countFramesByInstance(t, d, rawQuery, fix.InstanceID); n != 0 {
		t.Fatalf("post-delete: %d frame rows remain for instance %s, want 0 (ON DELETE CASCADE)", n, fix.InstanceID)
	}
	if n := countMessagesByInstance(t, d, rawQuery, fix.InstanceID); n != 0 {
		t.Fatalf("post-delete: %d message rows remain for instance %s, want 0 (ON DELETE CASCADE)", n, fix.InstanceID)
	}
	if n := countNodesByInstance(t, d, rawQuery, fix.InstanceID); n != 0 {
		t.Fatalf("post-delete: %d node rows remain for instance %s, want 0 (ON DELETE CASCADE)", n, fix.InstanceID)
	}
	var inst *persistence.InstanceRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, err := store.Instances().Get(ctx, fix.InstanceID, tx)
		inst = r
		return err
	}); err != nil {
		t.Fatalf("post-delete Instances.Get: %v", err)
	}
	if inst != nil {
		t.Fatalf("post-delete: instance row remains, want nil")
	}
}

func countClaimHandlesByInstance(
	t *testing.T, d persistence.Database,
	rawQuery func(*testing.T, persistence.Database, string, ...any) []RawQueryRow,
	id shared.UUID,
) int {
	t.Helper()
	rows := rawQuery(t, d,
		`SELECT COUNT(*) AS n FROM rimsky_claim_handles
		  WHERE holder_node_id IN (
		    SELECT id FROM rimsky_nodes WHERE instance_id = ?
		  )`,
		id.String(),
	)
	if len(rows) == 0 {
		return 0
	}
	return scanCount(t, rows[0]["n"])
}

func countClaimHoldersByInstance(
	t *testing.T, d persistence.Database,
	rawQuery func(*testing.T, persistence.Database, string, ...any) []RawQueryRow,
	id shared.UUID,
) int {
	t.Helper()
	rows := rawQuery(t, d,
		`SELECT COUNT(*) AS n FROM rimsky_claim_holders
		  WHERE holder_run_id IN (
		    SELECT r.id FROM rimsky_node_runs r
		      JOIN rimsky_run_scopes s ON s.id = r.run_scope_id
		     WHERE s.instance_id = ?
		  )`,
		id.String(),
	)
	if len(rows) == 0 {
		return 0
	}
	return scanCount(t, rows[0]["n"])
}

func countClaimHandlesByID(
	t *testing.T, d persistence.Database,
	rawQuery func(*testing.T, persistence.Database, string, ...any) []RawQueryRow,
	id shared.UUID,
) int {
	t.Helper()
	rows := rawQuery(t, d,
		`SELECT COUNT(*) AS n FROM rimsky_claim_handles WHERE id = ?`,
		id.String(),
	)
	if len(rows) == 0 {
		return 0
	}
	return scanCount(t, rows[0]["n"])
}

func countClaimHoldersByID(
	t *testing.T, d persistence.Database,
	rawQuery func(*testing.T, persistence.Database, string, ...any) []RawQueryRow,
	id shared.UUID,
) int {
	t.Helper()
	rows := rawQuery(t, d,
		`SELECT COUNT(*) AS n FROM rimsky_claim_holders WHERE id = ?`,
		id.String(),
	)
	if len(rows) == 0 {
		return 0
	}
	return scanCount(t, rows[0]["n"])
}

func countScopesByInstance(
	t *testing.T, d persistence.Database,
	rawQuery func(*testing.T, persistence.Database, string, ...any) []RawQueryRow,
	id shared.UUID,
) int {
	t.Helper()
	rows := rawQuery(t, d,
		`SELECT COUNT(*) AS n FROM rimsky_run_scopes WHERE instance_id = ?`,
		id.String(),
	)
	if len(rows) == 0 {
		return 0
	}
	return scanCount(t, rows[0]["n"])
}

func countFramesByInstance(
	t *testing.T, d persistence.Database,
	rawQuery func(*testing.T, persistence.Database, string, ...any) []RawQueryRow,
	id shared.UUID,
) int {
	t.Helper()
	rows := rawQuery(t, d,
		`SELECT COUNT(*) AS n FROM rimsky_frames WHERE instance_id = ?`,
		id.String(),
	)
	if len(rows) == 0 {
		return 0
	}
	return scanCount(t, rows[0]["n"])
}

func countMessagesByInstance(
	t *testing.T, d persistence.Database,
	rawQuery func(*testing.T, persistence.Database, string, ...any) []RawQueryRow,
	id shared.UUID,
) int {
	t.Helper()
	rows := rawQuery(t, d,
		`SELECT COUNT(*) AS n FROM rimsky_messages WHERE instance_id = ?`,
		id.String(),
	)
	if len(rows) == 0 {
		return 0
	}
	return scanCount(t, rows[0]["n"])
}

func countNodesByInstance(
	t *testing.T, d persistence.Database,
	rawQuery func(*testing.T, persistence.Database, string, ...any) []RawQueryRow,
	id shared.UUID,
) int {
	t.Helper()
	rows := rawQuery(t, d,
		`SELECT COUNT(*) AS n FROM rimsky_nodes WHERE instance_id = ?`,
		id.String(),
	)
	if len(rows) == 0 {
		return 0
	}
	return scanCount(t, rows[0]["n"])
}

func countRunsByInstanceScopes(
	t *testing.T, d persistence.Database,
	rawQuery func(*testing.T, persistence.Database, string, ...any) []RawQueryRow,
	id shared.UUID,
) int {
	t.Helper()
	rows := rawQuery(t, d,
		`SELECT COUNT(*) AS n FROM rimsky_node_runs
		  WHERE run_scope_id IN (
		    SELECT id FROM rimsky_run_scopes WHERE instance_id = ?
		  )`,
		id.String(),
	)
	if len(rows) == 0 {
		return 0
	}
	return scanCount(t, rows[0]["n"])
}

func scanCount(t *testing.T, v any) int {
	t.Helper()
	switch n := v.(type) {
	case int64:
		return int(n)
	case int:
		return n
	case float64:
		return int(n)
	}
	t.Fatalf("scanCount: unexpected type %T (%v)", v, v)
	return 0
}
