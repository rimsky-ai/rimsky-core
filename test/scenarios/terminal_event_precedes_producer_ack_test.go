// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: claim-producer
// @concept: terminal-resolution
package scenarios

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/service"
	stubstore "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/testfixture"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestTerminalEventStandsBeforeTheProducerHearsAnything(t *testing.T) {
	t.Parallel()

	syncCaps := claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
	}
	endpoint, store, teardown := stubfixture.Start(t, stubstore.Config{Capabilities: syncCaps})
	t.Cleanup(teardown)

	h := scenario.Start(t, scenario.HarnessOpts{
		NoScheduler:  true,
		NoSupervisor: true,
		ClaimProducers: config.RemoteClaimProducersConfig{
			ClaimProducers: map[string]config.ClaimProducerEntry{
				"settle-store": {Endpoint: "grpc://" + endpoint, Capabilities: syncCaps},
			},
		},
	})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "terminal-event-before-ack", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "acquirer", Executor: "stub"},
				scenario.WithClaimProducers(scenario.AliasedClaimRef("settle-store", "@thing", "rw", "held")),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-terminal-event-before-ack", map[string]any{})

	acq := h.FindNode(iid, "acquirer")
	require.NotNil(t, acq)
	frameID := h.GetRunningFrameID(iid)
	mainScopeID := h.GetLatestFrameRootRunScopeID(iid)
	const supervisorID = "settle-supervisor"

	h.ExecSQL(`DELETE FROM rimsky_node_runs WHERE node_id = $1`, acq.ID)
	acqRunID := uuid.New()
	h.ExecSQL(
		`INSERT INTO rimsky_node_runs (id, node_id, executor_name, required_claim_producers, enqueued_at, frame_id, run_scope_id, state, creation_reason, sequence)
		 VALUES ($1, $2, 'stub', '{}', NOW() - INTERVAL '10 seconds', $3, $4, 'fresh', 'cascade', 1)`,
		acqRunID, acq.ID, frameID, mainScopeID,
	)
	chID := uuid.New()
	h.ExecSQL(
		`INSERT INTO rimsky_claim_handles
		   (id, node_run_id, lock_kind, producer_name, claim_scope_data, address, intent,
		    is_held, holder_supervisor_id, holder_node_id, expires_at, frame_id, state)
		 VALUES ($1, $2, 'claim_scope', 'settle-store', '"@thing"', '"@thing"', 'rw',
		         TRUE, $3, $4, NOW() + INTERVAL '10 minutes', $5, 'active')`,
		chID, acqRunID, supervisorID, acq.ID, frameID,
	)
	h.ExecSQL(
		`INSERT INTO rimsky_claim_holders (id, claim_handle_id, holder_run_id, state, completed_at)
		 VALUES ($1, $2, $3, 'completed', NOW())`,
		uuid.New(), chID, acqRunID,
	)

	client, err := service.Dial(h.Ctx, "settle-store", "grpc://"+endpoint, service.TLSModeOff)
	require.NoError(t, err)
	t.Cleanup(client.Close)
	registry := locks.NewRegistry()
	registry.Add("settle-store", client)

	args := runtime.RunArgs{
		Persist:               h.Persist,
		Queue:                 h.Queue,
		ClaimHandles:          h.Persist.ClaimHandles(),
		ClaimProducerRegistry: registry,
		Clock:                 shared.SystemClock{},
		Logger:                shared.SilentLogger{},
		SupervisorID:          supervisorID,
	}

	require.NoError(t, h.Persist.Transaction(h.Ctx, func(ctx context.Context, tx persistence.Tx) error {
		_, err := runtime.CheckAndFireResolution(ctx, args, chID, tx)
		return err
	}))

	for _, c := range store.Calls() {
		require.NotContains(t, []string{"commit", "abandon", "release"}, c.Verb,
			"precondition: the outbox has not been flushed, so the producer must not have been told anything yet")
	}

	var evs persistence.EventListResult
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Events().List(h.Ctx,
			persistence.EventListFilter{InstanceID: &iid, KindIn: []string{events.KindClaimResolutionCommit().String()}},
			persistence.ListPagination{Limit: 10}, tx)
		evs = r
		return err
	}))
	require.NotEmpty(t, evs.Events,
		"the terminal event records rimsky's settlement decision, so it must stand before the producer has acknowledged anything")
	require.Equal(t, chID.String(), evs.Events[0].Payload.Map()["claim_handle_id"],
		"the event must name the claim handle rimsky settled")

	flushed, ferr := runtime.FlushProducerVerbOutbox(h.Ctx, args)
	require.NoError(t, ferr)
	require.Positive(t, flushed,
		"the outbox row written in the settling transaction is what eventually tells the producer")

	sawCommit := false
	for _, c := range store.Calls() {
		if c.Verb == "commit" {
			sawCommit = true
		}
	}
	require.True(t, sawCommit,
		"at-least-once outbox delivery — not the event — is what guarantees the producer hears")
}
