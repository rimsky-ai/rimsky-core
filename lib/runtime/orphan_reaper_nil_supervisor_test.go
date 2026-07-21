// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

func TestReapOneClaimHandle_NilHolderSupervisorIDWarnsAndSkips(t *testing.T) {
	t.Parallel()
	capLog := shared.NewCapturingLogger()
	lh := persistence.ClaimHandleRow{
		ID:                 shared.UUID(uuid.New()),
		LockKind:           persistence.LockKindScope,
		HolderSupervisorID: nil,
	}

	if err := reapOneClaimHandle(context.Background(), OrphanReaperArgs{}, lh, capLog); err != nil {
		t.Fatalf("reapOneClaimHandle: %v", err)
	}

	found := false
	for _, rec := range capLog.Records() {
		if rec.Level == "warn" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a Warn log for a nil holder_supervisor_id row, got records=%#v", capLog.Records())
	}
}
