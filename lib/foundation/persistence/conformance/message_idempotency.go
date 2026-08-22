// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package conformance

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

func idempotencyInsertOrLookup(
	ctx context.Context, t *testing.T, d persistence.Database,
	row persistence.MessageIdempotencyRow,
) (persistence.MessageIdempotencyRow, bool) {
	t.Helper()
	store := d.Tables()
	var out persistence.MessageIdempotencyRow
	var inserted bool
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		var err error
		out, inserted, err = store.MessageIdempotencies().InsertOrLookup(ctx, row, tx)
		return err
	}); err != nil {
		t.Fatalf("InsertOrLookup(%+v): %v", row, err)
	}
	return out, inserted
}

// @decision: message-idempotencies-dedup-tuple
func testMessageIdempotencyInsertOrLookup(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)

	base := persistence.MessageIdempotencyRow{
		InstanceID:     fix.InstanceID,
		SenderKind:     "operator",
		Sender:         "operator",
		SenderSubject:  "api-key-A",
		IdempotencyKey: "key-1",
		MessageID:      shared.UUID(uuid.New()),
	}

	first, inserted := idempotencyInsertOrLookup(ctx, t, d, base)
	if !inserted {
		t.Fatalf("fresh InsertOrLookup returned inserted=false")
	}
	if first.MessageID != base.MessageID {
		t.Fatalf("fresh InsertOrLookup message id = %s, want %s", first.MessageID, base.MessageID)
	}

	replay := base
	replay.MessageID = shared.UUID(uuid.New())
	got, inserted := idempotencyInsertOrLookup(ctx, t, d, replay)
	if inserted {
		t.Fatalf("replay InsertOrLookup returned inserted=true")
	}
	if got.MessageID != base.MessageID {
		t.Fatalf("replay returned message id %s, want original %s", got.MessageID, base.MessageID)
	}
	if diff := got.CreatedAt.Sub(first.CreatedAt); diff < -time.Millisecond || diff > time.Millisecond {
		t.Fatalf("replay created_at %v drifted from original %v", got.CreatedAt, first.CreatedAt)
	}

	otherSubject := base
	otherSubject.SenderSubject = "api-key-B"
	otherSubject.MessageID = shared.UUID(uuid.New())
	got, inserted = idempotencyInsertOrLookup(ctx, t, d, otherSubject)
	if !inserted || got.MessageID != otherSubject.MessageID {
		t.Fatalf("distinct sender_subject collided: inserted=%v id=%s want=%s",
			inserted, got.MessageID, otherSubject.MessageID)
	}

	publisherKind := base
	publisherKind.SenderKind = "publisher"
	publisherKind.SenderSubject = ""
	publisherKind.MessageID = shared.UUID(uuid.New())
	got, inserted = idempotencyInsertOrLookup(ctx, t, d, publisherKind)
	if !inserted || got.MessageID != publisherKind.MessageID {
		t.Fatalf("distinct sender_kind collided: inserted=%v id=%s want=%s",
			inserted, got.MessageID, publisherKind.MessageID)
	}

	otherSender := publisherKind
	otherSender.Sender = "publisher-two"
	otherSender.MessageID = shared.UUID(uuid.New())
	_, inserted = idempotencyInsertOrLookup(ctx, t, d, otherSender)
	if !inserted {
		t.Fatalf("distinct sender collided with existing tuple")
	}

	// @decision: message-sender-kind-discriminator
	for _, kind := range []string{persistence.DedupSenderKindInstance, persistence.DedupSenderKindAnonymous} {
		row := base
		row.SenderKind = kind
		row.SenderSubject = ""
		row.MessageID = shared.UUID(uuid.New())
		got, inserted = idempotencyInsertOrLookup(ctx, t, d, row)
		if !inserted || got.MessageID != row.MessageID {
			t.Fatalf("the dedup discriminator must admit %q: inserted=%v id=%s want=%s",
				kind, inserted, got.MessageID, row.MessageID)
		}
	}

	unknownKind := base
	unknownKind.SenderKind = "sensor"
	unknownKind.MessageID = shared.UUID(uuid.New())
	if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		_, _, err := d.Tables().MessageIdempotencies().InsertOrLookup(ctx, unknownKind, tx)
		return err
	}); err == nil {
		t.Fatal("the dedup discriminator is a closed four-value enum; an unknown sender_kind must be refused")
	}

	otherInstance := seedFixtureSet(ctx, t, d)
	otherInstanceRow := base
	otherInstanceRow.InstanceID = otherInstance.InstanceID
	otherInstanceRow.MessageID = shared.UUID(uuid.New())
	got, inserted = idempotencyInsertOrLookup(ctx, t, d, otherInstanceRow)
	if !inserted || got.MessageID != otherInstanceRow.MessageID {
		t.Fatalf("identical (kind, sender, subject, key) tuple under a different instance_id collided "+
			"with the first instance's row: inserted=%v id=%s want=%s",
			inserted, got.MessageID, otherInstanceRow.MessageID)
	}
}

