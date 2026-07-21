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

	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks/storetest"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
	"github.com/rimsky-ai/rimsky-core/test/support/pgdbtest"
)

func TestSettleFromFanoutChild_ChildOwnAttributesNeverAggregateOntoParentBag(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgdbtest.OpenDriver(ctx, t)
	backend := d.Tables()

	tmpl := insertDeployedTemplate(ctx, t, backend, node.TemplateSpec{
		Name: "child-attr-no-aggregate", Version: "1",
	})
	ck := "ck-child-attr-no-aggregate"
	var mainScopeID shared.UUID
	var inst persistence.InstanceRow
	var parentNode, childNode persistence.NodeRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		i, ms := seedInstanceWithMainScope(ctx, t, backend, tx, tmpl.ID, &ck)
		inst = i
		mainScopeID = ms
		p, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: inst.ID, NodeType: "parent", Executor: "stub",
		}, tx)
		if err != nil {
			return err
		}
		parentNode = p
		c, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: inst.ID, NodeType: "child", Executor: "stub",
		}, tx)
		if err != nil {
			return err
		}
		childNode = c
		return nil
	}))

	frameID := seedFrame(ctx, t, backend, inst.ID, mainScopeID)
	parentNodeRunID := seedRunForNode(ctx, t, backend, d.Queue(), parentNode.ID, frameID)
	childNodeRunID := seedRunForNode(ctx, t, backend, d.Queue(), childNode.ID, frameID)

	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return backend.NodeAttributes().Upsert(ctx, childNodeRunID, childNode.ID, map[string]any{
			"child_secret_output": "leak-me-if-aggregation-is-reintroduced",
			"another_child_key":   float64(42),
		}, tx)
	}))

	reg := locks.NewRegistry()
	store := storetest.NewFake("no-aggregate-store", claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
	})
	store.CommitResult = claimproducer.CommitResult{ProducerMetadata: []byte("producer-metadata-marker")}
	reg.Add("no-aggregate-store", store)
	args := runtime.RunArgs{
		Persist:       backend,
		ClaimHandles:  backend.ClaimHandles(),
		StoreRegistry: reg,
		Logger:        shared.SilentLogger{},
		SupervisorID:  "sup-no-agg",
	}
	args = withSyncVerbFlush(args)

	parentID := shared.UUID(uuid.New())
	childID := shared.UUID(uuid.New())
	intent := "rw"
	pName := "no-aggregate-store"
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := backend.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID:                 parentID,
			LockKind:           persistence.LockKindScope,
			ProducerName:       &pName,
			ClaimScopeData:     []byte(`"parent-scope"`),
			Address:            []byte(`"parent-addr"`),
			Intent:             &intent,
			HolderSupervisorID: "sup-no-agg",
			HolderNodeID:       parentNode.ID,
			ExpiresAt:          time.Now().Add(10 * time.Minute),
			NodeRunID:          &parentNodeRunID,
		}, tx); err != nil {
			return err
		}
		if err := backend.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID:                  childID,
			LockKind:            persistence.LockKindScope,
			ProducerName:        &pName,
			ClaimScopeData:      []byte(`"child-scope"`),
			Address:             []byte(`"child-addr"`),
			Intent:              &intent,
			HolderSupervisorID:  "sup-no-agg",
			HolderNodeID:        childNode.ID,
			NodeRunID:           &childNodeRunID,
			ExpiresAt:           time.Now().Add(10 * time.Minute),
			ParentClaimHandleID: &parentID,
		}, tx); err != nil {
			return err
		}
		return backend.ClaimHandles().BumpExpectedChildrenCount(ctx, parentID, "sup-no-agg", 1, tx)
	}))

	resolveSubclaim(ctx, t, backend, args, childID, parentID, store, runtime.OutcomeCommit)

	var parentAttrs *persistence.NodeAttributesRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := backend.NodeAttributes().GetByRun(ctx, parentNodeRunID, tx)
		parentAttrs = r
		return err
	}))
	require.NotNil(t, parentAttrs, "parent writeback row must exist once the child producer_metadata write lands")

	meta, ok := parentAttrs.Data["producer_metadata"].(map[string]any)
	require.True(t, ok, "parent bag must carry producer_metadata once a child commits (proves the writeback path actually ran): got %+v", parentAttrs.Data)
	require.NotEmpty(t, meta, "producer_metadata must be non-empty")

	_, hasSecret := parentAttrs.Data["child_secret_output"]
	require.False(t, hasSecret,
		"child's own attribute bag key leaked onto the parent's attribute bag: %+v", parentAttrs.Data)
	_, hasOther := parentAttrs.Data["another_child_key"]
	require.False(t, hasOther,
		"child's own attribute bag key leaked onto the parent's attribute bag: %+v", parentAttrs.Data)

	for _, v := range meta {
		require.NotContains(t, v, "leak-me-if-aggregation-is-reintroduced",
			"child attribute content leaked into producer_metadata: %+v", meta)
	}
}
