// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Cross-driver conformance for the publisher-subscription desired-state
// lifecycle (concept:publisher-subscription): rows are born `mounting`,
// the reconciler's guarded CompareAndSetState flips them, and a settled
// row is never overwritten by a late flip.

package conformance

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

func testPublisherSubscriptionLifecycle(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fx := seedFixtureSet(ctx, t, d)
	store := d.Tables()
	subs := store.PublisherSubscriptions()

	// Get requires an explicit tx (the no-nil-tx contract).
	getSub := func(id shared.UUID) *persistence.PublisherSubscriptionRow {
		t.Helper()
		var row *persistence.PublisherSubscriptionRow
		if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			var err error
			row, err = subs.Get(ctx, tx, id)
			return err
		}); err != nil {
			t.Fatalf("Get: %v", err)
		}
		return row
	}

	subID := shared.UUID(uuid.New())
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return subs.Insert(ctx, tx, persistence.PublisherSubscriptionRow{
			ID:             subID,
			InstanceID:     shared.UUID(fx.InstanceID),
			PublisherName:  "sensor-alpha",
			Kind:           "http",
			ResolvedConfig: []byte(`{"url":"https://example.invalid"}`),
			TargetNode:     "root",
			StartedAt:      time.Now().UTC(),
			// State left empty: the driver default is `mounting` —
			// rows are born unmounted by design.
		})
	}); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	row := getSub(subID)
	if row == nil || row.State != persistence.PublisherSubscriptionStateMounting {
		t.Fatalf("expected fresh row in state=mounting, got %+v", row)
	}
	if row.FailureReason != "" {
		t.Fatalf("expected empty failure_reason on fresh row, got %q", row.FailureReason)
	}

	// Reconciler selection: the mounting row is visible by state.
	mounting, err := subs.ListByState(ctx, persistence.PublisherSubscriptionStateMounting)
	if err != nil {
		t.Fatalf("ListByState(mounting): %v", err)
	}
	if len(mounting) != 1 || mounting[0].ID != subID {
		t.Fatalf("expected exactly the seeded row in ListByState(mounting), got %+v", mounting)
	}

	// Guarded flip: mounting → active succeeds exactly once.
	flipped, err := subs.CompareAndSetState(ctx, subID,
		persistence.PublisherSubscriptionStateMounting,
		persistence.PublisherSubscriptionStateActive, "")
	if err != nil {
		t.Fatalf("CompareAndSetState(mounting→active): %v", err)
	}
	if !flipped {
		t.Fatalf("expected mounting→active CAS to update the row")
	}

	// A late flip against a settled row is a no-op — the guard is the
	// reconciler's defense against overwriting a concurrent lifecycle
	// transition.
	flipped, err = subs.CompareAndSetState(ctx, subID,
		persistence.PublisherSubscriptionStateMounting,
		persistence.PublisherSubscriptionStateFailed, "late flip must not land")
	if err != nil {
		t.Fatalf("CompareAndSetState(stale guard): %v", err)
	}
	if flipped {
		t.Fatalf("CAS with stale `from` state must not update the row")
	}
	row = getSub(subID)
	if row.State != persistence.PublisherSubscriptionStateActive || row.FailureReason != "" {
		t.Fatalf("settled row overwritten by stale CAS: %+v", row)
	}

	// failure_reason round-trips on a real failed flip (fresh row).
	failedID := shared.UUID(uuid.New())
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return subs.Insert(ctx, tx, persistence.PublisherSubscriptionRow{
			ID:             failedID,
			InstanceID:     shared.UUID(fx.InstanceID),
			PublisherName:  "sensor-beta",
			Kind:           "http",
			ResolvedConfig: []byte(`{}`),
			TargetNode:     "root",
			StartedAt:      time.Now().UTC(),
		})
	}); err != nil {
		t.Fatalf("Insert failed-row fixture: %v", err)
	}
	flipped, err = subs.CompareAndSetState(ctx, failedID,
		persistence.PublisherSubscriptionStateMounting,
		persistence.PublisherSubscriptionStateFailed,
		`publisher "sensor-beta" is not registered`)
	if err != nil || !flipped {
		t.Fatalf("CompareAndSetState(mounting→failed): flipped=%v err=%v", flipped, err)
	}
	row = getSub(failedID)
	if row.State != persistence.PublisherSubscriptionStateFailed {
		t.Fatalf("expected state=failed, got %+v", row)
	}
	if row.FailureReason != `publisher "sensor-beta" is not registered` {
		t.Fatalf("failure_reason did not round-trip, got %q", row.FailureReason)
	}
}
