// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package controlapi

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks/storetest"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
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

	var lcRow *persistence.LifecycleIdempotencyRow
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := h.persist.LifecycleIdempotency().Get(ctx, "topics-ring",
			persistence.LifecycleIdempotencyScopeTemplate, hash, tx)
		lcRow = r
		return err
	}))
	require.Nil(t, lcRow,
		"the failed delivery leaves the ledger unadvanced, so the next fan-out for this scope retries it")

	clearSubscriberFailure(t, h, "topics-ring")
	NewLifecycleReconciler(h.deps, time.Hour).tick(ctx)

	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := h.persist.LifecycleIdempotency().Get(ctx, "topics-ring",
			persistence.LifecycleIdempotencyScopeTemplate, hash, tx)
		lcRow = r
		return err
	}))
	require.NotNil(t, lcRow,
		"the reconciler tick redelivers the undelivered template-scope event and advances the ledger row")
	require.Equal(t, persistence.LifecycleIdempotencyStateRegistered, lcRow.State)
	require.Equal(t, 1, countSubscriberCalls(t, h, "topics-ring", "on_template_registered"),
		"the retry delivers OnTemplateRegistered once, on the tick after the subscriber recovers")
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
	var lcRow *persistence.LifecycleIdempotencyRow
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := h.persist.LifecycleIdempotency().Get(ctx, "topics-ring",
			persistence.LifecycleIdempotencyScopeInstance, instID, tx)
		lcRow = r
		return err
	}))
	require.Nil(t, lcRow, "the failed delivery leaves no instance-scope ledger row")

	clearSubscriberFailure(t, h, "topics-ring")
	NewLifecycleReconciler(h.deps, time.Hour).tick(ctx)

	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := h.persist.LifecycleIdempotency().Get(ctx, "topics-ring",
			persistence.LifecycleIdempotencyScopeInstance, instID, tx)
		lcRow = r
		return err
	}))
	require.NotNil(t, lcRow,
		"the reconciler tick redelivers the undelivered instance-created event and writes the ledger row")
	require.Equal(t, persistence.LifecycleIdempotencyStateCreated, lcRow.State)
	require.Equal(t, 1, countSubscriberCalls(t, h, "topics-ring", "on_instance_created"),
		"the retry delivers OnInstanceCreated once, on the tick after the subscriber recovers")
}

// @decision: lifecycle-subscriber-at-least-once-delivery
// @concept: lifecycle-subscriber
func TestLifecycleReconciler_RedeliveredEventIsNotSentAgainOnALaterTick(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	failSubscriber(t, h, "topics-ring", "on_template_registered")
	status, out := h.httpJSON(t, "POST", "/v1/templates",
		templateWithClaimProducersAndLocks("reg-drain-once-"+uuid.NewString()))
	require.Equal(t, http.StatusCreated, status, out)

	clearSubscriberFailure(t, h, "topics-ring")
	reconciler := NewLifecycleReconciler(h.deps, time.Hour)
	reconciler.tick(ctx)
	reconciler.tick(ctx)

	require.Equal(t, 1, countSubscriberCalls(t, h, "topics-ring", "on_template_registered"),
		"the ledger row the first tick wrote stops the second tick from redelivering")
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
func TestLifecycleReconciler_RunScopeTerminalPrecedesInstanceTerminated(t *testing.T) {
	t.Parallel()
	f := newTerminatorFixture(t)

	_, instanceID := seedTerminatedInstance(t, f, "alpha", true)
	term := NewLifecycleReconciler(f.deps, time.Millisecond)
	term.tick(context.Background())

	var runScopeCall, terminatedCall *storetest.FakeCall
	calls := f.alpha.Calls()
	for i := range calls {
		switch calls[i].Verb {
		case "on_run_scope_terminal":
			runScopeCall = &calls[i]
		case "on_instance_terminated":
			terminatedCall = &calls[i]
		}
	}
	require.NotNil(t, runScopeCall,
		"one poll tick delivers OnRunScopeTerminal for the terminated instance's root run scope")
	require.NotNil(t, terminatedCall,
		"one poll tick delivers OnInstanceTerminated for instance "+instanceID.String())
	require.Less(t, runScopeCall.Sequence, terminatedCall.Sequence,
		"a peer observes run-scope closure before the instance is reported terminated")
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
