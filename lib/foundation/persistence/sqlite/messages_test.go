// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: message

package sqlite_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	sqlitepersistdrv "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

// seedMessageInstance reuses the same template+scope+instance pattern
// as seedBreakpointFixture (no FK violations for the messages.instance_id
// reference). Returns the instance id so the test can attach messages
// to it.
func seedMessageInstance(t *testing.T, ctx context.Context, d persistence.Database) shared.UUID {
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
		t.Fatalf("seedMessageInstance: %v", err)
	}
	return instanceID
}

// TestMessagesScan_NullPayload asserts that scanMessages tolerates a
// NULL payload column. Regression target for the cycle-1 fix that
// switched the scan to a nullable `[]byte` indirect — without that
// fix, Get/List/etc. of a NULL-payload row failed with a Scan error
// ("converting NULL to *json.RawMessage is unsupported").
func TestMessagesScan_NullPayload(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := openSQLiteDriver(t)
	instanceID := seedMessageInstance(t, ctx, d)

	messages := d.Tables().Messages()
	msgID := shared.UUID(uuid.New())

	// @deliberate: Payload omitted → json.RawMessage(nil) → driver sends NULL,
	// which is the exact column shape scanMessages must tolerate (cycle-1 fix).
	require := func(condition bool, msg string, args ...any) {
		t.Helper()
		if !condition {
			t.Fatalf(msg, args...)
		}
	}
	if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return messages.Insert(ctx, tx, persistence.EnqueueMessageRequest{
			ID:         msgID,
			InstanceID: instanceID,
			Type:       "invalidate",
			Sender:     "operator",
			SenderKind: "operator",
			// @deliberate: Payload omitted → json.RawMessage(nil) → NULL row;
			// matches the on-wire shape of an invalidate envelope with no body.
		})
	}); err != nil {
		t.Fatalf("Messages.Insert: %v", err)
	}

	// @constraint: NULL payload column must scan into a zero-length
	// json.RawMessage (nil or empty); cycle-1 nullable-shim contract.
	row, err := messages.Get(ctx, msgID)
	if err != nil {
		t.Fatalf("Messages.Get: %v (NULL payload must scan without error)", err)
	}
	require(row != nil, "Messages.Get returned nil for an inserted row")
	if len(row.Payload) != 0 {
		t.Fatalf("payload bytes = %q; want zero-length (NULL column → nil/empty json.RawMessage)", string(row.Payload))
	}
	// @constraint: empty RawMessage represents JSON null at this boundary;
	// any non-empty bytes must still parse as valid JSON.
	if len(row.Payload) > 0 {
		var v any
		if err := json.Unmarshal(row.Payload, &v); err != nil {
			t.Fatalf("payload is non-empty but not valid JSON: %v", err)
		}
	}

	// @constraint: List is a second scanMessages caller; per-instance filter
	// narrows scope to the NULL-payload row inserted above.
	instUUID := shared.UUID(instanceID)
	page, err := messages.List(ctx, persistence.MessageListFilter{InstanceID: &instUUID}, persistence.ListPagination{Limit: 10})
	if err != nil {
		t.Fatalf("Messages.List: %v (NULL payload must scan without error in List path too)", err)
	}
	if len(page.Rows) != 1 {
		t.Fatalf("List returned %d rows; want 1", len(page.Rows))
	}
	if len(page.Rows[0].Payload) != 0 {
		t.Fatalf("List row payload bytes = %q; want zero-length", string(page.Rows[0].Payload))
	}

	// @constraint: ListPendingForInstance is the third scanMessages caller;
	// covered so a regression dropping the nullable shim in any one caller fails here.
	var pending []persistence.MessageRow
	if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := messages.ListPendingForInstance(ctx, tx, instUUID)
		pending = r
		return err
	}); err != nil {
		t.Fatalf("Messages.ListPendingForInstance: %v (NULL payload must scan without error)", err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending rows = %d; want 1", len(pending))
	}
	if len(pending[0].Payload) != 0 {
		t.Fatalf("pending row payload bytes = %q; want zero-length", string(pending[0].Payload))
	}
}

