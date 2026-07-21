// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package asset

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
	"github.com/rimsky-ai/rimsky-core/test/support/pgdbtest"
)

func TestDurableLifetimePersistence_TaxonomyConstants(t *testing.T) {
	t.Parallel()
	if spec.ClaimLifetimeSubgraph != "subgraph" {
		t.Errorf("ClaimLifetimeSubgraph = %q, want subgraph", spec.ClaimLifetimeSubgraph)
	}
	if spec.ClaimLifetimeDurable != "durable" {
		t.Errorf("ClaimLifetimeDurable = %q, want durable", spec.ClaimLifetimeDurable)
	}
}

func TestDurableLifetimePersistence_InsertInputCarriesLifetime(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgdbtest.OpenDriver(ctx, t)
	backend := d.Tables()

	tmpl := insertDeployedTemplateAsset(ctx, t, backend, node.TemplateSpec{
		Name: "durable-lifetime-persistence", Version: "1",
	})
	instID := shared.UUID(uuid.New())
	mainScopeID := shared.UUID(uuid.New())
	ck := "ck-durable-lifetime-persistence"
	var acqNode persistence.NodeRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := backend.RunScopes().Create(ctx, persistence.RunScopeRow{
			ID: mainScopeID, GraphName: "main", InstanceID: instID,
		}, tx); err != nil {
			return err
		}
		if _, err := backend.Instances().Create(ctx, persistence.InstanceCreateInput{
			ID: instID, TemplateHash: tmpl.ID,
			InstanceKey: &ck, Params: map[string]any{},
		}, tx); err != nil {
			return err
		}
		a, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: instID,
			NodeType: "acquirer", Executor: "stub",
		}, tx)
		if err != nil {
			return err
		}
		acqNode = a
		return nil
	}))

	claimHandleID := shared.UUID(uuid.New())
	intent := "rw"
	prodName := "workspace"
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return backend.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID: claimHandleID, LockKind: persistence.LockKindScope,
			ProducerName: &prodName, ClaimScopeData: []byte(`"durable"`), Address: []byte(`"durable-addr"`),
			Intent:             &intent,
			HolderSupervisorID: "sup-persist", HolderNodeID: acqNode.ID,
			ExpiresAt: time.Now().Add(10 * time.Minute),
			IsHeld:    true,
			Lifetime:  spec.ClaimLifetimeDurable,
		}, tx)
	}))

	var row *persistence.ClaimHandleRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := backend.ClaimHandles().Get(ctx, claimHandleID, tx)
		row = r
		return err
	}))
	require.NotNil(t, row, "durable claim handle must round-trip through the store")
	require.Equal(t, spec.ClaimLifetimeDurable, row.Lifetime,
		"Lifetime must round-trip as durable, not be lost or defaulted by the insert path")
	require.True(t, row.IsHeld, "IsHeld must round-trip as true for a durable held claim")
}
