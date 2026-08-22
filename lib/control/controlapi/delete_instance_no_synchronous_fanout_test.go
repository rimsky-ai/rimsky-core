// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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

// @decision: lifecycle-fanout-after-commit
// @concept: lifecycle-subscriber
// @concept: control-api
func TestDeleteInstance_CallsNoSubscriberSynchronously(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	tplID := registerAndDeployBody(t, h, templateWithClaimProducersAndLocks("del-nofanout-"+uuid.NewString()))
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
		if err := h.persist.RunScopes().Create(ctx, persistence.RunScopeRow{
			ID: rootScopeID, GraphName: "main", InstanceID: instID,
		}, tx); err != nil {
			return err
		}
		msgID := uuid.New()
		if err := h.persist.Messages().Insert(ctx, persistence.EnqueueMessageRequest{
			ID:         msgID,
			InstanceID: instID,
			Type:       "test/seed",
			Sender:     "test",
			SenderKind: "operator",
		}, tx); err != nil {
			return err
		}
		_, err := h.persist.Frames().InsertRunningFrame(ctx, instID, msgID, rootScopeID, tx)
		return err
	}))
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return h.persist.Instances().MarkTerminated(ctx, instID, tx)
	}))

	cp, ok := h.producers.Get("topics-ring")
	require.True(t, ok)
	fake, ok := cp.(*storetest.Fake)
	require.True(t, ok)
	before := len(fake.Calls())

	status, out = h.httpJSON(t, "DELETE", "/v1/instances/"+instID.String(), nil)
	require.Equal(t, http.StatusOK, status, out)
	require.Equal(t, true, out["deleted"])

	for _, c := range fake.Calls()[before:] {
		require.NotEqual(t, "on_run_scope_terminal", c.Verb,
			"the delete route delivers no lifecycle event; the polling terminator alone does")
		require.NotEqual(t, "on_instance_terminated", c.Verb,
			"the delete route delivers no lifecycle event; the polling terminator alone does")
	}
}
