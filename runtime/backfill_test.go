// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/foundation/shared"
)

func TestCreateBackfill_EnqueuesInvalidateMessage(t *testing.T) {
	m := newFakeMessages()
	ctx := context.Background()
	inst := shared.UUID(uuid.New())
	now := time.Now().UTC()

	res, err := CreateBackfill(ctx, nil, m, now, BackfillCreateRequest{
		InstanceID:               inst,
		TargetNode:               "loader",
		PartitionRequestOverride: json.RawMessage(`{"range":"2026-01"}`),
		Reason:                   "missed",
		Sender:                   "op-123",
	})
	if err != nil {
		t.Fatalf("CreateBackfill: %v", err)
	}
	if res.MessageID == (shared.UUID{}) || res.BackfillOperationID == (shared.UUID{}) {
		t.Fatalf("expected non-zero ids, got %+v", res)
	}
	row, _ := m.Get(ctx, res.MessageID)
	if row == nil {
		t.Fatal("expected message to be enqueued")
	}
	if row.Kind != "invalidate" {
		t.Fatalf("expected kind=invalidate, got %q", row.Kind)
	}
	if row.Target != "loader" {
		t.Fatalf("expected target=loader, got %q", row.Target)
	}
	if row.BackfillOperationID == nil || *row.BackfillOperationID != res.BackfillOperationID {
		t.Fatalf("backfill_operation_id not propagated to row")
	}
}

func TestCancelBackfill_MarksPendingCancelled(t *testing.T) {
	m := newFakeMessages()
	ctx := context.Background()
	inst := shared.UUID(uuid.New())
	now := time.Now().UTC()
	created, err := CreateBackfill(ctx, nil, m, now, BackfillCreateRequest{
		InstanceID: inst, TargetNode: "loader", Sender: "op-A",
	})
	if err != nil {
		t.Fatalf("CreateBackfill: %v", err)
	}
	n, err := CancelBackfill(ctx, nil, m, now.Add(time.Second), created.BackfillOperationID)
	if err != nil {
		t.Fatalf("CancelBackfill: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 cancelled row, got %d", n)
	}
	row, _ := m.Get(ctx, created.MessageID)
	if !row.Cancelled {
		t.Fatal("expected row.Cancelled = true")
	}
	if row.DeliveredAt == nil {
		t.Fatal("expected row.DeliveredAt to be stamped on cancel")
	}
}

func TestGetBackfillStatus_PayloadReasonExtracted(t *testing.T) {
	m := newFakeMessages()
	ctx := context.Background()
	inst := shared.UUID(uuid.New())
	now := time.Now().UTC()
	created, _ := CreateBackfill(ctx, nil, m, now, BackfillCreateRequest{
		InstanceID: inst, TargetNode: "loader", Sender: "op-A", Reason: "test reason",
	})
	row, _ := m.Get(ctx, created.MessageID)
	if row == nil {
		t.Fatal("expected backfill message to be enqueued")
	}
	var payload struct {
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(row.Payload, &payload); err != nil {
		t.Fatalf("payload unmarshal: %v", err)
	}
	if payload.Reason != "test reason" {
		t.Fatalf("expected reason 'test reason', got %q", payload.Reason)
	}
}
