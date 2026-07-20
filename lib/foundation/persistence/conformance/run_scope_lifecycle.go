// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: run-scope
package conformance

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

func testRunScopeCreateMainAndChild(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	mainScopeID := shared.UUID(uuid.New())
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID:           mainScopeID,
			GraphName:    spec.MainGraphName,
			InstanceID:   fix.InstanceID,
			PartitionKey: "",
		})
	}); err != nil {
		t.Fatalf("Create main: %v", err)
	}

	var got *persistence.RunScopeRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, err := store.RunScopes().GetByID(ctx, tx, mainScopeID)
		got = r
		return err
	}); err != nil {
		t.Fatalf("GetByID main: %v", err)
	}
	if got == nil {
		t.Fatalf("main scope: not found after create")
	}
	if got.ParentRunScopeID != nil {
		t.Fatalf("main scope: parent_run_scope_id = %v, want nil", got.ParentRunScopeID)
	}
	if got.ParentNodeRunID != nil {
		t.Fatalf("main scope: parent_run_id = %v, want nil", got.ParentNodeRunID)
	}
	if got.GraphName != spec.MainGraphName {
		t.Fatalf("main scope: graph_name = %q, want %q", got.GraphName, spec.MainGraphName)
	}

	parentNodeRunID := seedConformanceRunForNode(ctx, t, d, fix.NodeID, fix.FrameID)

	childScopeID := shared.UUID(uuid.New())
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID:               childScopeID,
			ParentRunScopeID: &mainScopeID,
			ParentNodeRunID:  &parentNodeRunID,
			GraphName:        "subgraph",
			InstanceID:       fix.InstanceID,
			PartitionKey:     "part-a",
		})
	}); err != nil {
		t.Fatalf("Create child: %v", err)
	}

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, err := store.RunScopes().GetByID(ctx, tx, childScopeID)
		got = r
		return err
	}); err != nil {
		t.Fatalf("GetByID child: %v", err)
	}
	if got == nil {
		t.Fatalf("child scope: not found after create")
	}
	if got.ParentRunScopeID == nil || *got.ParentRunScopeID != mainScopeID {
		t.Fatalf("child scope: parent_run_scope_id = %v, want %v", got.ParentRunScopeID, mainScopeID)
	}
	if got.ParentNodeRunID == nil || *got.ParentNodeRunID != parentNodeRunID {
		t.Fatalf("child scope: parent_run_id = %v, want %v", got.ParentNodeRunID, parentNodeRunID)
	}

	onlyScopeID := shared.UUID(uuid.New())
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID:               onlyScopeID,
			ParentRunScopeID: &mainScopeID,
			ParentNodeRunID:  nil,
			GraphName:        "subgraph",
			InstanceID:       fix.InstanceID,
			PartitionKey:     "part-only-scope",
		})
	}); err == nil {
		t.Fatalf("Create with ParentRunScopeID set and ParentNodeRunID nil must be rejected " +
			"(the two parent pointers must stand or fall together); got nil error")
	}

	onlyRunID := shared.UUID(uuid.New())
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID:               onlyRunID,
			ParentRunScopeID: nil,
			ParentNodeRunID:  &parentNodeRunID,
			GraphName:        "subgraph",
			InstanceID:       fix.InstanceID,
			PartitionKey:     "part-only-run",
		})
	}); err == nil {
		t.Fatalf("Create with ParentNodeRunID set and ParentRunScopeID nil must be rejected " +
			"(the two parent pointers must stand or fall together); got nil error")
	}
}

