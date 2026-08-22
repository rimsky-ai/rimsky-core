// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package runtime_test

import (
	"context"
	"errors"
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

// @concept: claim-producer
// @concept: event-log
func TestResolveClaimHandleTerminal_EventRowRecordsRimskysDecisionWithoutTheProducersAcknowledgement(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgdbtest.OpenDriver(ctx, t)
	backend := d.Tables()

	tmpl := insertDeployedTemplate(ctx, t, backend, node.TemplateSpec{
		Name: "terminal-row-independent-of-producer", Version: "1",
	})
	ck := "ck-terminal-row-independent"
	var mainScopeID shared.UUID
	var inst persistence.InstanceRow
	var workerNode persistence.NodeRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		i, ms := seedInstanceWithMainScope(ctx, t, backend, tmpl.ID, &ck, tx)
		inst = i
		mainScopeID = ms
		n, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: inst.ID, NodeType: "worker", Executor: "stub",
		}, tx)
		if err != nil {
			return err
		}
		workerNode = n
		return nil
	}))

	frameID := seedFrame(ctx, t, backend, inst.ID, mainScopeID)
	nodeRunID := seedRunForNode(ctx, t, backend, d.Queue(), workerNode.ID, frameID)

	const supervisorID = "sup-terminal-row-independent"
	claimID := shared.UUID(uuid.New())
	producerName := "refusing-store"
	intent := "rw"
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return backend.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID:                 claimID,
			LockKind:           persistence.LockKindScope,
			ProducerName:       &producerName,
			ClaimScopeData:     []byte(`"independent-scope"`),
			Address:            []byte(`"independent-addr"`),
			Intent:             &intent,
			HolderSupervisorID: supervisorID,
			HolderNodeID:       workerNode.ID,
			ExpiresAt:          time.Now().Add(10 * time.Minute),
			NodeRunID:          &nodeRunID,
		}, tx)
	}))

	reg := locks.NewRegistry()
	store := storetest.NewFake(producerName, claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
	})
	store.ErrorFunc = func(verb string, _ claimproducer.ClaimID) error {
		if verb == "commit" {
			return errors.New("producer refuses to acknowledge the commit")
		}
		return nil
	}
	reg.Add(producerName, store)
	args := runtime.RunArgs{
		Persist:               backend,
		ClaimHandles:          backend.ClaimHandles(),
		ClaimProducerRegistry: reg,
		Logger:                shared.SilentLogger{},
		Clock:                 shared.SystemClock{},
		SupervisorID:          supervisorID,
	}

	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		_, err := runtime.ResolveClaimHandleTerminal(ctx, args, runtime.TerminalDecision{
			ClaimHandleID: claimID,
			SupervisorID:  supervisorID,
			Source:        runtime.ActiveTerminal,
			Outcome:       runtime.OutcomeCommit,
			Producer:      store,
			Scope:         []byte(`"independent-scope"`),
			Address:       []byte(`"independent-addr"`),
			ProducerName:  producerName,
			LineageHint: runtime.ClaimLineageHint{
				InstanceID:   inst.ID,
				FrameID:      frameID,
				NodeRunID:    nodeRunID,
				NodeID:       workerNode.ID,
				ProducerName: producerName,
			},
		}, tx)
		return err
	}))

	var res persistence.EventListResult
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := backend.Events().List(ctx, persistence.EventListFilter{
			InstanceID: &inst.ID,
			KindIn:     []string{"claim_resolution.commit"},
		}, persistence.ListPagination{Limit: 10}, tx)
		res = r
		return err
	}))
	require.Len(t, res.Events, 1,
		"the settlement writes its terminal row when rimsky decides, not when the producer answers")
	payload := res.Events[0].Payload.Map()
	require.Equal(t, claimID.String(), payload["claim_handle_id"])
	require.Equal(t, producerName, payload["producer_name"])
}
