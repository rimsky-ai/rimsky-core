// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/clientiface"
)

type lifecycleFakeClient struct {
	name string

	mu             sync.Mutex
	unsubscribeErr error
	unsubscribed   []shared.UUID
}

func (c *lifecycleFakeClient) Name() string { return c.name }

func (c *lifecycleFakeClient) Subscribe(context.Context, clientiface.SubscribeRequest) error {
	return nil
}

func (c *lifecycleFakeClient) Unsubscribe(_ context.Context, id shared.UUID) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.unsubscribed = append(c.unsubscribed, id)
	return c.unsubscribeErr
}

func (c *lifecycleFakeClient) ListSubscriptions(context.Context) ([]clientiface.ListedPublisherSubscription, error) {
	return nil, nil
}

func (c *lifecycleFakeClient) unsubscribeCalls() []shared.UUID {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]shared.UUID(nil), c.unsubscribed...)
}

type lifecycleFakeRegistry struct {
	client *lifecycleFakeClient
}

func (r *lifecycleFakeRegistry) Get(name string) (clientiface.PublisherClient, bool) {
	if r.client == nil || r.client.name != name {
		return nil, false
	}
	return r.client, true
}

func (r *lifecycleFakeRegistry) All() []clientiface.PublisherClient {
	if r.client == nil {
		return nil
	}
	return []clientiface.PublisherClient{r.client}
}

func openPublisherLifecycleTables(t *testing.T) persistence.Tables {
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
	return d.Tables()
}

func seedPublisherSubscription(
	t *testing.T, tables persistence.Tables, row persistence.PublisherSubscriptionRow,
) (instanceID shared.UUID) {
	t.Helper()
	ctx := context.Background()
	templateHash := "sha256-" + uuid.NewString()
	instanceID = shared.UUID(uuid.New())
	row.InstanceID = instanceID
	if row.StartedAt.IsZero() {
		row.StartedAt = time.Now().UTC()
	}
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := tables.Templates().Insert(ctx, persistence.TemplateInsertInput{
			ID: templateHash, Spec: spec.TemplateSpec{Name: "publisher-lifecycle-fixture", Version: "1"},
			State: persistence.TemplateStateRegistered, Source: "direct",
		}, tx); err != nil {
			return err
		}
		if _, err := tables.Instances().Create(ctx, persistence.InstanceCreateInput{
			ID: instanceID, TemplateHash: templateHash,
		}, tx); err != nil {
			return err
		}
		return tables.PublisherSubscriptions().Insert(ctx, tx, row)
	}); err != nil {
		t.Fatalf("seedPublisherSubscription: %v", err)
	}
	return instanceID
}

func getPublisherSubscriptionRow(t *testing.T, tables persistence.Tables, id shared.UUID) *persistence.PublisherSubscriptionRow {
	t.Helper()
	var row *persistence.PublisherSubscriptionRow
	if err := tables.Transaction(context.Background(), func(ctx context.Context, tx persistence.Tx) error {
		r, err := tables.PublisherSubscriptions().Get(ctx, tx, id)
		row = r
		return err
	}); err != nil {
		t.Fatalf("getPublisherSubscriptionRow: %v", err)
	}
	return row
}

// @concept: publisher-subscription
func TestRecoverUnknownPublisherFailures_FlipsFailedToMountingOncePublisherRegisters(t *testing.T) {
	tables := openPublisherLifecycleTables(t)
	subID := shared.UUID(uuid.New())
	seedPublisherSubscription(t, tables, persistence.PublisherSubscriptionRow{
		ID:             subID,
		PublisherName:  "late-pub",
		Kind:           "test-kind",
		ResolvedConfig: []byte(`{}`),
		State:          persistence.PublisherSubscriptionStateFailed,
		FailureReason:  unknownPublisherReason("late-pub"),
	})

	client := &lifecycleFakeClient{name: "late-pub"}
	deps := PublisherLifecycleDeps{
		Persist:    tables,
		Publishers: &lifecycleFakeRegistry{client: client},
		Clock:      shared.SystemClock{},
		Logger:     shared.SilentLogger{},
	}

	recovered, err := recoverUnknownPublisherFailures(context.Background(), deps)
	if err != nil {
		t.Fatalf("recoverUnknownPublisherFailures: %v", err)
	}
	if len(recovered) != 1 || recovered[0].ID != subID {
		t.Fatalf("recovered = %+v, want exactly [%v]", recovered, subID)
	}
	if recovered[0].State != persistence.PublisherSubscriptionStateMounting {
		t.Fatalf("recovered row state = %q, want %q", recovered[0].State, persistence.PublisherSubscriptionStateMounting)
	}
	if recovered[0].FailureReason != "" {
		t.Fatalf("recovered row failure_reason = %q, want empty", recovered[0].FailureReason)
	}

	row := getPublisherSubscriptionRow(t, tables, subID)
	if row == nil || row.State != persistence.PublisherSubscriptionStateMounting {
		t.Fatalf("persisted row after recovery = %+v, want state=mounting", row)
	}
}

