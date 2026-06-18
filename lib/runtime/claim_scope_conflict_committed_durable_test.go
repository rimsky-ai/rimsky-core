// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	pgtest "github.com/rimsky-ai/rimsky-core/test/support/pgmigrate"
)

func TestClaimScopeConflict_CommittedDurableStillConflicts(t *testing.T) {
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
		i, _ := seedInstanceWithMainScope(ctx, t, backend, tx, tmpl.ID, nil)
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

	idA := shared.UUID(uuid.New())
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return backend.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID:                 idA,
			LockKind:           persistence.LockKindScope,
			ProducerName:       &producer,
			ClaimScopeData:     scopeBytes,
			Address:            []byte(`"addr-A"`),
			Intent:             &intent,
			HolderSupervisorID: "sup-A",
			HolderNodeID:       nd.ID,
			ExpiresAt:          time.Now().Add(10 * time.Minute),
			Lifetime:           spec.ClaimLifetimeDurable,
		}, tx)
	}))

	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return backend.ClaimHandles().Promote(ctx, idA, "sup-A", spec.ClaimHandleStateCommitted, tx)
	}))

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

	var hits []persistence.ClaimHandleRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		rows, err := backend.ClaimHandles().ListByProducerClaimScope(ctx, producer, tx)
		hits = rows
		return err
	}))
	require.Len(t, hits, 1, "ListByProducerClaimScope must surface the committed-durable row for conflict detection")
	require.Equal(t, idA, hits[0].ID)
	require.Equal(t, spec.ClaimHandleStateCommitted, hits[0].State)
	require.Equal(t, spec.ClaimLifetimeDurable, hits[0].Lifetime)
	require.Equal(t, string(scopeBytes), string(hits[0].ClaimScopeData),
		"surfaced row must carry the byte-equal claim-scope")
}

func TestClaimScopeConflict_CommittedSubgraphDoesNotConflict(t *testing.T) {
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
		i, _ := seedInstanceWithMainScope(ctx, t, backend, tx, tmpl.ID, nil)
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
			ClaimScopeData:     scopeBytes,
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
		rows, err := backend.ClaimHandles().ListByProducerClaimScope(ctx, producer, tx)
		hits = rows
		return err
	}))
	require.Empty(t, hits,
		"committed-subgraph row must NOT participate in scope-conflict detection (producer Released the scope at Commit)")
}
