// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package config

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
	"github.com/rimsky-ai/rimsky-core/test/support/awaited"
)

func seedInstanceForSubscriptions(t *testing.T, store persistence.Tables) shared.UUID {
	t.Helper()
	ctx := context.Background()
	templateHash := "sha256-" + uuid.NewString()
	instanceID := uuid.New()
	mainRunScopeID := uuid.New()
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := store.Templates().Insert(ctx, persistence.TemplateInsertInput{
			ID:     templateHash,
			Spec:   spec.TemplateSpec{Name: "fixture", Version: "1.0.0"},
			State:  persistence.TemplateStateRegistered,
			Source: "direct",
		}, tx); err != nil {
			return err
		}
		if err := store.RunScopes().Create(ctx, persistence.RunScopeRow{
			ID:         mainRunScopeID,
			GraphName:  spec.MainGraphName,
			InstanceID: instanceID,
		}, tx); err != nil {
			return err
		}
		_, err := store.Instances().Create(ctx, persistence.InstanceCreateInput{
			ID:                    instanceID,
			TemplateHash:          templateHash,
			TargetRoutingIdentity: "test-subscription-agent",
		}, tx)
		return err
	}); err != nil {
		t.Fatalf("seedInstanceForSubscriptions: %v", err)
	}
	return shared.UUID(instanceID)
}

func getSubscriptionRow(t *testing.T, store persistence.Tables, id shared.UUID) *persistence.PublisherSubscriptionRow {
	t.Helper()
	var row *persistence.PublisherSubscriptionRow
	if err := store.Transaction(context.Background(), func(ctx context.Context, tx persistence.Tx) error {
		var err error
		row, err = store.PublisherSubscriptions().Get(ctx, id, tx)
		return err
	}); err != nil {
		t.Fatalf("getSubscriptionRow: %v", err)
	}
	if row == nil {
		t.Fatalf("getSubscriptionRow: row %s not found", id)
	}
	return row
}

type flakyPublisherClient struct {
	fakePublisherClient
	mu2        sync.Mutex
	failFirstN int
	attempts   int
}

func (*flakyPublisherClient) SupportedKinds(context.Context) ([]string, error) { return nil, nil }

func (c *flakyPublisherClient) Subscribe(ctx context.Context, req runtime.SubscribeRequest) error {
	c.mu2.Lock()
	c.attempts++
	n := c.attempts
	c.mu2.Unlock()
	if n <= c.failFirstN {
		return errors.New("publisher busy: simulated contention")
	}
	return c.fakePublisherClient.Subscribe(ctx, req)
}

func (c *flakyPublisherClient) attemptCount() int {
	c.mu2.Lock()
	defer c.mu2.Unlock()
	return c.attempts
}

type flakyPublisherRegistry struct{ client *flakyPublisherClient }

func (r *flakyPublisherRegistry) Get(name string) (runtime.PublisherClient, bool) {
	if name == r.client.name {
		return r.client, true
	}
	return nil, false
}

func (r *flakyPublisherRegistry) All() []runtime.PublisherClient {
	return []runtime.PublisherClient{r.client}
}

func TestStartPublisherSubscriptions_InsertsMountingNoInlineRPC(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openMigratedSQLite(t)
	store := db.Tables()
	instanceID := seedInstanceForSubscriptions(t, store)

	fake := &fakePublisherClient{name: "pub-a"}
	deps := runtime.PublisherLifecycleDeps{
		Persist:    store,
		Publishers: &fakePublisherRegistry{client: fake},
		Clock:      shared.SystemClock{},
		Logger:     shared.SilentLogger{},
	}
	err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return runtime.StartPublisherSubscriptionsForInstance(ctx, deps, instanceID, nil, []spec.PublisherSpec{
			{Name: "pub-a", Kind: "object_store", Config: spec.RawJSON(`{"bucket":"b"}`), MessageType: "fixture/ping"},
		}, tx)
	})
	if err != nil {
		t.Fatalf("StartPublisherSubscriptionsForInstance: %v", err)
	}

	fake.mu.Lock()
	subscribeCalls := len(fake.subscribed)
	fake.mu.Unlock()
	if subscribeCalls != 0 {
		t.Fatalf("instance-create performed %d inline Subscribe RPC(s); must be 0", subscribeCalls)
	}
	rows, err := store.PublisherSubscriptions().ListByInstance(ctx, instanceID)
	if err != nil {
		t.Fatalf("ListByInstance: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 subscription row, got %d", len(rows))
	}
	if rows[0].State != persistence.PublisherSubscriptionStateMounting {
		t.Fatalf("expected state=mounting on the created row, got %q", rows[0].State)
	}
}

