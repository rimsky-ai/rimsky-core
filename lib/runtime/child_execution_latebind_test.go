// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime_test

import (
	"context"
	"encoding/json"
	"testing"

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
	"github.com/rimsky-ai/rimsky-core/test/support/pgdbtest"
)

func TestSettleFromFanoutChild_LateBoundProducer_ResolvesViaContext(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgdbtest.OpenDriver(ctx, t)
	backend := d.Tables()

	tmpl := insertDeployedTemplate(ctx, t, backend, node.TemplateSpec{
		Name: "late-bind-fanout-settle", Version: "1",
	})
	ck := "ck-latebind"
	var mainScopeID shared.UUID
	var inst persistence.InstanceRow
	var parentNode persistence.NodeRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		i, ms := seedInstanceWithMainScope(ctx, t, backend, tmpl.ID, &ck, tx)
		inst = i
		mainScopeID = ms
		p, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: inst.ID, NodeType: "parent", Executor: "stub",
		}, tx)
		if err != nil {
			return err
		}
		parentNode = p
		return nil
	}))

	frameID := seedFrame(ctx, t, backend, inst.ID, mainScopeID)
	parentNodeRunID := seedRunForNode(ctx, t, backend, d.Queue(), parentNode.ID, frameID)

	const lateBoundProducerName = "late-bound-store"
	const proxyName = "host-agent-proxy"
	proxy := storetest.NewFake(proxyName, claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
	})
	reg := locks.NewRegistry(
		locks.WithLookupInstanceBindings(func(_ context.Context, instanceID string) (map[string]json.RawMessage, bool, error) {
			if instanceID != inst.ID.String() {
				return nil, false, nil
			}
			return map[string]json.RawMessage{lateBoundProducerName: json.RawMessage(`{}`)}, true, nil
		}),
		locks.WithLateBindServiceProxies(map[string]string{"claim_producer": proxyName}),
	)
	reg.Add(proxyName, proxy)

	args := runtime.RunArgs{
		Persist:               backend,
		ClaimHandles:          backend.ClaimHandles(),
		ClaimProducerRegistry: reg,
		Logger:                shared.SilentLogger{},
		SupervisorID:          "sup-LB",
	}
	args = withSyncVerbFlush(args)

	policy := spec.AggregationPolicy{Kind: spec.AggregationKindStrict}
	parentID, subIDs := seedFanOutParentAndSubclaims(
		ctx, t, backend, parentNodeRunID, parentNode.ID, "sup-LB",
		lateBoundProducerName, policy, 1,
	)

	subProducer := storetest.NewFake(lateBoundProducerName, claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
	})
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		pc, err := runtime.ResolveClaimHandleTerminal(ctx, args, runtime.TerminalDecision{
			ClaimHandleID:       subIDs[0],
			SupervisorID:        args.SupervisorID,
			Source:              runtime.ActiveTerminal,
			Outcome:             runtime.OutcomeCommit,
			Producer:            subProducer,
			Scope:               []byte(`"sub-scope"`),
			Address:             []byte(`"sub-addr"`),
			Lifetime:            spec.ClaimLifetimeSubgraph,
			ProducerName:        lateBoundProducerName,
			ParentClaimHandleID: &parentID,
		}, tx)
		if err != nil {
			return err
		}
		if pc != nil {
			pc(ctx)
		}
		return nil
	}), "SettleFromFanoutChild must resolve the parent's producer via GetWithContext (the late-bind proxy path), not the static-only Get")

	var parentRow *persistence.ClaimHandleRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := backend.ClaimHandles().Get(ctx, parentID, tx)
		parentRow = r
		return err
	}))
	require.NotNil(t, parentRow)
	require.Equal(t, spec.ClaimHandleStateCommitted, parentRow.State,
		"parent claim must settle to committed once its only fan-out child (acquired through a late-bound proxy producer) resolves")
}
