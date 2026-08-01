// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package lineage

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
)

func TestRecursiveAncestorWalk_ChainsParentRunID(t *testing.T) {
	t.Parallel()
	lt := &queryableLineage{}
	ctx := context.Background()
	frameID := shared.UUID(uuid.New())
	instanceID := shared.UUID(uuid.New())

	rootRunID := shared.UUID(uuid.New())
	childRunID := shared.UUID(uuid.New())
	grandchildRunID := shared.UUID(uuid.New())

	if err := runtime.WriteLeafRunLineage(ctx, lt, instanceID, frameID, time.Now().UTC(), runtime.LeafRunRecord{
		NodeRunID:          rootRunID,
		NodeID:             shared.UUID(uuid.New()),
		FrameID:            frameID,
		State:              "fresh",
		SettlingSignalType: "terminal/success",
	}, nil); err != nil {
		t.Fatalf("seed root: %v", err)
	}
	if err := runtime.WriteLeafRunLineage(ctx, lt, instanceID, frameID, time.Now().UTC(), runtime.LeafRunRecord{
		NodeRunID:          childRunID,
		NodeID:             shared.UUID(uuid.New()),
		FrameID:            frameID,
		State:              "fresh",
		SettlingSignalType: "terminal/success",
		ParentNodeRunID:    rootRunID.String(),
	}, nil); err != nil {
		t.Fatalf("seed child: %v", err)
	}
	if err := runtime.WriteLeafRunLineage(ctx, lt, instanceID, frameID, time.Now().UTC(), runtime.LeafRunRecord{
		NodeRunID:          grandchildRunID,
		NodeID:             shared.UUID(uuid.New()),
		FrameID:            frameID,
		State:              "fresh",
		SettlingSignalType: "terminal/success",
		ParentNodeRunID:    childRunID.String(),
	}, nil); err != nil {
		t.Fatalf("seed grandchild: %v", err)
	}

	current := grandchildRunID
	visited := []shared.UUID{current}
	for hops := 0; hops < 10; hops++ {
		row, ok := lt.findByRunID(current)
		if !ok {
			t.Fatalf("walk: missing row for %s at hop %d", current, hops)
		}
		var rec runtime.LeafRunRecord
		if err := json.Unmarshal(row.Record, &rec); err != nil {
			t.Fatalf("walk: unmarshal at hop %d: %v", hops, err)
		}
		if rec.ParentNodeRunID == "" {
			break
		}
		parent, err := uuid.Parse(rec.ParentNodeRunID)
		if err != nil {
			t.Fatalf("walk: bad parent_run_id %q at hop %d: %v", rec.ParentNodeRunID, hops, err)
		}
		current = shared.UUID(parent)
		visited = append(visited, current)
	}

	if len(visited) != 3 {
		t.Fatalf("walk: visited %d runs want 3 (chain root→child→grandchild)", len(visited))
	}
	if visited[0] != grandchildRunID {
		t.Errorf("walk start: got %s want grandchild %s", visited[0], grandchildRunID)
	}
	if visited[1] != childRunID {
		t.Errorf("walk hop 1: got %s want child %s", visited[1], childRunID)
	}
	if visited[2] != rootRunID {
		t.Errorf("walk hop 2: got %s want root %s", visited[2], rootRunID)
	}

	rows, err := lt.QueryByParentNodeRunID(ctx, rootRunID, 10)
	if err != nil {
		t.Fatalf("QueryByParentNodeRunID(root): %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("QueryByParentNodeRunID(root): got %d rows want 1 (the child)", len(rows))
	}
	rows, err = lt.QueryByParentNodeRunID(ctx, childRunID, 10)
	if err != nil {
		t.Fatalf("QueryByParentNodeRunID(child): %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("QueryByParentNodeRunID(child): got %d rows want 1 (the grandchild)", len(rows))
	}
}

type queryableLineage struct {
	fakeLineage
}

func (f *queryableLineage) QueryByParentNodeRunID(_ context.Context, parentNodeRunID shared.UUID, limit int) ([]persistence.LineageRow, error) {
	var out []persistence.LineageRow
	for _, r := range f.rows {
		if r.RecordKind != persistence.LineageRecordKindLeafRun {
			continue
		}
		var rec runtime.LeafRunRecord
		if err := json.Unmarshal(r.Record, &rec); err != nil {
			continue
		}
		if rec.ParentNodeRunID == parentNodeRunID.String() {
			out = append(out, r)
			if limit > 0 && len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

func (f *queryableLineage) findByRunID(runID shared.UUID) (persistence.LineageRow, bool) {
	for _, r := range f.rows {
		if r.RecordKind != persistence.LineageRecordKindLeafRun {
			continue
		}
		var rec runtime.LeafRunRecord
		if err := json.Unmarshal(r.Record, &rec); err != nil {
			continue
		}
		if rec.NodeRunID == runID {
			return r, true
		}
	}
	return persistence.LineageRow{}, false
}

func TestRecursiveAncestorWalk_ChainsSubstitutionRefs(t *testing.T) {
	t.Parallel()
	lt := &queryableLineage{}
	ctx := context.Background()
	frameID := shared.UUID(uuid.New())
	instanceID := shared.UUID(uuid.New())

	rootRunID := shared.UUID(uuid.New())
	childRunID := shared.UUID(uuid.New())
	grandchildRunID := shared.UUID(uuid.New())

	if err := runtime.WriteLeafRunLineage(ctx, lt, instanceID, frameID, time.Now().UTC(), runtime.LeafRunRecord{
		NodeRunID:          rootRunID,
		NodeID:             shared.UUID(uuid.New()),
		FrameID:            frameID,
		State:              "fresh",
		SettlingSignalType: "terminal/success",
	}, nil); err != nil {
		t.Fatalf("seed root: %v", err)
	}
	if err := runtime.WriteLeafRunLineage(ctx, lt, instanceID, frameID, time.Now().UTC(), runtime.LeafRunRecord{
		NodeRunID:          childRunID,
		NodeID:             shared.UUID(uuid.New()),
		FrameID:            frameID,
		State:              "fresh",
		SettlingSignalType: "terminal/success",
		SubstitutionRefs: []runtime.SubstitutionRef{
			{SourceKind: "run", SourceNodeAlias: "root", SourceVersionOrID: rootRunID.String()},
		},
	}, nil); err != nil {
		t.Fatalf("seed child: %v", err)
	}
	if err := runtime.WriteLeafRunLineage(ctx, lt, instanceID, frameID, time.Now().UTC(), runtime.LeafRunRecord{
		NodeRunID:          grandchildRunID,
		NodeID:             shared.UUID(uuid.New()),
		FrameID:            frameID,
		State:              "fresh",
		SettlingSignalType: "terminal/success",
		SubstitutionRefs: []runtime.SubstitutionRef{
			{SourceKind: "run", SourceNodeAlias: "child", SourceVersionOrID: childRunID.String()},
		},
	}, nil); err != nil {
		t.Fatalf("seed grandchild: %v", err)
	}

	current := grandchildRunID
	visited := []shared.UUID{current}
	for hops := 0; hops < 10; hops++ {
		row, ok := lt.findByRunID(current)
		if !ok {
			t.Fatalf("walk: missing row for %s at hop %d", current, hops)
		}
		var rec runtime.LeafRunRecord
		if err := json.Unmarshal(row.Record, &rec); err != nil {
			t.Fatalf("walk: unmarshal at hop %d: %v", hops, err)
		}
		var next shared.UUID
		for _, ref := range rec.SubstitutionRefs {
			if ref.SourceKind != "run" {
				continue
			}
			parent, err := uuid.Parse(ref.SourceVersionOrID)
			if err != nil {
				continue
			}
			next = shared.UUID(parent)
			break
		}
		if next == (shared.UUID{}) {
			break
		}
		current = next
		visited = append(visited, current)
	}

	if len(visited) != 3 {
		t.Fatalf("walk: visited %d runs want 3 (chain root→child→grandchild via substitution_refs)", len(visited))
	}
	if visited[0] != grandchildRunID || visited[1] != childRunID || visited[2] != rootRunID {
		t.Errorf("walk order: got %+v want [grandchild=%s, child=%s, root=%s]",
			visited, grandchildRunID, childRunID, rootRunID)
	}
}
