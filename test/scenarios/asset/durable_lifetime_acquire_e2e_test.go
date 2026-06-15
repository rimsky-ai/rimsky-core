// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// D5 scenario — durable_lifetime_acquire_e2e.
//
// `lifetime: durable` must thread through the REAL acquire path
// (`runtime.RunNode` → tryAcquire → acquireClaim) onto the persisted
// `rimsky_claim_handles.lifetime` column — and onto every fan-out
// sub-claim row (`parent_claim_handle_id = <parent>`).
//
// The companion scenarios `durable_lifetime_e2e_test.go` and
// `durable_lifetime_persistence_test.go` INSERT a durable row directly
// (bypassing acquireClaim), so neither catches a lifetime that is
// dropped on the acquire path. This scenario drives the supervisor's
// claim acquisition end to end against a real Postgres and asserts the
// row the acquire path itself wrote.
//
// Construction: a fan-out parent node holds a `lifetime: durable`
// scope claim against an in-process Fake store. Driving the fan-out
// parent through RunNode acquires the parent claim, splits it into
// sub-claims, creates the child runs, and returns WITHOUT dispatching
// the parent to an executor (the parent run stays `running`). That
// leaves both the parent claim handle row and every sub-claim row alive
// for direct assertion — and exercises the durable threading on both
// the top-level claim (acquireClaim) and the fan-out sub-claims
// (acquireFanOutIfDeclared → AcquireSubClaims).
//
// @concept: claim-lifetime
// @concept: fan-out
// @concept: claim-handle

package asset

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks/storetest"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor"
	pgtest "github.com/rimsky-ai/rimsky-core/test/support/pgmigrate"
)

func TestDurableLifetimePersistedOnAcquire(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
	backend := d.Tables()

	const (
		storeName = "content"
		nodeType  = "acquirer"
	)

	// @constraint: A fan-out parent node holding a durable scope claim. The producer
	// advertises the DataProcessing mix-in (the canonicalizer requires
	// it for `lifetime: durable`) and SupportsSplitScope so the fan-out
	// sub-claim acquisition fires.
	tmplSpec := node.TemplateSpec{
		Name: "durable-acquire-e2e", Version: "1",
		Nodes: []node.TemplateNodeDef{
			{
				Type:     nodeType,
				Executor: "stub",
				Stores: []node.NodeStoreRef{
					{
						Name:     storeName,
						Selector: "/durable/root",
						Intent:   "rw",
						Alias:    "asset",
						Lifetime: string(spec.ClaimLifetimeDurable),
					},
				},
				FanOut: &node.FanOutSpec{
					Claim:            "asset",
					PartitionRequest: "all",
				},
			},
		},
	}
	tmpl := insertDeployedTemplateAsset(ctx, t, backend, tmplSpec)

	instID := shared.UUID(uuid.New())
	mainScopeID := shared.UUID(uuid.New())
	ck := "ck-durable-acquire-e2e"
	var acqNode persistence.NodeRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := backend.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID: mainScopeID, GraphName: "main", InstanceID: instID,
		}); err != nil {
			return err
		}
		if _, err := backend.Instances().Create(ctx, persistence.InstanceCreateInput{
			ID: instID, TemplateHash: tmpl.ID,
			InstanceKey: &ck, Params: map[string]any{},
			MainRunScopeID: mainScopeID,
		}, tx); err != nil {
			return err
		}
		a, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: instID,
			NodeType: nodeType, Executor: "stub",
		}, tx)
		if err != nil {
			return err
		}
		acqNode = a
		return nil
	}))

	frameID := seedFrameAsset(ctx, t, backend, instID, acqNode.ID)
	_ = seedRunForNodeAsset(ctx, t, backend, d.Queue(), acqNode.ID, frameID)

	// @deliberate: In-process Fake claim producer advertising DataProcessing +
	// SupportsSplitScope. SplitScope returns three durable sub-scopes so
	// the fan-out sub-claim acquisition exercises the inherited-lifetime
	// path.
	reg := locks.NewRegistry()
	stubStore := storetest.NewFake(storeName, claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
		SupportsSplitScope:    true,
		Protocols:             []string{claimproducer.ProtocolDataProcessing},
	})
	parts := []string{"a", "b", "c"}
	stubStore.SplitClaimScopeFunc = func(req claimproducer.SplitClaimScopeRequest) (claimproducer.SplitClaimScopeResponse, error) {
		descs := make([]claimproducer.SubClaimScopeDescriptor, 0, len(parts))
		for _, p := range parts {
			scope, _ := json.Marshal("/durable/root/" + p)
			descs = append(descs, claimproducer.SubClaimScopeDescriptor{
				ClaimScopeData: scope,
				PartitionKey:   p,
			})
		}
		return claimproducer.SplitClaimScopeResponse{SubClaimScopes: descs}, nil
	}
	reg.Add(storeName, stubStore)

	pool := executor.NewClientPool()
	t.Cleanup(func() { _ = pool.Close() })

	args := runtime.RunArgs{
		Persist:           backend,
		Queue:             d.Queue(),
		ClaimHandles:      backend.ClaimHandles(),
		AdvisoryLocker:    d.AdvisoryLocker(),
		StoreRegistry:     reg,
		Clock:             shared.SystemClock{},
		Logger:            shared.SilentLogger{},
		SupervisorID:      "sup-D5",
		AcceptedExecutors: []string{"stub"},
		AcceptedStores:    []string{storeName},
		Pool:              pool,
		Resolver: executor.NewStaticResolver(map[string]executor.Endpoint{
			"stub": {Transport: "grpc", URL: "127.0.0.1:1"},
		}),
		HeartbeatInterval: 100 * time.Millisecond,
	}

	// @constraint: Drive the REAL acquire path. A fan-out parent acquires its claim,
	// splits sub-claims, creates child runs, and returns Ran=true without
	// dispatching the parent to the (unreachable) executor.
	res, err := runtime.RunNode(ctx, args, nil)
	require.NoError(t, err, "RunNode must acquire the fan-out parent")
	require.True(t, res.Ran, "the fan-out parent candidate must be acquired and dispatched")

	// @deliberate: Parent claim handle: the row the REAL acquire path (acquireClaim)
	// wrote must carry lifetime=durable.
	var parent *persistence.ClaimHandleRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		rows, err := backend.ClaimHandles().ListByHolderNode(ctx, acqNode.ID, tx)
		if err != nil {
			return err
		}
		for i := range rows {
			if rows[i].ParentClaimHandleID == nil {
				parent = &rows[i]
			}
		}
		return nil
	}))
	require.NotNil(t, parent, "parent claim handle must persist for the running fan-out parent")
	require.Equal(t, spec.ClaimLifetimeDurable, parent.Lifetime,
		"durable lifetime must thread through acquireClaim onto the persisted parent row")

	// @deliberate: Fan-out sub-case: every child claim row (parent_claim_handle_id =
	// parent) must inherit lifetime=durable.
	var children []persistence.ClaimHandleRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		rows, err := backend.ClaimHandles().ListChildClaimHandles(ctx, parent.ID, tx)
		children = rows
		return err
	}))
	require.Len(t, children, len(parts),
		"one durable sub-claim per fan-out partition must persist")
	for _, c := range children {
		require.Equal(t, spec.ClaimLifetimeDurable, c.Lifetime,
			"fan-out sub-claim %s must inherit lifetime=durable from its parent", c.ID)
	}
}
