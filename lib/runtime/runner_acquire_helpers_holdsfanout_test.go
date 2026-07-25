// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
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
	"github.com/rimsky-ai/rimsky-core/test/support/pgdbtest"
)

// @concept: fan-out
// @concept: claim-co-holdership
func TestAcquireFanOutIfDeclared_HoldsBoundClaimReachesSplitScope(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgdbtest.OpenDriver(ctx, t)
	backend := d.Tables()

	const producerName = "holds-fanout-producer"
	tmplSpec := &node.TemplateSpec{
		Name:    "holds-fanout-tmpl",
		Version: "1",
		Nodes: []node.TemplateNodeDef{
			{
				Type:     "producer",
				Executor: "stub",
				ClaimProducers: []spec.NodeClaimProducerRef{
					{Name: producerName, Selector: "sel", Intent: "rw"},
				},
			},
			{
				Type:     "fan-out-consumer",
				Executor: "stub",
				Holds:    map[string]spec.HoldsBinding{producerName: {From: "producer"}},
				FanOut: &spec.FanOutSpec{
					Claim:            producerName,
					PartitionRequest: `"all"`,
				},
			},
		},
	}
	sum := sha256.Sum256([]byte(tmplSpec.Name + ":" + tmplSpec.Version))
	tmplHash := "sha256-" + hex.EncodeToString(sum[:])
	ck := "ck-holds-fanout"
	instID := shared.UUID(uuid.New())
	mainScopeID := shared.UUID(uuid.New())
	var producerNode, consumerNode persistence.NodeRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := backend.Templates().Insert(ctx, persistence.TemplateInsertInput{
			ID: tmplHash, Spec: *tmplSpec, State: persistence.TemplateStateRegistered,
		}, tx); err != nil {
			return err
		}
		if err := backend.Templates().UpdateState(ctx, tmplHash, persistence.TemplateStateDeployed, tx); err != nil {
			return err
		}
		if err := backend.RunScopes().Create(ctx, persistence.RunScopeRow{
			ID: mainScopeID, GraphName: "main", InstanceID: instID,
		}, tx); err != nil {
			return err
		}
		if _, err := backend.Instances().Create(ctx, persistence.InstanceCreateInput{TargetRoutingIdentity: "test-agent",
			ID: instID, TemplateHash: tmplHash, InstanceKey: &ck, Params: map[string]any{},
		}, tx); err != nil {
			return err
		}
		p, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: instID, NodeType: "producer", Executor: "stub",
		}, tx)
		if err != nil {
			return err
		}
		producerNode = p
		c, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: instID, NodeType: "fan-out-consumer", Executor: "stub",
		}, tx)
		if err != nil {
			return err
		}
		consumerNode = c
		return nil
	}))

	var frameID shared.UUID
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		msgID := shared.UUID(uuid.New())
		if err := backend.Messages().Insert(ctx, persistence.EnqueueMessageRequest{
			ID: msgID, InstanceID: instID, Type: "test/seed", Sender: "test", SenderKind: "operator",
		}, tx); err != nil {
			return err
		}
		fid, err := backend.Frames().InsertRunningFrame(ctx, instID, msgID, mainScopeID, tx)
		frameID = fid
		return err
	}))

	producerNameRef := producerName
	intent := "rw"
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return backend.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID:                 shared.UUID(uuid.New()),
			LockKind:           persistence.LockKindScope,
			ProducerName:       &producerNameRef,
			ClaimScopeData:     []byte(`"parent-scope"`),
			Address:            []byte(`"addr"`),
			Intent:             &intent,
			HolderSupervisorID: "sup-holds-fanout",
			HolderNodeID:       producerNode.ID,
			ExpiresAt:          time.Now().Add(time.Hour),
			FrameID:            &frameID,
		}, tx)
	}))

	errStop := errors.New("stop-after-capture")
	store := storetest.NewFake(producerName, claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
		SupportsSplitScope:    true,
	})
	store.SplitClaimScopeFunc = func(claimproducer.SplitClaimScopeRequest) (claimproducer.SplitClaimScopeResponse, error) {
		return claimproducer.SplitClaimScopeResponse{}, errStop
	}
	reg := locks.NewRegistry()
	reg.Add(producerName, store)

	consumerDef := LookupNodeDef(tmplSpec, "fan-out-consumer")
	require.NotNil(t, consumerDef)

	out := &acquisition{InstanceID: instID, RunScopeID: mainScopeID}
	args := RunArgs{
		Persist:               backend,
		ClaimHandles:          backend.ClaimHandles(),
		ClaimProducerRegistry: reg,
		Logger:                shared.SilentLogger{},
		SupervisorID:          "sup-holds-fanout",
	}
	cand := persistence.Candidate{
		FrameID:   frameID,
		NodeID:    consumerNode.ID,
		NodeRunID: shared.UUID(uuid.New()),
	}

	err := backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return acquireFanOutIfDeclared(ctx, args, instID, out, cand, consumerDef, nil, 30*time.Second, tx)
	})
	if !errors.Is(err, errStop) {
		t.Fatalf("expected a holds:-bound fan_out.claim to be resolved via the co-held claim handle and "+
			"reach SplitScope, not silently degrade to a single ordinary dispatch; got err=%v", err)
	}
}