// @concept: publisher-subscription
func TestRecoverUnknownPublisherFailures_LeavesOtherFailureReasonsAlone(t *testing.T) {
	tables := openPublisherLifecycleTables(t)
	subID := shared.UUID(uuid.New())
	seedPublisherSubscription(t, tables, persistence.PublisherSubscriptionRow{
		ID:             subID,
		PublisherName:  "bad-config-pub",
		Kind:           "test-kind",
		ResolvedConfig: []byte(`{}`),
		State:          persistence.PublisherSubscriptionStateFailed,
		FailureReason:  "publisher config resolution failed: boom",
	})

	client := &lifecycleFakeClient{name: "bad-config-pub"}
	deps := PublisherLifecycleDeps{
		Persist:    tables,
		Publishers: &lifecycleFakeRegistry{client: client},
		Clock:      shared.SystemClock{},
		Logger:     shared.SilentLogger{},
	}

	recovered, err := recoverUnknownPublisherFailures(context.Background(), deps)
	if err != nil {
		t.Fatalf("recoverUnknownPublisherFailures: %v", err)
	}
	if len(recovered) != 0 {
		t.Fatalf("recovered = %+v, want none — a non-unknown-publisher failure must never auto-recover", recovered)
	}
	row := getPublisherSubscriptionRow(t, tables, subID)
	if row == nil || row.State != persistence.PublisherSubscriptionStateFailed {
		t.Fatalf("persisted row = %+v, want state still failed", row)
	}
}

// @concept: publisher-subscription
func TestStopPublisherSubscriptionsForInstance_ActiveSubscriptionUnsubscribesAndStops(t *testing.T) {
	tables := openPublisherLifecycleTables(t)
	subID := shared.UUID(uuid.New())
	instanceID := seedPublisherSubscription(t, tables, persistence.PublisherSubscriptionRow{
		ID:             subID,
		PublisherName:  "active-pub",
		Kind:           "test-kind",
		ResolvedConfig: []byte(`{}`),
		State:          persistence.PublisherSubscriptionStateActive,
	})

	client := &lifecycleFakeClient{name: "active-pub"}
	deps := PublisherLifecycleDeps{
		Persist:    tables,
		Publishers: &lifecycleFakeRegistry{client: client},
		Clock:      shared.SystemClock{},
		Logger:     shared.SilentLogger{},
	}

	if err := StopPublisherSubscriptionsForInstance(context.Background(), deps, instanceID); err != nil {
		t.Fatalf("StopPublisherSubscriptionsForInstance: %v", err)
	}

	calls := client.unsubscribeCalls()
	if len(calls) != 1 || calls[0] != subID {
		t.Fatalf("Unsubscribe calls = %v, want exactly [%v]", calls, subID)
	}
	row := getPublisherSubscriptionRow(t, tables, subID)
	if row == nil || row.State != persistence.PublisherSubscriptionStateStopped {
		t.Fatalf("row after stop = %+v, want state=stopped", row)
	}
}

