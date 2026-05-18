// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Regression coverage for the scope-conflict property the Stage-4
// claim-handle state-column refactor must preserve: a committed-
// durable row continues to occupy its scope (asset surface) and any
// subsequent acquire of the same byte-equal scope sees it via
// ListByProducerScope, even though `holder_supervisor_id` is NULL.

package runtime_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/foundation/shared"
	"github.com/fallguy/rimsky/foundation/spec"
	"github.com/fallguy/rimsky/graph/node"
	"github.com/fallguy/rimsky/internal/pgtest"
)

func TestScopeConflict_CommittedDurableStillConflicts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
	backend := d.Tables()

	tmpl := insertDeployedTemplate(ctx, t, backend, node.TemplateSpec{
		Name: "scope-conflict-" + uuid.NewString(), Version: "1",
		Nodes: []node.TemplateNodeDef{{Type: "n", Executor: "stub"}},
	})
	var inst persistence.InstanceRow
	var nd persistence.NodeRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		i, err := backend.Instances().Create(ctx, persistence.InstanceCreateInput{
			ID:           shared.UUID(uuid.New()),
			TemplateHash: tmpl.ID,
			Params:       map[string]any{},
		}, tx)
		if err != nil {
			return err
		}
		inst = i
		n, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: inst.ID, NodeType: "n", Executor: "stub",
		}, tx)
		if err != nil {
			return err
		}
		nd = n
		return nil
	}))

	scopeBytes := []byte(`"shared-scope"`)
	producer := "p-x"
	intent := "rw"

	// 1. Acquire a durable claim A on scopeBytes.
	idA := shared.UUID(uuid.New())
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return backend.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID:                 idA,
			LockKind:           persistence.LockKindScope,
			ProducerName:       &producer,
			ScopeData:          scopeBytes,
			Address:            []byte(`"addr-A"`),
			Intent:             &intent,
			HolderSupervisorID: "sup-A",
			HolderNodeID:       nd.ID,
			ExpiresAt:          time.Now().Add(10 * time.Minute),
			Lifetime:           spec.ClaimLifetimeDurable,
		}, tx)
	}))

	// 2. Promote A to committed (simulating durable-Commit at terminal).
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return backend.ClaimHandles().Promote(ctx, idA, "sup-A", spec.ClaimHandleStateCommitted, tx)
	}))

	// Verify the row is state='committed', holder_supervisor_id NULL.
	var rowA *persistence.ClaimHandleRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := backend.ClaimHandles().Get(ctx, idA, tx)
		rowA = r
		return err
	}))
	require.NotNil(t, rowA)
	require.Equal(t, spec.ClaimHandleStateCommitted, rowA.State)
	require.Equal(t, spec.ClaimLifetimeDurable, rowA.Lifetime)
	require.Empty(t, rowA.HolderSupervisorID, "committed row must have holder_supervisor_id NULL")

	// 3. ListByProducerScope MUST surface the committed-durable row so
	//    a new acquire's in-Go scope-conflict check sees the
	//    byte-equal scope as taken. This is the load-bearing property:
	//    a committed-durable row remains in conflict-detection scope
	//    until its producer Releases it (via instance termination or
	//    operator DELETE /assets/{alias}). Without this, two writers
	//    could simultaneously hold the same logical scope —
	//    @blessed-invariant 4b violation.
	var hits []persistence.ClaimHandleRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		rows, err := backend.ClaimHandles().ListByProducerScope(ctx, producer, tx)
		hits = rows
		return err
	}))
	require.Len(t, hits, 1, "ListByProducerScope must surface the committed-durable row for conflict detection")
	require.Equal(t, idA, hits[0].ID)
	require.Equal(t, spec.ClaimHandleStateCommitted, hits[0].State)
	require.Equal(t, spec.ClaimLifetimeDurable, hits[0].Lifetime)
	require.Equal(t, string(scopeBytes), string(hits[0].ScopeData),
		"surfaced row must carry the byte-equal scope")
}

func TestScopeConflict_CommittedSubgraphDoesNotConflict(t *testing.T) {
	// Counterpoint to the durable case: a committed-subgraph row does
	// NOT participate in conflict detection. The producer Released the
	// scope on subgraph-Commit; only the rimsky-side ledger row
	// lingers for forensics until retention sweep reaps it.
	t.Parallel()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
	backend := d.Tables()

	tmpl := insertDeployedTemplate(ctx, t, backend, node.TemplateSpec{
		Name: "scope-conflict-sg-" + uuid.NewString(), Version: "1",
		Nodes: []node.TemplateNodeDef{{Type: "n", Executor: "stub"}},
	})
	var inst persistence.InstanceRow
	var nd persistence.NodeRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		i, err := backend.Instances().Create(ctx, persistence.InstanceCreateInput{
			ID:           shared.UUID(uuid.New()),
			TemplateHash: tmpl.ID,
			Params:       map[string]any{},
		}, tx)
		if err != nil {
			return err
		}
		inst = i
		n, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: inst.ID, NodeType: "n", Executor: "stub",
		}, tx)
		if err != nil {
			return err
		}
		nd = n
		return nil
	}))

	scopeBytes := []byte(`"shared-scope-sg"`)
	producer := "p-sg"
	intent := "rw"

	idA := shared.UUID(uuid.New())
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := backend.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID:                 idA,
			LockKind:           persistence.LockKindScope,
			ProducerName:       &producer,
			ScopeData:          scopeBytes,
			Address:            []byte(`"addr-A"`),
			Intent:             &intent,
			HolderSupervisorID: "sup-A",
			HolderNodeID:       nd.ID,
			ExpiresAt:          time.Now().Add(10 * time.Minute),
			Lifetime:           spec.ClaimLifetimeSubgraph,
		}, tx); err != nil {
			return err
		}
		return backend.ClaimHandles().Promote(ctx, idA, "sup-A", spec.ClaimHandleStateCommitted, tx)
	}))

	var hits []persistence.ClaimHandleRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		rows, err := backend.ClaimHandles().ListByProducerScope(ctx, producer, tx)
		hits = rows
		return err
	}))
	require.Empty(t, hits,
		"committed-subgraph row must NOT participate in scope-conflict detection (producer Released the scope at Commit)")
}
