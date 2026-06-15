// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: instance
// @concept: run-scope

// @constraint: InstancesDeleteCascade conformance area.
// Pins that Instances.Delete walks the entire run-scope tree atomically:
// child RunScopes (subgraph + fanout_partition), their node-run rows,
// and the parent's own main-scope rows all disappear with the instance.
// Both drivers rely on the schema's ON DELETE CASCADE chain declared in
// migrations 007 + 008 (rimsky_run_scopes.{instance_id,parent_run_id,
// parent_run_scope_id} and rimsky_node_runs.run_scope_id), so deleting
// the instance row alone walks the subtree.
//
// Pre-CASCADE the runtime issued an explicit topological walk that
// erroneously deleted rimsky_node_runs rows that rimsky_run_scopes.
// parent_run_id pointed at, surfacing as FK 23503 at statement end on
// postgres (NO ACTION default; checked at statement end). This test
// pins the corrected behavior.
//
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

// testInstancesDeleteCascadeRunScopeTree creates an instance with:
//   - 1 main RunScope (the fixture's default)
//   - 1 fanout_partition RunScope (parent: a run row in main scope)
//   - 1 subgraph RunScope (parent: a run row in the fanout partition)
//   - dispatch rows under each RunScope (main, fanout, subgraph)
//
// Then deletes the instance and asserts every row in the tree is gone
// (uses rawQuery to count rows directly against the schema, since the
// application-layer RunScopes accessor doesn't expose a ListByInstance
// surface).
func testInstancesDeleteCascadeRunScopeTree(
	t *testing.T, d persistence.Database,
	rawQuery func(t *testing.T, d persistence.Database, sql string, args ...any) []RawQueryRow,
) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	// @constraint: this run row is the parent_run_id of the fanout partition
	// scope created below, anchoring the cascade-tree fixture.
	mainScopeRunID := seedConformanceRunForNode(ctx, t, d, fix.NodeID, fix.FrameID)

	// @constraint: the rimsky_run_scopes CHECK constraint requires both
	// parent_run_scope_id and parent_run_id to be set together for non-main
	// scopes.
	fanoutScopeID := shared.UUID(uuid.New())
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID:               fanoutScopeID,
			ParentRunScopeID: &fix.MainRunScopeID,
			ParentRunID:      &mainScopeRunID,
			GraphName:        spec.MainGraphName,
			InstanceID:       fix.InstanceID,
			PartitionKey:     "part-a",
		})
	}); err != nil {
		t.Fatalf("Create fanout RunScope: %v", err)
	}

	// @constraint: this fanout run row becomes the parent_run_id of the
	// subgraph scope below, pinning the nested-tree cascade case where
	// parent_run_id points at a run row that itself lives in a child scope.
	fanoutRunID := shared.UUID(uuid.New())
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.RunTree().CreateChildRun(ctx, tx, persistence.CreateChildRunInput{
			RunID:        fanoutRunID,
			NodeID:       fix.NodeID,
			FrameID:      fix.FrameID,
			RunScopeID:   fanoutScopeID,
			ExecutorName: "test-executor",
		})
	}); err != nil {
		t.Fatalf("CreateChildRun fanout: %v", err)
	}

	// @constraint: subgraph scope's parent_run_id points at fanoutRunID,
	// a row that lives in the fanout scope itself — the cascade must
	// traverse a child-scope run row to reach the subgraph.
	subgraphScopeID := shared.UUID(uuid.New())
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID:               subgraphScopeID,
			ParentRunScopeID: &fanoutScopeID,
			ParentRunID:      &fanoutRunID,
			GraphName:        "subgraph",
			InstanceID:       fix.InstanceID,
			PartitionKey:     "",
		})
	}); err != nil {
		t.Fatalf("Create subgraph RunScope: %v", err)
	}

	// @constraint: a run row inside the subgraph scope forces the cascade
	// to walk to the deepest layer of the tree.
	subgraphRunID := shared.UUID(uuid.New())
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.RunTree().CreateChildRun(ctx, tx, persistence.CreateChildRunInput{
			RunID:        subgraphRunID,
			NodeID:       fix.NodeID,
			FrameID:      fix.FrameID,
			RunScopeID:   subgraphScopeID,
			ExecutorName: "test-executor",
		})
	}); err != nil {
		t.Fatalf("CreateChildRun subgraph: %v", err)
	}

	// @constraint: claim handles + holders are seeded to exercise the FK
	// cascade chain — rimsky_claim_handles.node_run_id → rimsky_node_runs.id
	// ON DELETE SET NULL and rimsky_claim_holders.holder_run_id →
	// rimsky_node_runs.id ON DELETE CASCADE. Fan-out parents in production
	// carry a parent_claim_handle_id with child claim handles + holders; this
	// pins that the parent claim handle's node_run_id → rimsky_node_runs FK
	// chain unwinds via rimsky_instances → rimsky_nodes ON DELETE CASCADE,
	// and the rimsky_claim_holders cascade removes the holder rows.
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
		// @constraint: parent claim handle's node_run_id binds to the fanout
		// run row so the FK cascade chain through that run row is exercised.
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
		// @constraint: child claim handle holds on the subgraph run row with
		// parent_claim_handle_id set, exercising both the node_run_id and
		// parent_claim_handle_id FKs simultaneously.
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
		// @constraint: claim holder row is keyed on the subgraph run so the
		// rimsky_claim_holders.holder_run_id ON DELETE CASCADE FK fires.
		return store.ClaimHolders().Insert(ctx, persistence.ClaimHolderInsertInput{
			ID:            holderID,
			ClaimHandleID: childClaimHandleID,
			HolderRunID:   subgraphRunID,
		}, tx)
	}); err != nil {
		t.Fatalf("seed claim handles + holder: %v", err)
	}

	// @constraint: pre-delete counts guard against the post-delete=0
	// assertion succeeding vacuously against an empty starting state.
	if preScopes := countScopesByInstance(t, d, rawQuery, fix.InstanceID); preScopes < 3 {
		t.Fatalf("pre-delete: expected ≥3 RunScopes (main+fanout+subgraph), got %d", preScopes)
	}
	if preRuns := countRunsByInstanceScopes(t, d, rawQuery, fix.InstanceID); preRuns < 3 {
		t.Fatalf("pre-delete: expected ≥3 node_runs (main+fanout+subgraph), got %d", preRuns)
	}
	if preHandles := countClaimHandlesByInstance(t, d, rawQuery, fix.InstanceID); preHandles < 2 {
		t.Fatalf("pre-delete: expected ≥2 claim_handles (parent+child), got %d", preHandles)
	}
	if preHolders := countClaimHoldersByInstance(t, d, rawQuery, fix.InstanceID); preHolders < 1 {
		t.Fatalf("pre-delete: expected ≥1 claim_holder, got %d", preHolders)
	}

	// @constraint: a single Instances.Delete must walk the entire tree via
	// the schema's ON DELETE CASCADE chain — no explicit topological walk.
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.Instances().Delete(ctx, fix.InstanceID, tx)
	}); err != nil {
		t.Fatalf("Instances.Delete: %v", err)
	}

	// @constraint: every RunScope and node_run row for the deleted instance
	// must be gone post-delete — the cascade owns the full run-scope tree.
	if n := countScopesByInstance(t, d, rawQuery, fix.InstanceID); n != 0 {
		t.Fatalf("post-delete: %d RunScope rows remain for instance %s, want 0",
			n, fix.InstanceID)
	}
	if n := countRunsByInstanceScopes(t, d, rawQuery, fix.InstanceID); n != 0 {
		t.Fatalf("post-delete: %d node_run rows remain for instance %s, want 0",
			n, fix.InstanceID)
	}
	// @constraint: claim_handles.holder_node_id is ON DELETE CASCADE on
	// rimsky_nodes, so when the instance cascade removes the nodes the
	// handles disappear with them — both parent + child handles must be
	// gone.
	if n := countClaimHandlesByID(t, d, rawQuery, parentClaimHandleID); n != 0 {
		t.Fatalf("post-delete: parent claim_handle remains, want 0 got %d", n)
	}
	if n := countClaimHandlesByID(t, d, rawQuery, childClaimHandleID); n != 0 {
		t.Fatalf("post-delete: child claim_handle remains, want 0 got %d", n)
	}
	// @constraint: claim_holders.claim_handle_id is ON DELETE CASCADE on
	// rimsky_claim_handles, so handle removal walks the holder row.
	if n := countClaimHoldersByID(t, d, rawQuery, holderID); n != 0 {
		t.Fatalf("post-delete: claim_holder remains, want 0 got %d", n)
	}
	// @constraint: the instance row itself must be gone post-delete.
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

// countClaimHandlesByInstance counts rimsky_claim_handles rows whose
// holder_node_id binds to a node of the given instance.
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

// countClaimHoldersByInstance counts rimsky_claim_holders rows whose
// holder_run_id belongs to a node_run row of the given instance.
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

// countClaimHandlesByID counts rimsky_claim_handles rows with the
// given id (0 or 1).
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

// countClaimHoldersByID counts rimsky_claim_holders rows with the
// given id (0 or 1).
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

// countScopesByInstance counts rimsky_run_scopes rows whose
// instance_id = id.
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

// countRunsByInstanceScopes counts rimsky_node_runs rows whose
// run_scope_id belongs to a RunScope of the given instance.
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

// scanCount coerces a COUNT(*) raw-query result to int. Postgres returns
// int64; sqlite returns int64 too via the bundled modernc driver. Be
// defensive — both shapes plus float64 (in case of JSON-ish coercion).
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
