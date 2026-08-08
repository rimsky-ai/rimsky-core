// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package runtime

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	_ "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/clientiface"
)

type reconcilerFakePublisherClient struct {
	name string

	mu         sync.Mutex
	live       []clientiface.ListedPublisherSubscription
	subs       []shared.UUID
	listCalled chan struct{}
}

func (c *reconcilerFakePublisherClient) Name() string { return c.name }

func (*reconcilerFakePublisherClient) SupportedKinds(context.Context) ([]string, error) {
	return nil, nil
}

func (c *reconcilerFakePublisherClient) Subscribe(_ context.Context, req clientiface.SubscribeRequest) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.subs = append(c.subs, req.PublisherSubscriptionID)
	return nil
}

func (c *reconcilerFakePublisherClient) Unsubscribe(context.Context, shared.UUID) error {
	return nil
}

func (c *reconcilerFakePublisherClient) ListSubscriptions(context.Context) ([]clientiface.ListedPublisherSubscription, error) {
	c.mu.Lock()
	out := append([]clientiface.ListedPublisherSubscription(nil), c.live...)
	c.mu.Unlock()
	c.listCalled <- struct{}{}
	return out, nil
}

func (c *reconcilerFakePublisherClient) setLive(live []clientiface.ListedPublisherSubscription) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.live = live
}

func (c *reconcilerFakePublisherClient) subscribeCalls() []shared.UUID {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]shared.UUID(nil), c.subs...)
}

type reconcilerFakeRegistry struct {
	client *reconcilerFakePublisherClient
}

func (r *reconcilerFakeRegistry) Get(name string) (clientiface.PublisherClient, bool) {
	if r.client == nil || r.client.name != name {
		return nil, false
	}
	return r.client, true
}

func (r *reconcilerFakeRegistry) All() []clientiface.PublisherClient {
	if r.client == nil {
		return nil
	}
	return []clientiface.PublisherClient{r.client}
}

func openReconcilerFixtureTables(t *testing.T) (persistence.Tables, shared.UUID, shared.UUID) {
	t.Helper()
	ctx := context.Background()
	d, err := persistence.Open(ctx, persistence.Config{
		Driver: "sqlite",
		SQLite: &persistence.SQLiteConfig{Path: filepath.Join(t.TempDir(), "state.db")},
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := d.Migrate(ctx, shared.SilentLogger{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	tables := d.Tables()

	templateHash := "sha256-" + uuid.NewString()
	instanceID := shared.UUID(uuid.New())
	subID := shared.UUID(uuid.New())

	tmpl := spec.TemplateSpec{Name: "publisher-reconciler-fixture", Version: "1"}

	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := tables.Templates().Insert(ctx, persistence.TemplateInsertInput{
			ID: templateHash, Spec: tmpl, State: persistence.TemplateStateRegistered, Source: "direct",
		}, tx); err != nil {
			return err
		}
		if _, err := tables.Instances().Create(ctx, persistence.InstanceCreateInput{TargetRoutingIdentity: "test-agent",
			ID: instanceID, TemplateHash: templateHash,
		}, tx); err != nil {
			return err
		}
		return tables.PublisherSubscriptions().Insert(ctx, persistence.PublisherSubscriptionRow{
			ID:             subID,
			InstanceID:     instanceID,
			PublisherName:  "test-pub",
			Kind:           "test-kind",
			ResolvedConfig: []byte(`{}`),
			State:          persistence.PublisherSubscriptionStateActive,
			StartedAt:      time.Now().UTC(),
		}, tx)
	}); err != nil {
		t.Fatalf("seed fixture: %v", err)
	}

	return tables, instanceID, subID
}

// @concept: publisher-subscription
func TestRunPublisherSubscriptionReconcilerLoop_TickRedrivesActiveSubscription(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tables, instanceID, subID := openReconcilerFixtureTables(t)

	client := &reconcilerFakePublisherClient{
		name:       "test-pub",
		listCalled: make(chan struct{}),
		live: []clientiface.ListedPublisherSubscription{
			{PublisherSubscriptionID: subID, InstanceID: instanceID, Kind: "test-kind"},
		},
	}
	deps := PublisherLifecycleDeps{
		Persist:    tables,
		Publishers: &reconcilerFakeRegistry{client: client},
		Clock:      shared.SystemClock{},
		Logger:     shared.SilentLogger{},
	}

	tick := make(chan time.Time)
	done := make(chan struct{})
	go func() {
		runPublisherSubscriptionReconcilerLoop(ctx, deps, tick)
		close(done)
	}()

	<-client.listCalled

	client.setLive(nil)

	tick <- time.Now()

	<-client.listCalled

	for len(client.subscribeCalls()) == 0 {
		time.Sleep(2 * time.Millisecond)
	}
	calls := client.subscribeCalls()
	if len(calls) != 1 || calls[0] != subID {
		t.Fatalf("tick-driven reconcile: Subscribe calls = %v, want exactly [%v]", calls, subID)
	}

	cancel()
	<-done
}