func testRunScopeCloseStampsClosedAt(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	scopeID := shared.UUID(uuid.New())
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID:         scopeID,
			GraphName:  spec.MainGraphName,
			InstanceID: fix.InstanceID,
		})
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.RunScopes().Close(ctx, tx, scopeID)
	}); err != nil {
		t.Fatalf("Close: %v", err)
	}

	var got *persistence.RunScopeRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, err := store.RunScopes().GetByID(ctx, tx, scopeID)
		got = r
		return err
	}); err != nil {
		t.Fatalf("GetByID after close: %v", err)
	}
	if got == nil || got.ClosedAt == nil {
		t.Fatalf("Close did not stamp closed_at")
	}
	firstClosedAt := *got.ClosedAt

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.RunScopes().Close(ctx, tx, scopeID)
	}); err != nil {
		t.Fatalf("Close (second call): %v", err)
	}
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, err := store.RunScopes().GetByID(ctx, tx, scopeID)
		got = r
		return err
	}); err != nil {
		t.Fatalf("GetByID after re-close: %v", err)
	}
	if got == nil || got.ClosedAt == nil {
		t.Fatalf("Re-close cleared closed_at")
	}
	if !got.ClosedAt.Equal(firstClosedAt) {
		t.Fatalf("Re-close changed closed_at: was %v, now %v", firstClosedAt, *got.ClosedAt)
	}
}

func testRunScopeAffirmAfterCloseErrRunScopeClosed(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	scopeID := shared.UUID(uuid.New())
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		if err := store.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID:         scopeID,
			GraphName:  spec.MainGraphName,
			InstanceID: fix.InstanceID,
		}); err != nil {
			return err
		}
		return store.RunScopes().Close(ctx, tx, scopeID)
	}); err != nil {
		t.Fatalf("Create+Close: %v", err)
	}

	err := inTx(ctx, store, func(tx persistence.Tx) error {
		_, err := store.Nodes().CreateCascadePending(ctx, tx, fix.NodeID, scopeID, fix.FrameID)
		return err
	})
	if !errors.Is(err, persistence.ErrRunScopeClosed) {
		t.Fatalf("Affirm-after-close: err = %v, want ErrRunScopeClosed", err)
	}
}

func testRunScopeFanoutPartitionUniqueness(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	mainScopeID := shared.UUID(uuid.New())
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID:         mainScopeID,
			GraphName:  spec.MainGraphName,
			InstanceID: fix.InstanceID,
		})
	}); err != nil {
		t.Fatalf("Create main: %v", err)
	}

	parentNodeRunID := seedConformanceRunForNode(ctx, t, d, fix.NodeID, fix.FrameID)

	firstID := shared.UUID(uuid.New())
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID:               firstID,
			ParentRunScopeID: &mainScopeID,
			ParentNodeRunID:  &parentNodeRunID,
			GraphName:        spec.MainGraphName,
			InstanceID:       fix.InstanceID,
			PartitionKey:     "key-a",
		})
	}); err != nil {
		t.Fatalf("Create first fanout_partition: %v", err)
	}

	secondID := shared.UUID(uuid.New())
	err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID:               secondID,
			ParentRunScopeID: &mainScopeID,
			ParentNodeRunID:  &parentNodeRunID,
			GraphName:        spec.MainGraphName,
			InstanceID:       fix.InstanceID,
			PartitionKey:     "key-a",
		})
	})
	if err == nil {
		t.Fatalf("Create second fanout_partition with duplicate (parent_run_id, partition_key): expected unique-violation error, got nil")
	}

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, gErr := store.RunScopes().GetByID(ctx, tx, secondID)
		if gErr != nil {
			return gErr
		}
		if r != nil {
			t.Fatalf("Create second fanout_partition rejected but the row is visible: %+v", r)
		}
		return nil
	}); err != nil {
		t.Fatalf("GetByID(secondID): %v", err)
	}
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, gErr := store.RunScopes().GetByID(ctx, tx, firstID)
		if gErr != nil {
			return gErr
		}
		if r == nil {
			t.Fatalf("first fanout_partition row vanished after the rejected duplicate Create")
		}
		return nil
	}); err != nil {
		t.Fatalf("GetByID(firstID): %v", err)
	}
}

func runScopeKindFromStructuralFields(r persistence.RunScopeRow) string {
	if r.ParentRunScopeID == nil && r.ParentNodeRunID == nil {
		return "root"
	}
	if r.PartitionKey == "" {
		return "sub_graph"
	}
	return "fan_out_partition"
}

