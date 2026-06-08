// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// N9 scenario — partition_selector_override input validation.
//
// CreateBackfill carries a `partition_request_override` byte slice
// that is opaque to rimsky; the fan-out node's substitution layer
// reads named fields by walkPath only. The payload round-trip proof
// that once lived here (TestPartitionSelectorOverride_RoundTripsThroughPayload)
// is superseded by the full-stack scenario
// TestBackfillPartitionOverrideFullStack
// (test/scenarios/backfill_partition_override_fullstack_test.go), which
// drives the override through the REAL backfill → message-delivery →
// fan-out SplitScope acquisition path against a testcontainers Postgres
// rather than a fake message bus — the end-to-end materialization is now
// the real proof (spec S-lifecycle-fullstack-terminate-backfill). What
// remains here is the orthogonal, cheap input-validation of CreateBackfill.
package backfill

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
)

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
