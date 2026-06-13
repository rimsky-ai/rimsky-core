// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Tests for the publisher-subscription mounting lifecycle
// (concept:publisher-subscription): instance-create persists desired-state
// rows in `mounting` with no inline Subscribe RPC, the reconciliation
// worker drives the handshake with no attempt cap, and StartControlAPI
// wires the worker at startup.

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
)

// seedInstanceForSubscriptions creates template + run scope + instance so
// publisher-subscription rows can FK against the instance.
//
// @source: lib/control/config/publisher_resync_test.go:TestPublisherResyncOnStartup
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
		if err := store.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID:         mainRunScopeID,
			GraphName:  spec.MainGraphName,
			InstanceID: instanceID,
		}); err != nil {
			return err
		}
		_, err := store.Instances().Create(ctx, persistence.InstanceCreateInput{
			ID:             instanceID,
			TemplateHash:   templateHash,
			MainRunScopeID: mainRunScopeID,
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
		row, err = store.PublisherSubscriptions().Get(ctx, tx, id)
		return err
	}); err != nil {
		t.Fatalf("getSubscriptionRow: %v", err)
	}
	if row == nil {
		t.Fatalf("getSubscriptionRow: row %s not found", id)
	}
	return row
}

// flakyPublisherClient fails the first failFirstN Subscribe calls, then
// succeeds — so a test can prove the reconciler keeps retrying past any
// bounded budget instead of flipping the row to failed.
type flakyPublisherClient struct {
	fakePublisherClient
	mu2        sync.Mutex
	failFirstN int
	attempts   int
}

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

// TestStartPublisherSubscriptions_InsertsMountingNoInlineRPC proves the
// load-bearing instance-create property: rows are persisted in `mounting`
// and NO Subscribe RPC happens inline — instance creation cannot block
// on, or fail because of, publisher reachability.
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
		return runtime.StartPublisherSubscriptionsForInstance(ctx, deps, tx, instanceID, nil, []spec.PublisherSpec{
			{Name: "pub-a", Kind: "object_store", Config: json.RawMessage(`{"bucket":"b"}`), TargetNode: "ingest"},
		})
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

// TestStartPublisherSubscriptions_UnknownPublisherFailsWithReason — an
// unregistered publisher name is the one non-retryable failure class in
// the create path: the row flips to failed and carries a reason the
// instance surface can show.
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
		return runtime.StartPublisherSubscriptionsForInstance(ctx, deps, tx, instanceID, nil, []spec.PublisherSpec{
			{Name: "no-such-publisher", Kind: "object_store", Config: json.RawMessage(`{}`), TargetNode: "ingest"},
		})
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

// TestPublisherSubscriptionReconciler_RetriesPastBudgetThenActivates — a
// slow / contended publisher keeps the row in observable `mounting`
// (never `failed`) while the reconciler retries without an attempt cap,
// and the row flips to `active` once the publisher recovers. The fake
// fails the first 4 attempts — past the old 3-attempt budget — so a
// reintroduced bounded budget fails this test.
func TestPublisherSubscriptionReconciler_RetriesPastBudgetThenActivates(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db := openMigratedSQLite(t)
	store := db.Tables()
	instanceID := seedInstanceForSubscriptions(t, store)

	subID := shared.UUID(uuid.New())
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return store.PublisherSubscriptions().Insert(ctx, tx, persistence.PublisherSubscriptionRow{
			ID:             subID,
			InstanceID:     instanceID,
			PublisherName:  "pub-a",
			Kind:           "object_store",
			ResolvedConfig: json.RawMessage(`{"bucket":"b"}`),
			TargetNode:     "ingest",
			State:          persistence.PublisherSubscriptionStateMounting,
			StartedAt:      time.Now().UTC(),
		})
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

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		row := getSubscriptionRow(t, store, subID)
		switch row.State {
		case persistence.PublisherSubscriptionStateActive:
			if got := flaky.attemptCount(); got <= 4 {
				t.Fatalf("row active after %d attempts; the first 4 fail, so activation requires ≥5", got)
			}
			return
		case persistence.PublisherSubscriptionStateFailed:
			t.Fatalf("retryable Subscribe failure flipped the row to failed (reason %q) — failed is reserved for non-retryable errors", row.FailureReason)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("subscription never reached active; attempts=%d state=%q",
		flaky.attemptCount(), getSubscriptionRow(t, store, subID).State)
}

// TestStartControlAPI_StartsSubscriptionReconciler asserts the wiring:
// StartControlAPI launches the reconciliation worker at startup with the
// publisher registry + persistence handles, bound to the shutdown
// context.
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

	select {
	case deps := <-started:
		if deps.Persist == nil {
			t.Fatalf("reconciler started without a persistence handle")
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("StartControlAPI did not start the publisher-subscription reconciler")
	}
}