func testMessageIdempotencyInsertOrLookupRace(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)

	row := func() persistence.MessageIdempotencyRow {
		return persistence.MessageIdempotencyRow{
			InstanceID:     fix.InstanceID,
			SenderKind:     "operator",
			Sender:         "operator",
			SenderSubject:  "api-key-race",
			IdempotencyKey: "race-key",
			MessageID:      shared.UUID(uuid.New()),
		}
	}

	store := d.Tables()
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		results []persistence.MessageIdempotencyRow
		inserts int
	)
	race := func() {
		defer wg.Done()
		r := row()
		var out persistence.MessageIdempotencyRow
		var inserted bool
		if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			var err error
			out, inserted, err = store.MessageIdempotencies().InsertOrLookup(ctx, r, tx)
			return err
		}); err != nil {
			t.Errorf("racing InsertOrLookup: %v", err)
			return
		}
		mu.Lock()
		results = append(results, out)
		if inserted {
			inserts++
		}
		mu.Unlock()
	}

	wg.Add(2)
	go race()
	go race()
	wg.Wait()

	if inserts != 1 {
		t.Fatalf("two racing InsertOrLookup calls on the same key must produce exactly one insert; got %d", inserts)
	}
	if len(results) != 2 || results[0].MessageID != results[1].MessageID {
		t.Fatalf("racing InsertOrLookup calls must resolve to a single shared message id; got %+v", results)
	}
}

func testMessageIdempotencyDeleteOlderThan(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	table := d.Tables().MessageIdempotencies()

	now := time.Now().UTC()
	oldRow := persistence.MessageIdempotencyRow{
		InstanceID:     fix.InstanceID,
		SenderKind:     "operator",
		Sender:         "operator",
		SenderSubject:  "api-key-A",
		IdempotencyKey: "swept-key",
		MessageID:      shared.UUID(uuid.New()),
		CreatedAt:      now.Add(-2 * time.Hour),
	}
	freshRow := persistence.MessageIdempotencyRow{
		InstanceID:     fix.InstanceID,
		SenderKind:     "operator",
		Sender:         "operator",
		SenderSubject:  "api-key-A",
		IdempotencyKey: "kept-key",
		MessageID:      shared.UUID(uuid.New()),
		CreatedAt:      now,
	}
	if _, inserted := idempotencyInsertOrLookup(ctx, t, d, oldRow); !inserted {
		t.Fatalf("old-row seed did not insert")
	}
	if _, inserted := idempotencyInsertOrLookup(ctx, t, d, freshRow); !inserted {
		t.Fatalf("fresh-row seed did not insert")
	}

	deleted, err := table.DeleteOlderThan(ctx, now.Add(-1*time.Hour))
	if err != nil {
		t.Fatalf("DeleteOlderThan: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("DeleteOlderThan deleted %d rows, want exactly 1", deleted)
	}

	got, inserted := idempotencyInsertOrLookup(ctx, t, d, persistence.MessageIdempotencyRow{
		InstanceID:     fix.InstanceID,
		SenderKind:     "operator",
		Sender:         "operator",
		SenderSubject:  "api-key-A",
		IdempotencyKey: "kept-key",
		MessageID:      shared.UUID(uuid.New()),
	})
	if inserted || got.MessageID != freshRow.MessageID {
		t.Fatalf("kept tuple no longer dedups: inserted=%v id=%s want=%s",
			inserted, got.MessageID, freshRow.MessageID)
	}

	resend := persistence.MessageIdempotencyRow{
		InstanceID:     fix.InstanceID,
		SenderKind:     "operator",
		Sender:         "operator",
		SenderSubject:  "api-key-A",
		IdempotencyKey: "swept-key",
		MessageID:      shared.UUID(uuid.New()),
	}
	got, inserted = idempotencyInsertOrLookup(ctx, t, d, resend)
	if !inserted || got.MessageID != resend.MessageID {
		t.Fatalf("swept tuple did not insert fresh: inserted=%v id=%s want=%s",
			inserted, got.MessageID, resend.MessageID)
	}

	deleted, err = table.DeleteOlderThan(ctx, now.Add(-1*time.Hour))
	if err != nil {
		t.Fatalf("second DeleteOlderThan: %v", err)
	}
	if deleted != 0 {
		t.Fatalf("second DeleteOlderThan deleted %d rows, want 0", deleted)
	}
}