func TestStartPublisherSubscriptions_UnknownPublisherFailsWithReason(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openMigratedSQLite(t)
	store := db.Tables()
	instanceID := seedInstanceForSubscriptions(t, store)

	fake := &fakePublisherClient{name: "pub-a"}
	deps := runtime.PublisherLifecycleDeps{
		Persist:    store,
		Publishers: &fakePublisherRegistry{client: fake},
		Clock:      shared.SystemClock{},
		Logger:     shared.SilentLogger{},
	}
	err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return runtime.StartPublisherSubscriptionsForInstance(ctx, deps, instanceID, nil, []spec.PublisherSpec{
			{Name: "no-such-publisher", Kind: "object_store", Config: spec.RawJSON(`{}`), MessageType: "fixture/ping"},
		}, tx)
	})
	if err != nil {
		t.Fatalf("StartPublisherSubscriptionsForInstance: %v", err)
	}
	rows, err := store.PublisherSubscriptions().ListByInstance(ctx, instanceID)
	if err != nil {
		t.Fatalf("ListByInstance: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 subscription row, got %d", len(rows))
	}
	if rows[0].State != persistence.PublisherSubscriptionStateFailed {
		t.Fatalf("expected state=failed for unknown publisher, got %q", rows[0].State)
	}
	if !strings.Contains(rows[0].FailureReason, "no-such-publisher") {
		t.Fatalf("failure_reason should name the unregistered publisher, got %q", rows[0].FailureReason)
	}
}

func TestPublisherSubscriptionReconciler_RetriesPastBudgetThenActivates(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db := openMigratedSQLite(t)
	store := db.Tables()
	instanceID := seedInstanceForSubscriptions(t, store)

	subID := shared.UUID(uuid.New())
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return store.PublisherSubscriptions().Insert(ctx, persistence.PublisherSubscriptionRow{
			ID:             subID,
			InstanceID:     instanceID,
			PublisherName:  "pub-a",
			Kind:           "object_store",
			ResolvedConfig: json.RawMessage(`{"bucket":"b"}`),

			State:     persistence.PublisherSubscriptionStateMounting,
			StartedAt: time.Now().UTC(),
		}, tx)
	}); err != nil {
		t.Fatalf("seed mounting row: %v", err)
	}

	flaky := &flakyPublisherClient{
		fakePublisherClient: fakePublisherClient{name: "pub-a"},
		failFirstN:          4,
	}
	deps := runtime.PublisherLifecycleDeps{
		Persist:    store,
		Publishers: &flakyPublisherRegistry{client: flaky},
		Clock:      shared.SystemClock{},
		Logger:     shared.SilentLogger{},
	}
	go runtime.RunPublisherSubscriptionReconciler(ctx, deps, 10*time.Millisecond)

	awaited.Until(t, "the reconciler to retry past the first four failures and activate the subscription", func() bool {
		row := getSubscriptionRow(t, store, subID)
		if row.State == persistence.PublisherSubscriptionStateFailed {
			t.Fatalf("retryable Subscribe failure flipped the row to failed (reason %q) — failed is reserved for non-retryable errors", row.FailureReason)
		}
		return row.State == persistence.PublisherSubscriptionStateActive
	})
	if got := flaky.attemptCount(); got <= 4 {
		t.Fatalf("row active after %d attempts; the first 4 fail, so activation requires ≥5", got)
	}
}

func TestStartControlAPI_StartsSubscriptionReconciler(t *testing.T) {
	db := openMigratedSQLite(t)

	started := make(chan runtime.PublisherLifecycleDeps, 1)
	orig := runPublisherSubscriptionReconciler
	t.Cleanup(func() { runPublisherSubscriptionReconciler = orig })
	runPublisherSubscriptionReconciler = func(ctx context.Context, deps runtime.PublisherLifecycleDeps, interval time.Duration) {
		started <- deps
		runtime.RunPublisherSubscriptionReconciler(ctx, deps, interval)
	}

	h, err := StartControlAPI(ControlAPIConfig{
		Driver: db,
		Clock:  shared.SystemClock{},
		Logger: shared.SilentLogger{},
		Host:   "127.0.0.1",
		Port:   0,
	})
	if err != nil {
		t.Fatalf("StartControlAPI: %v", err)
	}
	t.Cleanup(func() {
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = h.Shutdown(shutCtx)
	})

	if deps := <-started; deps.Persist == nil {
		t.Fatalf("reconciler started without a persistence handle")
	}
}
