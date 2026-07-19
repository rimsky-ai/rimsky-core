// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package controlapi

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks/storetest"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

func TestDeleteInstance_OnRunScopeTerminalFiresBeforeOnInstanceTerminated(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	tplID := registerAndDeployBody(t, h, templateWithClaimProducersAndLocks("del-order-"+uuid.NewString()))
	ck := "ck-" + uuid.NewString()
	status, out := h.httpJSON(t, "POST", "/v1/instances", map[string]any{
		"template":     tplID,
		"instance_key": ck,
	})
	require.Equal(t, http.StatusCreated, status, out)
	instID, err := uuid.Parse(out["instance_id"].(string))
	require.NoError(t, err)

	rootScopeID := uuid.New()
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := h.persist.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID: rootScopeID, GraphName: "main", InstanceID: instID,
		}); err != nil {
			return err
		}
		msgID := uuid.New()
		if err := h.persist.Messages().Insert(ctx, tx, persistence.EnqueueMessageRequest{
			ID:         msgID,
			InstanceID: instID,
			Type:       "test/seed",
			Sender:     "test",
			SenderKind: "operator",
		}); err != nil {
			return err
		}
		_, err := h.persist.Frames().InsertRunningFrame(ctx, instID, msgID, rootScopeID, tx)
		return err
	}))

	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return h.persist.Instances().MarkTerminated(ctx, instID, tx)
	}))

	status, out = h.httpJSON(t, "DELETE", "/v1/instances/"+instID.String(), nil)
	require.Equal(t, http.StatusOK, status, out)

	cp, ok := h.stores.Get("topics-ring")
	require.True(t, ok)
	fake, ok := cp.(*storetest.Fake)
	require.True(t, ok)

	allCalls := fake.Calls()
	var runScopeCall, terminatedCall *storetest.FakeCall
	for i := range allCalls {
		switch allCalls[i].Verb {
		case "on_run_scope_terminal":
			runScopeCall = &allCalls[i]
		case "on_instance_terminated":
			terminatedCall = &allCalls[i]
		}
	}
	require.NotNil(t, runScopeCall, "DELETE /v1/instances/{id} must fan out OnRunScopeTerminal for the frame's root run scope")
	require.NotNil(t, terminatedCall, "DELETE /v1/instances/{id} must fan out OnInstanceTerminated")
	require.Less(t, runScopeCall.Sequence, terminatedCall.Sequence,
		"the explicit-DELETE close site must fire OnRunScopeTerminal strictly before OnInstanceTerminated, "+
			"same as the polling-terminator close site, so main-scope-dependent peers observe run-scope "+
			"closure before the instance is reported terminated")
}
