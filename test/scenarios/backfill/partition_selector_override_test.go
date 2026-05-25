// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// N9 scenario — partition_selector_override.
//
// CreateBackfill carries a `partition_request_override` byte slice
// that is opaque to rimsky; the fan-out node's substitution layer
// reads named fields by walkPath only. The scenario pins the
// payload-threading shape end-to-end: the override goes in,
// CreateBackfill enqueues a message whose payload carries it
// verbatim.
package backfill

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/fallguyconsulting/rimsky/foundation/shared"
	"github.com/fallguyconsulting/rimsky/runtime"
)

func TestPartitionSelectorOverride_RoundTripsThroughPayload(t *testing.T) {
	t.Parallel()
	m := newFakeMessages()
	ctx := context.Background()
	override := json.RawMessage(`{"partition_keys":["region-x","region-y"]}`)
	created, err := runtime.CreateBackfill(ctx, nil, m, time.Now().UTC(), runtime.BackfillCreateRequest{
		InstanceID:               shared.UUID(uuid.New()),
		TargetNode:               "ingest_results",
		PartitionRequestOverride: override,
		Reason:                   "operator backfill from scenario",
		Sender:                   "operator/scenario",
	})
	if err != nil {
		t.Fatalf("CreateBackfill: %v", err)
	}
	row, err := m.Get(ctx, created.MessageID)
	if err != nil || row == nil {
		t.Fatalf("Get message: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(row.Payload, &payload); err != nil {
		t.Fatalf("payload not JSON: %v", err)
	}
	if payload["backfill_operation_id"] != created.BackfillOperationID.String() {
		t.Errorf("payload.backfill_operation_id mismatch: %v vs %s",
			payload["backfill_operation_id"], created.BackfillOperationID)
	}
	if payload["reason"] != "operator backfill from scenario" {
		t.Errorf("payload.reason mismatch: %v", payload["reason"])
	}
	// The override field round-trips verbatim — opaque to rimsky per
	// @blessed-invariant 21.
	got, _ := json.Marshal(payload["partition_request_override"])
	if string(got) != string(override) {
		t.Errorf("partition_request_override round-trip mismatch: got %s want %s", got, override)
	}
}

func TestPartitionSelectorOverride_ValidatesInput(t *testing.T) {
	t.Parallel()
	m := newFakeMessages()
	if _, err := runtime.CreateBackfill(context.Background(), nil, m, time.Now().UTC(), runtime.BackfillCreateRequest{
		TargetNode: "ingest_results",
	}); err == nil {
		t.Error("expected error when instance_id is empty")
	}
	if _, err := runtime.CreateBackfill(context.Background(), nil, m, time.Now().UTC(), runtime.BackfillCreateRequest{
		InstanceID: shared.UUID(uuid.New()),
	}); err == nil {
		t.Error("expected error when target_node is empty")
	}
}