// TestMessagesScan_NonNullPayload is the green-path sanity check: a
// row inserted with a real payload round-trips through scanMessages
// with the bytes intact. Pairs with TestMessagesScan_NullPayload so a
// regression that flipped the nullable handler to always-nil would
// fail this test too.
func TestMessagesScan_NonNullPayload(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := openSQLiteDriver(t)
	instanceID := seedMessageInstance(t, ctx, d)
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

// TestMessagesList_DeliveredAfterBefore pins the persistence-side
// predicates for the `delivered_after` / `delivered_before` filters
// surfaced through the GET /v1/instances/{id}/messages query params.
// The control-API handler advertises and accepts these filters; this
// test fails if a future refactor regresses the persistence layer to
// the silent-drop shape (where the filter is accepted at the HTTP
// boundary but the WHERE clause ignores it).
func TestMessagesList_DeliveredAfterBefore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := openSQLiteDriver(t)
	instanceID := seedMessageInstance(t, ctx, d)
	messages := d.Tables().Messages()
	rawDB := sqlitepersistdrv.DBFromDatabase(d)

	// @deliberate: seed three rows: one with delivered_at = t1, one
	// with t2, one with t3, frame_id NULL so the rimsky_frames FK is
	// not exercised.
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
		// @deliberate: stamp delivered_at directly — MarkDelivered
		// requires a real frame row, which would mean threading the
		// rimsky_frames FK just for this filter test. The persistence
		// layer's WHERE clause is the unit under test; the column write
		// itself goes through the same fixed-width format formatTime
		// emits.
		if _, err := rawDB.ExecContext(ctx,
			`UPDATE rimsky_messages SET delivered_at = ? WHERE id = ?`,
			tt.Format("2006-01-02T15:04:05.000000000Z07:00"), ids[i].String(),
		); err != nil {
			t.Fatalf("stamp delivered_at[%d]: %v", i, err)
		}
	}

	instUUID := shared.UUID(instanceID)

	// @deliberate: delivered_after = t1 returns rows with delivered_at
	// strictly greater than t1 (the t2 and t3 rows, NOT the t1 row).
	after := t1
	page, err := messages.List(ctx, persistence.MessageListFilter{
		InstanceID:     &instUUID,
		DeliveredAfter: &after,
	}, persistence.ListPagination{Limit: 10})
	if err != nil {
		t.Fatalf("List(delivered_after=t1): %v", err)
	}
	if len(page.Rows) != 2 {
		t.Fatalf("delivered_after filter: got %d rows, want 2 (t2, t3)", len(page.Rows))
	}

	// @deliberate: delivered_before = t3 returns rows with delivered_at
	// strictly less than t3 (the t1 and t2 rows, NOT the t3 row).
	before := t3
	page, err = messages.List(ctx, persistence.MessageListFilter{
		InstanceID:      &instUUID,
		DeliveredBefore: &before,
	}, persistence.ListPagination{Limit: 10})
	if err != nil {
		t.Fatalf("List(delivered_before=t3): %v", err)
	}
	if len(page.Rows) != 2 {
		t.Fatalf("delivered_before filter: got %d rows, want 2 (t1, t2)", len(page.Rows))
	}

	// @deliberate: both bounds in combination return rows strictly
	// between them (only the t2 row).
	page, err = messages.List(ctx, persistence.MessageListFilter{
		InstanceID:      &instUUID,
		DeliveredAfter:  &after,
		DeliveredBefore: &before,
	}, persistence.ListPagination{Limit: 10})
	if err != nil {
		t.Fatalf("List(delivered_after=t1, delivered_before=t3): %v", err)
	}
	if len(page.Rows) != 1 || page.Rows[0].ID != ids[1] {
		t.Fatalf("delivered_after+before window: got %+v, want exactly t2 row %s",
			page.Rows, ids[1])
	}
}