// @concept: publisher-subscription
func TestStopPublisherSubscriptionsForInstance_MountingSubscriptionStopsEvenWhenUnsubscribeFails(t *testing.T) {
	tables := openPublisherLifecycleTables(t)
	subID := shared.UUID(uuid.New())
	instanceID := seedPublisherSubscription(t, tables, persistence.PublisherSubscriptionRow{
		ID:             subID,
		PublisherName:  "mounting-pub",
		Kind:           "test-kind",
		ResolvedConfig: []byte(`{}`),
		State:          persistence.PublisherSubscriptionStateMounting,
	})

	client := &lifecycleFakeClient{name: "mounting-pub", unsubscribeErr: errors.New("rpc unreachable")}
	deps := PublisherLifecycleDeps{
		Persist:    tables,
		Publishers: &lifecycleFakeRegistry{client: client},
		Clock:      shared.SystemClock{},
		Logger:     shared.SilentLogger{},
	}

	if err := StopPublisherSubscriptionsForInstance(context.Background(), deps, instanceID); err != nil {
		t.Fatalf("StopPublisherSubscriptionsForInstance: %v", err)
	}

	row := getPublisherSubscriptionRow(t, tables, subID)
	if row == nil || row.State != persistence.PublisherSubscriptionStateStopped {
		t.Fatalf("row after stop with failing RPC = %+v, want state=stopped "+
			"(a mounting subscription must still be marked stopped even when the peer Unsubscribe RPC fails, "+
			"since the peer may never have completed its own Subscribe)", row)
	}
}

// @concept: publisher-subscription
func TestStopPublisherSubscriptionsForInstance_UnknownPublisherMarksStoppedWithoutRPC(t *testing.T) {
	tables := openPublisherLifecycleTables(t)
	subID := shared.UUID(uuid.New())
	instanceID := seedPublisherSubscription(t, tables, persistence.PublisherSubscriptionRow{
		ID:             subID,
		PublisherName:  "not-registered-pub",
		Kind:           "test-kind",
		ResolvedConfig: []byte(`{}`),
		State:          persistence.PublisherSubscriptionStateActive,
	})

	deps := PublisherLifecycleDeps{
		Persist:    tables,
		Publishers: &lifecycleFakeRegistry{},
		Clock:      shared.SystemClock{},
		Logger:     shared.SilentLogger{},
	}

	if err := StopPublisherSubscriptionsForInstance(context.Background(), deps, instanceID); err != nil {
		t.Fatalf("StopPublisherSubscriptionsForInstance: %v", err)
	}

	row := getPublisherSubscriptionRow(t, tables, subID)
	if row == nil || row.State != persistence.PublisherSubscriptionStateStopped {
		t.Fatalf("row for an unregistered publisher = %+v, want state=stopped even with no live client to call", row)
	}
}

// @concept: publisher-subscription
func TestUnsubscribeIfRowStopped_CallsUnsubscribeWhenRowIsStopped(t *testing.T) {
	tables := openPublisherLifecycleTables(t)
	subID := shared.UUID(uuid.New())
	seedPublisherSubscription(t, tables, persistence.PublisherSubscriptionRow{
		ID:             subID,
		PublisherName:  "orphan-pub",
		Kind:           "test-kind",
		ResolvedConfig: []byte(`{}`),
		State:          persistence.PublisherSubscriptionStateStopped,
	})

	client := &lifecycleFakeClient{name: "orphan-pub"}
	deps := PublisherLifecycleDeps{
		Persist: tables,
		Clock:   shared.SystemClock{},
		Logger:  shared.SilentLogger{},
	}

	unsubscribeIfRowStopped(context.Background(), deps, client, subID)

	calls := client.unsubscribeCalls()
	if len(calls) != 1 || calls[0] != subID {
		t.Fatalf("Unsubscribe calls = %v, want exactly [%v] — a stale live subscription whose row already "+
			"settled to stopped must be compensated with an Unsubscribe call", calls, subID)
	}
}

// @concept: publisher-subscription
func TestUnsubscribeIfRowStopped_NoOpWhenRowStillActive(t *testing.T) {
	tables := openPublisherLifecycleTables(t)
	subID := shared.UUID(uuid.New())
	seedPublisherSubscription(t, tables, persistence.PublisherSubscriptionRow{
		ID:             subID,
		PublisherName:  "live-pub",
		Kind:           "test-kind",
		ResolvedConfig: []byte(`{}`),
		State:          persistence.PublisherSubscriptionStateActive,
	})

	client := &lifecycleFakeClient{name: "live-pub"}
	deps := PublisherLifecycleDeps{
		Persist: tables,
		Clock:   shared.SystemClock{},
		Logger:  shared.SilentLogger{},
	}

	unsubscribeIfRowStopped(context.Background(), deps, client, subID)

	if calls := client.unsubscribeCalls(); len(calls) != 0 {
		t.Fatalf("Unsubscribe calls = %v, want none — a row still active must not be compensated", calls)
	}
}
