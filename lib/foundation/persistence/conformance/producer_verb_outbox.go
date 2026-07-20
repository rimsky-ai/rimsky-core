// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package conformance

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

type producerVerbOutboxProvider interface {
	ProducerVerbOutbox() persistence.ProducerVerbOutboxTable
}

func TestProducerVerbOutbox(t *testing.T, d persistence.Database) {
	t.Helper()
	ctx := context.Background()
	provider, ok := d.Tables().(producerVerbOutboxProvider)
	if !ok {
		t.Fatalf("Tables backend %T must provide ProducerVerbOutbox()", d.Tables())
	}
	outbox := provider.ProducerVerbOutbox()

	base := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	producerA := "outbox-conf-producer-a-" + uuid.NewString()
	producerB := "outbox-conf-producer-b-" + uuid.NewString()
	claim1 := shared.UUID(uuid.New())
	claim2 := shared.UUID(uuid.New())
	claim3 := shared.UUID(uuid.New())
	instanceID := shared.UUID(uuid.New())
	parentID := shared.UUID(uuid.New())

	enqueue := func(claim shared.UUID, producer string, verb persistence.ProducerVerb) {
		t.Helper()
		if err := outbox.Enqueue(ctx, persistence.ProducerVerbOutboxInsertInput{
			ClaimHandleID:       claim,
			ProducerName:        producer,
			Verb:                verb,
			ClaimScopeData:      []byte(`"scope-` + claim.String() + `"`),
			Address:             []byte(`"addr"`),
			SupervisorID:        "sup-conf",
			InstanceID:          &instanceID,
			ParentClaimHandleID: &parentID,
			NextAttemptAt:       base,
			EnqueuedAt:          base,
		}, nil); err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
	}

	listMine := func() []persistence.ProducerVerbOutboxRow {
		t.Helper()
		all, err := outbox.ListAll(ctx, nil)
		if err != nil {
			t.Fatalf("ListAll: %v", err)
		}
		mine := make([]persistence.ProducerVerbOutboxRow, 0, len(all))
		for _, r := range all {
			if r.ProducerName == producerA || r.ProducerName == producerB {
				mine = append(mine, r)
			}
		}
		return mine
	}

	t.Run("EnqueueOrdersBySeqAndRoundTripsFields", func(t *testing.T) {
		enqueue(claim1, producerA, persistence.ProducerVerbCommit)
		enqueue(claim2, producerA, persistence.ProducerVerbAbandon)
		enqueue(claim3, producerB, persistence.ProducerVerbRelease)
		rows := listMine()
		if len(rows) != 3 {
			t.Fatalf("want 3 rows, got %d", len(rows))
		}
		if rows[0].ClaimHandleID != claim1 || rows[1].ClaimHandleID != claim2 || rows[2].ClaimHandleID != claim3 {
			t.Fatalf("rows must list in enqueue (seq) order: %+v", rows)
		}
		if rows[0].Seq >= rows[1].Seq || rows[1].Seq >= rows[2].Seq {
			t.Fatalf("seq must be strictly increasing: %d %d %d", rows[0].Seq, rows[1].Seq, rows[2].Seq)
		}
		r := rows[0]
		if r.Verb != persistence.ProducerVerbCommit || r.SupervisorID != "sup-conf" {
			t.Fatalf("field round-trip: %+v", r)
		}
		if !bytes.Equal(r.Address, []byte(`"addr"`)) {
			t.Fatalf("address round-trip: %q", r.Address)
		}
		if r.InstanceID == nil || *r.InstanceID != instanceID {
			t.Fatalf("instance_id round-trip: %+v", r.InstanceID)
		}
		if r.ParentClaimHandleID == nil || *r.ParentClaimHandleID != parentID {
			t.Fatalf("parent_claim_handle_id round-trip: %+v", r.ParentClaimHandleID)
		}
		if !r.NextAttemptAt.Equal(base) || !r.EnqueuedAt.Equal(base) {
			t.Fatalf("time round-trip: next=%v enq=%v want %v", r.NextAttemptAt, r.EnqueuedAt, base)
		}
		if r.AttemptCount != 0 || r.LastError != "" {
			t.Fatalf("fresh row must have zero attempts and empty last_error: %+v", r)
		}
	})

	t.Run("EnqueueIsIdempotentPerClaimAndVerb", func(t *testing.T) {
		enqueue(claim1, producerA, persistence.ProducerVerbCommit)
		if got := len(listMine()); got != 3 {
			t.Fatalf("duplicate (claim, verb) must not create a new row: got %d rows", got)
		}
	})

	t.Run("ListByProducerFiltersAndOrders", func(t *testing.T) {
		rows, err := outbox.ListByProducer(ctx, producerA, nil)
		if err != nil {
			t.Fatalf("ListByProducer: %v", err)
		}
		if len(rows) != 2 || rows[0].ClaimHandleID != claim1 || rows[1].ClaimHandleID != claim2 {
			t.Fatalf("ListByProducer(%s): %+v", producerA, rows)
		}
	})

	t.Run("RecordAttemptBumpsCountAndReschedules", func(t *testing.T) {
		rows := listMine()
		next := base.Add(42 * time.Second)
		if err := outbox.RecordAttempt(ctx, rows[0].Seq, next, "dial refused", nil); err != nil {
			t.Fatalf("RecordAttempt: %v", err)
		}
		if err := outbox.RecordAttempt(ctx, rows[0].Seq, next, "dial refused again", nil); err != nil {
			t.Fatalf("RecordAttempt: %v", err)
		}
		got := listMine()[0]
		if got.AttemptCount != 2 {
			t.Fatalf("attempt_count: want 2, got %d", got.AttemptCount)
		}
		if !got.NextAttemptAt.Equal(next) {
			t.Fatalf("next_attempt_at: want %v, got %v", next, got.NextAttemptAt)
		}
		if got.LastError != "dial refused again" {
			t.Fatalf("last_error: %q", got.LastError)
		}
	})

	t.Run("CountByProducerReportsUndeliveredPerProducer", func(t *testing.T) {
		counts, err := outbox.CountByProducer(ctx, nil)
		if err != nil {
			t.Fatalf("CountByProducer: %v", err)
		}
		if counts[producerA] != 2 || counts[producerB] != 1 {
			t.Fatalf("counts: %+v", counts)
		}
	})

	t.Run("DeleteRemovesExactlyOneRow", func(t *testing.T) {
		rows := listMine()
		if err := outbox.Delete(ctx, rows[0].Seq, nil); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if err := outbox.Delete(ctx, rows[0].Seq, nil); err != nil {
			t.Fatalf("Delete must be idempotent: %v", err)
		}
		remaining := listMine()
		if len(remaining) != 2 {
			t.Fatalf("want 2 rows after delete, got %d", len(remaining))
		}
		for _, r := range remaining {
			if r.Seq == rows[0].Seq {
				t.Fatalf("deleted seq still present: %+v", r)
			}
		}
	})
}
