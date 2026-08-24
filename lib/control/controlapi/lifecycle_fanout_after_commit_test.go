// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package controlapi

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks/storetest"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
)

// @decision: lifecycle-fanout-after-commit
// @concept: lifecycle-subscriber
// @concept: control-api
func TestTemplateRegister_FailingSubscriberLeavesTemplateRegistered(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	failSubscriber(t, h, "topics-ring", "on_template_registered")

	body := templateWithClaimProducersAndLocks("reg-subfail-" + uuid.NewString())
	status, out := h.httpJSON(t, "POST", "/v1/templates", body)
	require.Equal(t, http.StatusCreated, status,
		"a subscriber's failure never refuses the registration; body: %v", out)
	hash, _ := out["template_id"].(string)
	require.NotEmpty(t, hash)

	var row *persistence.TemplateRow
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := h.persist.Templates().GetByHash(ctx, hash, tx)
		row = r
		return err
	}))
	require.NotNil(t, row, "the transition commits even though the subscriber errored")

	require.NotEmpty(t, pendingRowsForService(t, h, persistence.LifecycleScopeTemplate, hash, "topics-ring"),
		"the failed delivery stays owed in the outbox, so the drain retries it")

	clearSubscriberFailure(t, h, "topics-ring")
	newStagedLifecycleDrain(h.deps).DrainOnce(ctx)

	require.Empty(t, pendingRowsForService(t, h, persistence.LifecycleScopeTemplate, hash, "topics-ring"),
		"the drain redelivers the undelivered template-scope event and the acknowledged row leaves the outbox")
	require.Equal(t, 1, countSubscriberCalls(t, h, "topics-ring", "on_template_registered"),
		"the retry delivers OnTemplateRegistered once, on the pass after the subscriber recovers")
}

func pendingRowsForService(
	t *testing.T, h *harness, kind persistence.LifecycleScopeKind, scopeID, service string,
) []persistence.LifecycleOutboxRow {
	t.Helper()
	ctx := context.Background()
	var rows []persistence.LifecycleOutboxRow
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := h.persist.LifecycleOutbox().ListPendingForScope(ctx, kind, scopeID, tx)
		rows = nil
		for _, row := range r {
			if row.ClaimProducerName == service {
				rows = append(rows, row)
			}
		}
		return err
	}))
	return rows
}

// @decision: lifecycle-fanout-after-commit
// @concept: lifecycle-subscriber
// @concept: control-api
func TestInstanceCreate_FailingSubscriberLeavesInstanceCreated(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	tplID := registerAndDeployBody(t, h, templateWithClaimProducersAndLocks("create-subfail-"+uuid.NewString()))
	failSubscriber(t, h, "topics-ring", "on_instance_created")

	status, out := h.httpJSON(t, "POST", "/v1/instances", map[string]any{
		"template":     tplID,
		"instance_key": "ck-" + uuid.NewString(),
	})
	require.Equal(t, http.StatusCreated, status,
		"a subscriber's failure never refuses instance creation; body: %v", out)
	instID, _ := out["instance_id"].(string)
	require.NotEmpty(t, instID)

	status, out = h.httpJSON(t, "GET", "/v1/instances/"+instID, nil)
	require.Equal(t, http.StatusOK, status, out)
	require.Equal(t, instID, out["id"], "the created instance is readable back")

	ctx := context.Background()
	require.NotEmpty(t, pendingRowsForService(t, h, persistence.LifecycleScopeInstance, instID, "topics-ring"),
		"the failed delivery stays owed in the outbox")

	clearSubscriberFailure(t, h, "topics-ring")
	newStagedLifecycleDrain(h.deps).DrainOnce(ctx)

	require.Empty(t, pendingRowsForService(t, h, persistence.LifecycleScopeInstance, instID, "topics-ring"),
		"the drain redelivers the undelivered instance-created event and the acknowledged row leaves the outbox")
	require.Equal(t, 1, countSubscriberCalls(t, h, "topics-ring", "on_instance_created"),
		"the retry delivers OnInstanceCreated once, on the pass after the subscriber recovers")
}

