// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: message

package sqlite_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
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
		Name:                "messages-null-payload-fixture",
		Version:             "1",
		FrameResolutionMode: spec.FrameResolutionSerialQueue,
		FrameTimeoutMs:      600000,
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
			Kind:       "invalidate",
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
			Kind:       "invalidate",
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
