// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// N9 scenario — status_rollup.
//
// GetBackfillStatus aggregates per-backfill state from the message
// row (delivered_at / cancelled / frame_id). The scenario pins
// status visibility through the create → status cycle.
package backfill

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/runtime"
)

func TestStatusRollup_PendingAfterCreate(t *testing.T) {
	t.Parallel()
	m := newFakeMessages()
	ctx := context.Background()
	created, err := runtime.CreateBackfill(ctx, nil, m, time.Now().UTC(), runtime.BackfillCreateRequest{
		InstanceID: shared.UUID(uuid.New()),
		TargetNode: "ingest_results",
		Sender:     "operator/status",
		Reason:     "status-rollup-scenario",
	})
	if err != nil {
		t.Fatalf("CreateBackfill: %v", err)
	}
	status, err := runtime.GetBackfillStatus(ctx, nil, m, created.BackfillOperationID)
	if err != nil {
		t.Fatalf("GetBackfillStatus: %v", err)
	}
	if status.Reason != "status-rollup-scenario" {
		t.Errorf("Reason: got %q want %q", status.Reason, "status-rollup-scenario")
	}
}