// @decision: lifecycle-subscriber-at-least-once-delivery
// @concept: lifecycle-subscriber
func TestLifecycleDrain_RedeliveredEventIsNotSentAgainOnALaterTick(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	failSubscriber(t, h, "topics-ring", "on_template_registered")
	status, out := h.httpJSON(t, "POST", "/v1/templates",
		templateWithClaimProducersAndLocks("reg-drain-once-"+uuid.NewString()))
	require.Equal(t, http.StatusCreated, status, out)

	clearSubscriberFailure(t, h, "topics-ring")
	drain := newStagedLifecycleDrain(h.deps)
	drain.DrainOnce(ctx)
	drain.DrainOnce(ctx)

	require.Equal(t, 1, countSubscriberCalls(t, h, "topics-ring", "on_template_registered"),
		"the first pass deletes the row it acknowledged, so the second pass has nothing to redeliver")
}

func clearSubscriberFailure(t *testing.T, h *harness, name string) {
	t.Helper()
	cp, ok := h.producers.Get(name)
	require.True(t, ok)
	fake, ok := cp.(*storetest.Fake)
	require.True(t, ok)
	fake.ErrorFunc = nil
	fake.Reset()
}

func countSubscriberCalls(t *testing.T, h *harness, name, verb string) int {
	t.Helper()
	cp, ok := h.producers.Get(name)
	require.True(t, ok)
	fake, ok := cp.(*storetest.Fake)
	require.True(t, ok)
	n := 0
	for _, c := range fake.Calls() {
		if c.Verb == verb {
			n++
		}
	}
	return n
}

// @decision: lifecycle-fanout-after-commit
// @concept: lifecycle-subscriber
func TestTerminateInstance_RunScopeTerminalPrecedesInstanceTerminated(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	tplID := registerAndDeployBody(t, h, templateWithClaimProducersAndLocks("term-order-"+uuid.NewString()))
	status, out := h.httpJSON(t, "POST", "/v1/instances", map[string]any{
		"template":     tplID,
		"instance_key": "ck-" + uuid.NewString(),
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

	clearSubscriberFailure(t, h, "topics-ring")

	status, out = h.httpJSON(t, "POST", "/v1/instances/"+instID.String()+"/terminate", nil)
	require.Equal(t, http.StatusOK, status, out)

	cp, ok := h.producers.Get("topics-ring")
	require.True(t, ok)
	fake, ok := cp.(*storetest.Fake)
	require.True(t, ok)

	var runScopeCall, terminatedCall *storetest.FakeCall
	calls := fake.Calls()
	for i := range calls {
		switch calls[i].Verb {
		case "on_run_scope_terminal":
			runScopeCall = &calls[i]
		case "on_instance_terminated":
			terminatedCall = &calls[i]
		}
	}
	require.NotNil(t, runScopeCall,
		"terminating the instance delivers OnRunScopeTerminal for the run scope it closes")
	require.NotNil(t, terminatedCall,
		"terminating the instance delivers OnInstanceTerminated for instance "+instID.String())
	require.Less(t, runScopeCall.Sequence, terminatedCall.Sequence,
		"a service observes run-scope closure before the instance is reported terminated")

	var scope *persistence.RunScopeRow
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := h.persist.RunScopes().GetByID(ctx, rootScopeID, tx)
		scope = r
		return err
	}))
	require.NotNil(t, scope)
	require.NotNil(t, scope.ClosedAt, "the terminating transaction closes the instance's open run scopes")
}

func failSubscriber(t *testing.T, h *harness, name, verb string) {
	t.Helper()
	cp, ok := h.producers.Get(name)
	require.True(t, ok)
	fake, ok := cp.(*storetest.Fake)
	require.True(t, ok)
	fake.ErrorFunc = func(called string, _ claimproducer.ClaimID) error {
		if called == verb {
			return errors.New("simulated subscriber failure")
		}
		return nil
	}
}

func newStagedLifecycleDrain(deps AppDeps) *runtime.LifecycleReconciler {
	return runtime.NewLifecycleReconciler(runtime.LifecycleReconcilerConfig{
		Persist:        deps.Persist,
		AdvisoryLocker: deps.AdvisoryLocker,
		Subscribers:    deps.LifecycleSubs,
		Clock:          deps.Clock,
		Logger:         deps.Logger,
	})
}
