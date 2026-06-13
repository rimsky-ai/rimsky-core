// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// message_idempotency.go — MessageIdempotency conformance area.
//
// Pins the universal Idempotency-Key dedup tuple behavior the
// control-api message-emit path depends on (per concept:message):
//
//   - InsertOrLookup fresh insert returns inserted=true with the
//     supplied message id (the handler maps this to 201 Created).
//   - Replay of the SAME dedup tuple returns inserted=false with the
//     ORIGINAL message id and created_at (the handler maps this to
//     200 OK with the original message_id — the operator-visible
//     status-code distinction).
//   - The dedup tuple carries BOTH the sender_kind and sender_subject
//     discriminators: two distinct api-keys (different sender_subject)
//     and a publisher named like an operator (different sender_kind)
//     must NOT cross-collide on the same Idempotency-Key.
//   - DeleteOlderThan removes exactly the rows past the cutoff; a
//     replay of a swept tuple inserts fresh (at-most-once dedup is
//     bounded by cfg:messages.idempotency_ttl_seconds, not forever).
//
// The postgres driver implements InsertOrLookup as a single
// ON CONFLICT … RETURNING (xmax = 0) statement while sqlite uses
// INSERT OR IGNORE + a follow-up SELECT — a driver-specific SQL idiom
// pair with real drift risk, which is why every observable above is
// asserted identically on both drivers.
package conformance

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

// idempotencyInsertOrLookup wraps one InsertOrLookup call in its own tx
// (mirroring the production message-create tx) and fails the test on a
// driver error.
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
		out, inserted, err = store.MessageIdempotencies().InsertOrLookup(ctx, tx, row)
		return err
	}); err != nil {
		t.Fatalf("InsertOrLookup(%+v): %v", row, err)
	}
	return out, inserted
}

// testMessageIdempotencyInsertOrLookup pins the fresh-insert vs
// conflict-replay contract plus both dedup-tuple discriminators.
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

	// Fresh insert: inserted=true, the supplied message id comes back.
	first, inserted := idempotencyInsertOrLookup(ctx, t, d, base)
	if !inserted {
		t.Fatalf("fresh InsertOrLookup returned inserted=false")
	}
	if first.MessageID != base.MessageID {
		t.Fatalf("fresh InsertOrLookup message id = %s, want %s", first.MessageID, base.MessageID)
	}

	// Replay of the identical dedup tuple with a DIFFERENT message id:
	// inserted=false, and the ORIGINAL message id + created_at come back
	// (no duplicate envelope; the handler returns 200 with the original
	// message_id).
	replay := base
	replay.MessageID = shared.UUID(uuid.New())
	got, inserted := idempotencyInsertOrLookup(ctx, t, d, replay)
	if inserted {
		t.Fatalf("replay InsertOrLookup returned inserted=true")
	}
	if got.MessageID != base.MessageID {
		t.Fatalf("replay returned message id %s, want original %s", got.MessageID, base.MessageID)
	}
	// created_at must be the ORIGINAL row's timestamp, not the replay's.
	// Compare with a small tolerance: the drivers store at different
	// precisions (timestamptz µs vs RFC3339Nano text) but both must
	// return the stored original, which the fresh-insert call also
	// surfaced.
	if diff := got.CreatedAt.Sub(first.CreatedAt); diff < -time.Millisecond || diff > time.Millisecond {
		t.Fatalf("replay created_at %v drifted from original %v", got.CreatedAt, first.CreatedAt)
	}

	// sender_subject discriminator: a second api-key reusing the same
	// Idempotency-Key against the same instance is a FRESH insert (no
	// cross-caller replay leak).
	otherSubject := base
	otherSubject.SenderSubject = "api-key-B"
	otherSubject.MessageID = shared.UUID(uuid.New())
	got, inserted = idempotencyInsertOrLookup(ctx, t, d, otherSubject)
	if !inserted || got.MessageID != otherSubject.MessageID {
		t.Fatalf("distinct sender_subject collided: inserted=%v id=%s want=%s",
			inserted, got.MessageID, otherSubject.MessageID)
	}

	// sender_kind discriminator: a publisher whose publisher_name is the
	// literal "operator" must not share a dedup tuple with operator-side
	// emits.
	publisherKind := base
	publisherKind.SenderKind = "publisher"
	publisherKind.SenderSubject = ""
	publisherKind.MessageID = shared.UUID(uuid.New())
	got, inserted = idempotencyInsertOrLookup(ctx, t, d, publisherKind)
	if !inserted || got.MessageID != publisherKind.MessageID {
		t.Fatalf("distinct sender_kind collided: inserted=%v id=%s want=%s",
			inserted, got.MessageID, publisherKind.MessageID)
	}

	// sender discriminator: a different publisher name is its own tuple.
	otherSender := publisherKind
	otherSender.Sender = "publisher-two"
	otherSender.MessageID = shared.UUID(uuid.New())
	_, inserted = idempotencyInsertOrLookup(ctx, t, d, otherSender)
	if !inserted {
		t.Fatalf("distinct sender collided with existing tuple")
	}
}

// testMessageIdempotencyDeleteOlderThan pins the retention sweep: only
// rows past the cutoff are removed, the count is exact, and a swept
// tuple becomes insertable again (dedup is TTL-bounded, not eternal).
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

	// Sweep at a cutoff between the two rows: exactly the old row goes.
	deleted, err := table.DeleteOlderThan(ctx, now.Add(-1*time.Hour))
	if err != nil {
		t.Fatalf("DeleteOlderThan: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("DeleteOlderThan deleted %d rows, want exactly 1", deleted)
	}

	// The kept tuple still dedups…
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

	// …while the swept tuple inserts fresh.
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

	// A second sweep at the same cutoff is a no-op (count stays exact).
	deleted, err = table.DeleteOlderThan(ctx, now.Add(-1*time.Hour))
	if err != nil {
		t.Fatalf("second DeleteOlderThan: %v", err)
	}
	if deleted != 0 {
		t.Fatalf("second DeleteOlderThan deleted %d rows, want 0", deleted)
	}
}
