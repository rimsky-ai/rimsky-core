// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// N7 scenario — recursive_ancestor_walk.
//
// Lineage rows form a parent_run_id chain across the run-tree;
// downstream tools recover ancestry by walking the run-tree
// upward via persistence.LineageTable.GetByRunID. This scenario
// pins that multiple leaf records sharing a frame can co-exist
// without collisions.
package lineage

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/fallguy/rimsky/foundation/shared"
	"github.com/fallguy/rimsky/runtime"
)

func TestRecursiveAncestorWalk_MultipleLeafRowsSameFrame(t *testing.T) {
	t.Parallel()
	lt := &fakeLineage{}
	ctx := context.Background()
	frameID := shared.UUID(uuid.New())
	instanceID := shared.UUID(uuid.New())
	const N = 5
	parent := shared.UUID(uuid.New())
	for i := 0; i < N; i++ {
		rec := runtime.LeafRunRecord{
			RunID:       shared.UUID(uuid.New()),
			NodeID:      parent,
			FrameID:     frameID,
			ChildKey:    "partition-" + string(rune('a'+i)),
			State:       "fresh",
			LastOutcome: "fresh_changed",
		}
		if err := runtime.WriteLeafRunLineage(ctx, nil, lt, instanceID, frameID, time.Now().UTC(), rec); err != nil {
			t.Fatalf("WriteLeafRunLineage[%d]: %v", i, err)
		}
	}
	if len(lt.rows) != N {
		t.Errorf("expected %d rows, got %d", N, len(lt.rows))
	}
	// Pin: distinct row IDs across the batch (no collisions).
	seenIDs := make(map[shared.UUID]bool, N)
	for _, r := range lt.rows {
		if seenIDs[r.ID] {
			t.Errorf("duplicate lineage row id %s", r.ID)
		}
		seenIDs[r.ID] = true
	}
}
