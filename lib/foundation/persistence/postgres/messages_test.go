// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: message

package postgres_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/internal/pgtest"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

// seedMessageInstanceForNullTest for the postgres mirror — uses the same template
// → main run scope → instance pattern as seedFrameParkedFixture so the
// rimsky_messages.instance_id FK is satisfied.
func seedMessageInstanceForNullTest(t *testing.T, ctx context.Context, d persistence.Database) shared.UUID {
	t.Helper()
	store := d.Tables()
	templateHash := "sha256-" + uuid.NewString()
	instanceID := uuid.New()
	mainRunScopeID := uuid.New()

	tmpl := spec.TemplateSpec{
		Name:           "messages-null-payload-fixture",
		Version:        "1",
		FrameTimeoutMs: 600000,
		Nodes: []spec.TemplateNodeDef{
			{Type: "n", Executor: "test-executor"},
		},
	}
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := store.Templates().Insert(ctx, persistence.TemplateInsertInput{
			ID:     templateHash,
			Spec:   tmpl,
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
		return nil
	}); err != nil {
		t.Fatalf("seedMessageInstanceForNullTest: %v", err)
	}
	return instanceID
}

// TestMessagesScan_NullPayload_Postgres confirms pgx tolerates a NULL
// payload column through the rimsky_messages → MessageRow scan path.
// Symmetric to TestMessagesScan_NullPayload in the sqlite test.
func TestMessagesScan_NullPayload_Postgres(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
	instanceID := seedMessageInstanceForNullTest(t, ctx, d)

	messages := d.Tables().Messages()
	msgID := shared.UUID(uuid.New())

	if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return messages.Insert(ctx, tx, persistence.EnqueueMessageRequest{
			ID:         msgID,
			InstanceID: instanceID,
			Type:       "invalidate",
			Sender:     "operator",
			SenderKind: "operator",
			// @deliberate: Payload omitted → nil → NULL on the wire; this test exercises the NULL-payload scan path.
		})
	}); err != nil {
		t.Fatalf("Messages.Insert: %v", err)
	}

	row, err := messages.Get(ctx, msgID)
	if err != nil {
		t.Fatalf("Messages.Get: %v (NULL payload must scan without error)", err)
	}
	if row == nil {
		t.Fatalf("Messages.Get returned nil for an inserted row")
	}
	if len(row.Payload) != 0 {
		t.Fatalf("payload bytes = %q; want zero-length (NULL column → nil/empty json.RawMessage)", string(row.Payload))
	}

	instUUID := shared.UUID(instanceID)
	page, err := messages.List(ctx, persistence.MessageListFilter{InstanceID: &instUUID}, persistence.ListPagination{Limit: 10})
	if err != nil {
		t.Fatalf("Messages.List: %v (NULL payload must scan in List path)", err)
	}
	if len(page.Rows) != 1 {
		t.Fatalf("List returned %d rows; want 1", len(page.Rows))
	}
	if len(page.Rows[0].Payload) != 0 {
		t.Fatalf("List row payload bytes = %q; want zero-length", string(page.Rows[0].Payload))
	}

	var pending []persistence.MessageRow
	if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := messages.ListPendingForInstance(ctx, tx, instUUID)
		pending = r
		return err
	}); err != nil {
		t.Fatalf("Messages.ListPendingForInstance: %v (NULL payload must scan)", err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending rows = %d; want 1", len(pending))
	}
	if len(pending[0].Payload) != 0 {
		t.Fatalf("pending row payload bytes = %q; want zero-length", string(pending[0].Payload))
	}
}

