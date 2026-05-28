// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// N9 scenario — lineage_chain.
//
// A backfill operation produces invalidate messages bound to a
// BackfillOperationID; downstream lineage rows projected from the
// resulting fan-out runs reference back to the op_id so the lineage
// chain can be traced. The scenario pins the BackfillOperationID
// threading through the message payload.
package backfill

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
)

func TestLineageChain_OperationIDThreadsThroughMessage(t *testing.T) {
	t.Parallel()
	m := newFakeMessages()
	ctx := context.Background()
	created, err := runtime.CreateBackfill(ctx, nil, m, time.Now().UTC(), runtime.BackfillCreateRequest{
		InstanceID: shared.UUID(uuid.New()),
		TargetNode: "ingest_results",
		Sender:     "operator/lineage",
		Reason:     "lineage-chain-scenario",
	})
	if err != nil {
		t.Fatalf("CreateBackfill: %v", err)
	}
	row, err := m.Get(ctx, created.MessageID)
	if err != nil || row == nil {
		t.Fatalf("Get message: %v", err)
	}
	if row.BackfillOperationID == nil || *row.BackfillOperationID != created.BackfillOperationID {
		t.Errorf("message.BackfillOperationID mismatch: %v vs %s", row.BackfillOperationID, created.BackfillOperationID)
	}
	var payload struct {
		BackfillOperationID string `json:"backfill_operation_id"`
	}
	if err := json.Unmarshal(row.Payload, &payload); err != nil {
		t.Fatalf("payload not JSON: %v", err)
	}
	if payload.BackfillOperationID != created.BackfillOperationID.String() {
		t.Errorf("payload.backfill_operation_id mismatch: %s vs %s",
			payload.BackfillOperationID, created.BackfillOperationID)
	}
}