func testRunScopeKindDerivableFromStructuralFields(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	rootID := shared.UUID(uuid.New())
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID:         rootID,
			GraphName:  spec.MainGraphName,
			InstanceID: fix.InstanceID,
		})
	}); err != nil {
		t.Fatalf("Create root: %v", err)
	}

	parentNodeRunID := seedConformanceRunForNode(ctx, t, d, fix.NodeID, fix.FrameID)

	subGraphID := shared.UUID(uuid.New())
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID:               subGraphID,
			ParentRunScopeID: &rootID,
			ParentNodeRunID:  &parentNodeRunID,
			GraphName:        "subgraph",
			InstanceID:       fix.InstanceID,
			PartitionKey:     "",
		})
	}); err != nil {
		t.Fatalf("Create sub-graph: %v", err)
	}

	fanOutID := shared.UUID(uuid.New())
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID:               fanOutID,
			ParentRunScopeID: &rootID,
			ParentNodeRunID:  &parentNodeRunID,
			GraphName:        spec.MainGraphName,
			InstanceID:       fix.InstanceID,
			PartitionKey:     "kind-derivation-key",
		})
	}); err != nil {
		t.Fatalf("Create fan-out partition: %v", err)
	}

	cases := []struct {
		id   shared.UUID
		want string
	}{
		{rootID, "root"},
		{subGraphID, "sub_graph"},
		{fanOutID, "fan_out_partition"},
	}
	for _, c := range cases {
		var got *persistence.RunScopeRow
		if err := inTx(ctx, store, func(tx persistence.Tx) error {
			r, err := store.RunScopes().GetByID(ctx, tx, c.id)
			got = r
			return err
		}); err != nil {
			t.Fatalf("GetByID %s: %v", c.id, err)
		}
		if got == nil {
			t.Fatalf("scope %s: not found after create", c.id)
		}
		if kind := runScopeKindFromStructuralFields(*got); kind != c.want {
			t.Fatalf("scope %s: kind derived from structural fields = %q, want %q "+
				"(parent_run_scope_id=%v parent_run_id=%v partition_key=%q)",
				c.id, kind, c.want, got.ParentRunScopeID, got.ParentNodeRunID, got.PartitionKey)
		}
	}
}

func testRunScopeListParentChain(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	mainID := shared.UUID(uuid.New())
	parent1 := seedConformanceRunForNode(ctx, t, d, fix.NodeID, fix.FrameID)
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID:         mainID,
			GraphName:  spec.MainGraphName,
			InstanceID: fix.InstanceID,
		})
	}); err != nil {
		t.Fatalf("Create main: %v", err)
	}
	midID := shared.UUID(uuid.New())
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID:               midID,
			ParentRunScopeID: &mainID,
			ParentNodeRunID:  &parent1,
			GraphName:        "subgraph-mid",
			InstanceID:       fix.InstanceID,
			PartitionKey:     "",
		})
	}); err != nil {
		t.Fatalf("Create mid: %v", err)
	}

	parent2 := seedConformanceRunForNode(ctx, t, d, fix.NodeID, fix.FrameID)
	leafID := shared.UUID(uuid.New())
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID:               leafID,
			ParentRunScopeID: &midID,
			ParentNodeRunID:  &parent2,
			GraphName:        "subgraph-leaf",
			InstanceID:       fix.InstanceID,
			PartitionKey:     "",
		})
	}); err != nil {
		t.Fatalf("Create leaf: %v", err)
	}

	var chain []persistence.RunScopeRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		c, err := store.RunScopes().ListParentChain(ctx, tx, leafID)
		chain = c
		return err
	}); err != nil {
		t.Fatalf("ListParentChain: %v", err)
	}
	if len(chain) != 3 {
		ids := make([]string, len(chain))
		for i, s := range chain {
			ids[i] = s.ID.String()
		}
		t.Fatalf("ListParentChain: got %d entries (%v), want 3", len(chain), ids)
	}
	if chain[0].ID != leafID {
		t.Fatalf("ListParentChain[0] = %s, want leaf %s", chain[0].ID, leafID)
	}
	if chain[1].ID != midID {
		t.Fatalf("ListParentChain[1] = %s, want mid %s", chain[1].ID, midID)
	}
	if chain[2].ID != mainID {
		t.Fatalf("ListParentChain[2] = %s, want main %s", chain[2].ID, mainID)
	}
}
