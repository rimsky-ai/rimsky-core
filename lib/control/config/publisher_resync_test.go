// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package config

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
)

// fakePublisherClient records the Subscribe / Unsubscribe / ListSubscriptions
// calls the resync sweeper makes, so the test can assert reconciliation
// fired against a live (drifted) publisher peer.
type fakePublisherClient struct {
	name string
	// live is the publisher's view of its subscriptions — deliberately
	// drifted from rimsky's persisted state so resync has work to do.
	live []runtime.ListedPublisherSubscription

	mu          sync.Mutex
	subscribed  []shared.UUID
	unsubbed    []shared.UUID
	listedCount int
}

func (c *fakePublisherClient) Name() string { return c.name }

func (c *fakePublisherClient) Subscribe(_ context.Context, req runtime.SubscribeRequest) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.subscribed = append(c.subscribed, req.PublisherSubscriptionID)
	return nil
}

func (c *fakePublisherClient) Unsubscribe(_ context.Context, id shared.UUID) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.unsubbed = append(c.unsubbed, id)
	return nil
}

func (c *fakePublisherClient) ListSubscriptions(_ context.Context) ([]runtime.ListedPublisherSubscription, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.listedCount++
	return c.live, nil
}

func (c *fakePublisherClient) sawSubscribe(id shared.UUID) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, s := range c.subscribed {
		if s == id {
			return true
		}
	}
	return false
}

func (c *fakePublisherClient) sawUnsubscribe(id shared.UUID) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, u := range c.unsubbed {
		if u == id {
			return true
		}
	}
	return false
}

// fakePublisherRegistry is a runtime.PublisherRegistry over a single fake
// client, so the resync sweeper's All()/Get() fan-out reaches it.
type fakePublisherRegistry struct{ client *fakePublisherClient }

func (r *fakePublisherRegistry) Get(name string) (runtime.PublisherClient, bool) {
	if name == r.client.name {
		return r.client, true
	}
	return nil, false
}

func (r *fakePublisherRegistry) All() []runtime.PublisherClient {
	return []runtime.PublisherClient{r.client}
}

// TestPublisherResyncOnStartup proves that control-api startup reconciles
// publisher subscriptions against the live publisher peer (F8): a sub rimsky
// persisted as active but the publisher no longer reports is re-issued
// (Subscribe), and a sub the publisher reports but rimsky no longer tracks
// is torn down (Unsubscribe).
//
// The test drives the real StartControlAPI against a real (SQLite) store and
// injects a fake publisher registry via the resync seam, so it asserts both
// that StartControlAPI invokes resync AND that the reconciliation does the
// right thing against a drifted peer.
//
// RED today: StartControlAPI never calls resync, so the seam override is
// never invoked and neither Subscribe nor Unsubscribe is observed.
func TestPublisherResyncOnStartup(t *testing.T) {
	ctx := context.Background()
	db := openMigratedSQLite(t)
	store := db.Tables()

	const publisherName = "pub-a"

	// Rimsky-expected, publisher-missing → resync must re-Subscribe.
	droppedSubID := shared.UUID(uuid.New())
	instanceID := uuid.New()
	// Publisher-reported, rimsky-unknown → resync must Unsubscribe.
	orphanSubID := shared.UUID(uuid.New())
	orphanInstanceID := shared.UUID(uuid.New())

	// Seed a live instance (template + run scope + instance) so the
	// publisher-subscription FK resolves, then the active subscription
	// rimsky believes is live but the publisher has lost (e.g. publisher
	// restarted with empty state).
	templateHash := "sha256-" + uuid.NewString()
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
		if _, err := store.Instances().Create(ctx, persistence.InstanceCreateInput{
			ID:             instanceID,
			TemplateHash:   templateHash,
			MainRunScopeID: mainRunScopeID,
		}, tx); err != nil {
			return err
		}
		return store.PublisherSubscriptions().Insert(ctx, tx, persistence.PublisherSubscriptionRow{
			ID:             droppedSubID,
			InstanceID:     instanceID,
			PublisherName:  publisherName,
			Kind:           "object_store",
			ResolvedConfig: json.RawMessage(`{"bucket":"b"}`),
			TargetNode:     "ingest",
			MessageKind:    "invalidate",
			State:          persistence.PublisherSubscriptionStateActive,
			StartedAt:      time.Now().UTC(),
		})
	}); err != nil {
		t.Fatalf("seed subscription: %v", err)
	}

	fake := &fakePublisherClient{
		name: publisherName,
		// The publisher reports ONLY the orphan — not the dropped sub.
		live: []runtime.ListedPublisherSubscription{
			{
				PublisherSubscriptionID: orphanSubID,
				InstanceID:              orphanInstanceID,
				Kind:                    "object_store",
				TargetNode:              "ingest",
				MessageKind:             "invalidate",
			},
		},
	}

	// Inject the fake registry via the startup-resync seam and record that
	// StartControlAPI fired it. The override runs the REAL reconciliation
	// against the real seeded store, swapping only the publisher registry.
	var resyncCalled bool
	var mu sync.Mutex
	done := make(chan struct{})
	orig := resyncPublishersAtStartup
	t.Cleanup(func() { resyncPublishersAtStartup = orig })
	resyncPublishersAtStartup = func(ctx context.Context, deps runtime.PublisherLifecycleDeps) error {
		mu.Lock()
		resyncCalled = true
		mu.Unlock()
		deps.Publishers = &fakePublisherRegistry{client: fake}
		err := runtime.ResyncPublisherSubscriptions(ctx, deps)
		close(done)
		return err
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

	// Resync may run in a goroutine; wait for it (bounded) before asserting.
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		// Fall through to the assertions below, which fail clearly.
	}

	mu.Lock()
	called := resyncCalled
	mu.Unlock()
	if !called {
		t.Fatalf("StartControlAPI did not invoke publisher resync at startup")
	}
	if !fake.sawSubscribe(droppedSubID) {
		t.Errorf("dropped subscription %s was not re-issued (no Subscribe observed)", droppedSubID)
	}
	if !fake.sawUnsubscribe(orphanSubID) {
		t.Errorf("orphan subscription %s was not torn down (no Unsubscribe observed)", orphanSubID)
	}
}