// TestMessagesList_DeliveredAfterBefore_Postgres pins the persistence
// predicates for the `delivered_after` / `delivered_before` filters
// surfaced through GET /v1/instances/{id}/messages. The control-API
// handler advertises and accepts these filters; this test fails if a
// future refactor regresses the persistence layer to silently drop
// them. Symmetric to the sqlite-side coverage so the conformance suite
// catches cross-backend drift.
func TestMessagesList_DeliveredAfterBefore_Postgres(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
	instanceID := seedMessageInstanceForNullTest(t, ctx, d)
	messages := d.Tables().Messages()

	t1 := time.Date(2026, 6, 14, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 6, 14, 11, 0, 0, 0, time.UTC)
	t3 := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	ids := make([]shared.UUID, 3)
	for i, tt := range []time.Time{t1, t2, t3} {
		ids[i] = shared.UUID(uuid.New())
		if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			return messages.Insert(ctx, tx, persistence.EnqueueMessageRequest{
				ID:         ids[i],
				InstanceID: instanceID,
				Type:       "ping/recheck",
				Sender:     "operator",
				SenderKind: "operator",
				ReceivedAt: tt.Add(-time.Hour),
			})
		}); err != nil {
			t.Fatalf("Messages.Insert[%d]: %v", i, err)
		}
		// Stamp delivered_at directly — MarkDelivered requires a real
		// rimsky_frames row, which would mean threading the FK just for
		// this filter test. The persistence-layer WHERE clause is the
		// unit under test.
		pgtest.ExecForTest(ctx, t, d,
			`UPDATE rimsky_messages SET delivered_at = $1 WHERE id = $2`, tt, ids[i])
	}

	instUUID := shared.UUID(instanceID)

	after := t1
	page, err := messages.List(ctx, persistence.MessageListFilter{
		InstanceID:     &instUUID,
		DeliveredAfter: &after,
	}, persistence.ListPagination{Limit: 10})
	if err != nil {
		t.Fatalf("List(delivered_after=t1): %v", err)
	}
	if len(page.Rows) != 2 {
		t.Fatalf("delivered_after: got %d rows, want 2 (t2, t3)", len(page.Rows))
	}

	before := t3
	page, err = messages.List(ctx, persistence.MessageListFilter{
		InstanceID:      &instUUID,
		DeliveredBefore: &before,
	}, persistence.ListPagination{Limit: 10})
	if err != nil {
		t.Fatalf("List(delivered_before=t3): %v", err)
	}
	if len(page.Rows) != 2 {
		t.Fatalf("delivered_before: got %d rows, want 2 (t1, t2)", len(page.Rows))
	}

	page, err = messages.List(ctx, persistence.MessageListFilter{
		InstanceID:      &instUUID,
		DeliveredAfter:  &after,
		DeliveredBefore: &before,
	}, persistence.ListPagination{Limit: 10})
	if err != nil {
		t.Fatalf("List(delivered_after+before): %v", err)
	}
	if len(page.Rows) != 1 || page.Rows[0].ID != ids[1] {
		t.Fatalf("delivered_after+before window: got %+v, want exactly t2 row %s",
			page.Rows, ids[1])
	}
}

// TestMessagesScan_NonNullPayload_Postgres is the green-path sanity
// check: payload bytes round-trip through Insert → scanMessages
// unchanged on postgres.
func TestMessagesScan_NonNullPayload_Postgres(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
	instanceID := seedMessageInstanceForNullTest(t, ctx, d)
	messages := d.Tables().Messages()
	msgID := shared.UUID(uuid.New())

	payload := json.RawMessage(`{"hello":"world"}`)
	if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return messages.Insert(ctx, tx, persistence.EnqueueMessageRequest{
			ID:         msgID,
			InstanceID: instanceID,
			Type:       "invalidate",
			Sender:     "operator",
			SenderKind: "operator",
			Payload:    payload,
		})
	}); err != nil {
		t.Fatalf("Messages.Insert: %v", err)
	}
	row, err := messages.Get(ctx, msgID)
	if err != nil {
		t.Fatalf("Messages.Get: %v", err)
	}
	if row == nil {
		t.Fatalf("Messages.Get returned nil")
	}
	if string(row.Payload) != string(payload) {
		t.Fatalf("payload round-trip: got %q, want %q", string(row.Payload), string(payload))
	}
}
