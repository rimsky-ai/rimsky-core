// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// N9 scenario — cancellation_policy.
//
// CancelBackfill marks every pending message bound to opID as
// cancelled (cancelled=TRUE, delivered_at=now, frame_id=NULL). The
// scenario pins the count returned and the side-table flip.
package backfill

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/fallguyconsulting/rimsky/foundation/shared"
	"github.com/fallguyconsulting/rimsky/runtime"
)

func TestCancellationPolicy_MarksPendingBackfillCancelled(t *testing.T) {
	t.Parallel()
	m := newFakeMessages()
	ctx := context.Background()
	instanceID := shared.UUID(uuid.New())
	now := time.Now().UTC()

	created, err := runtime.CreateBackfill(ctx, nil, m, now, runtime.BackfillCreateRequest{
		InstanceID: instanceID,
		TargetNode: "ingest_results",
		Sender:     "operator/scenario",
	})
	if err != nil {
		t.Fatalf("CreateBackfill: %v", err)
	}
	n, err := runtime.CancelBackfill(ctx, nil, m, now, created.BackfillOperationID)
	if err != nil {
		t.Fatalf("CancelBackfill: %v", err)
	}
	if n != 1 {
		t.Errorf("CancelBackfill: marked %d (want 1)", n)
	}
	row, err := m.Get(ctx, created.MessageID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !row.Cancelled {
		t.Error("row.Cancelled = false after CancelBackfill")
	}
	if row.FrameID != nil {
		t.Errorf("row.FrameID = %v (want nil after cancel)", row.FrameID)
	}
}

func TestCancellationPolicy_NoOpOnUnknownOpID(t *testing.T) {
	t.Parallel()
	m := newFakeMessages()
	n, err := runtime.CancelBackfill(context.Background(), nil, m, time.Now().UTC(), shared.UUID(uuid.New()))
	if err != nil {
		t.Errorf("unknown opID: expected nil error, got %v", err)
	}
	if n != 0 {
		t.Errorf("unknown opID: expected 0 cancelled, got %d", n)
	}
}
