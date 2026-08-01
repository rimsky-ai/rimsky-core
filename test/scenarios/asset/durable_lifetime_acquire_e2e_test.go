// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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
	"github.com/rimsky-ai/rimsky-core/test/support/pgdbtest"
)

func TestDurableLifetimePersistedOnAcquire(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgdbtest.OpenDriver(ctx, t)
	backend := d.Tables()

	const (
		producerName = "content"
		nodeType     = "acquirer"
	)

	tmplSpec := node.TemplateSpec{
		Name: "durable-acquire-e2e", Version: "1",
		Nodes: []node.TemplateNodeDef{
			{
				Type:     nodeType,
				Executor: "stub",
				ClaimProducers: []node.NodeClaimProducerRef{
					{
						Name:     producerName,
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
		if err := backend.RunScopes().Create(ctx, persistence.RunScopeRow{
			ID: mainScopeID, GraphName: "main", InstanceID: instID,
		}, tx); err != nil {
			return err
		}
		if _, err := backend.Instances().Create(ctx, persistence.InstanceCreateInput{TargetRoutingIdentity: "test-agent",
			ID: instID, TemplateHash: tmpl.ID,
			InstanceKey: &ck, Params: map[string]any{},
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

	frameID := seedFrameAsset(ctx, t, backend, instID, mainScopeID)
	_ = seedRunForNodeAsset(ctx, t, backend, d.Queue(), acqNode.ID, frameID)

	reg := locks.NewRegistry()
	stubStore := storetest.NewFake(producerName, claimproducer.Capabilities{
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
	reg.Add(producerName, stubStore)

	pool := executor.NewClientPool()
	t.Cleanup(func() { _ = pool.Close() })

	args := runtime.RunArgs{
		Persist:               backend,
		Queue:                 d.Queue(),
		ClaimHandles:          backend.ClaimHandles(),
		AdvisoryLocker:        d.AdvisoryLocker(),
		ClaimProducerRegistry: reg,
		Clock:                 shared.SystemClock{},
		Logger:                shared.SilentLogger{},
		SupervisorID:          "sup-D5",
		Pool:                  pool,
		Resolver: executor.NewStaticResolver(map[string]executor.Endpoint{
			"stub": {Transport: "grpc", URL: "127.0.0.1:1"},
		}),
		LivenessInterval: 100 * time.Millisecond,
	}

	res, err := runtime.RunNode(ctx, args, nil)
	require.NoError(t, err, "RunNode must acquire the fan-out parent")
	require.True(t, res.Ran, "the fan-out parent candidate must be acquired and dispatched")

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
