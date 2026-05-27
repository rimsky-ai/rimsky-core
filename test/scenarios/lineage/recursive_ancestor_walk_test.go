// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// N7 scenario — recursive_ancestor_walk.
//
// Lineage rows form a parent_run_id chain across the run-tree;
// downstream tools recover ancestry by walking the chain via the
// `record->>'parent_run_id'` JSONB path. This scenario pins the walk
// behavior end-to-end: a multi-level chain (root → child → grandchild)
// produces rows whose `parent_run_id` values trace back to the
// originating root, and the walk terminates correctly at the root
// (parent_run_id empty / key absent).
package lineage

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/runtime"
)

// TestRecursiveAncestorWalk_ChainsParentRunID seeds a parent_run_id
// chain of depth 3 (root → child → grandchild) and walks the chain
// upward from grandchild via the in-memory queryable fake. The walk
// must:
//
//  1. Recover the immediate parent from grandchild's record (= child's run_id).
//  2. Recover the grandparent from child's record (= root's run_id).
//  3. Terminate at root (parent_run_id empty / JSON key absent).
//
// Without ParentRunID on the emitted record the walk would have
// nothing to chain through; the pre-2026-05-17 version of this test
// inserted N rows at the same frame and only asserted distinct ids,
// which never exercised the walk behavior that the file's docstring
// claimed.
func TestRecursiveAncestorWalk_ChainsParentRunID(t *testing.T) {
	t.Parallel()
	lt := &queryableLineage{}
	ctx := context.Background()
	frameID := shared.UUID(uuid.New())
	instanceID := shared.UUID(uuid.New())

	rootRunID := shared.UUID(uuid.New())
	childRunID := shared.UUID(uuid.New())
	grandchildRunID := shared.UUID(uuid.New())

	// Seed: root has no parent.
	if err := runtime.WriteLeafRunLineage(ctx, nil, lt, instanceID, frameID, time.Now().UTC(),
		runtime.LeafRunRecord{
			RunID:              rootRunID,
			NodeID:             shared.UUID(uuid.New()),
			FrameID:            frameID,
			State:              "fresh",
			SettlingSignalType: "terminal/success",
		}); err != nil {
		t.Fatalf("seed root: %v", err)
	}
	// child's parent is root.
	if err := runtime.WriteLeafRunLineage(ctx, nil, lt, instanceID, frameID, time.Now().UTC(),
		runtime.LeafRunRecord{
			RunID:              childRunID,
			NodeID:             shared.UUID(uuid.New()),
			FrameID:            frameID,
			State:              "fresh",
			SettlingSignalType: "terminal/success",
			ParentRunID:        rootRunID.String(),
		}); err != nil {
		t.Fatalf("seed child: %v", err)
	}
	// grandchild's parent is child.
	if err := runtime.WriteLeafRunLineage(ctx, nil, lt, instanceID, frameID, time.Now().UTC(),
		runtime.LeafRunRecord{
			RunID:              grandchildRunID,
			NodeID:             shared.UUID(uuid.New()),
			FrameID:            frameID,
			State:              "fresh",
			SettlingSignalType: "terminal/success",
			ParentRunID:        childRunID.String(),
		}); err != nil {
		t.Fatalf("seed grandchild: %v", err)
	}

	// Walk upward from grandchild via the parent_run_id chain.
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
		if rec.ParentRunID == "" {
			// Reached the root — terminate.
			break
		}
		parent, err := uuid.Parse(rec.ParentRunID)
		if err != nil {
			t.Fatalf("walk: bad parent_run_id %q at hop %d: %v", rec.ParentRunID, hops, err)
		}
		current = shared.UUID(parent)
		visited = append(visited, current)
	}

	// The walk must have produced [grandchild, child, root].
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

	// QueryByParentRunID exercises the JSONB predicate path the
	// postgres + sqlite drivers both implement. The in-memory fake
	// re-parses JSON in-process — mirroring the predicate — so the
	// pin is "ParentRunID on the record persists into a queryable
	// shape." Cross-driver coverage of the SQL predicate itself lives
	// in the conformance suite (`testLineageQueryByParentRunID`).
	rows, err := lt.QueryByParentRunID(ctx, rootRunID, 10)
	if err != nil {
		t.Fatalf("QueryByParentRunID(root): %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("QueryByParentRunID(root): got %d rows want 1 (the child)", len(rows))
	}
	rows, err = lt.QueryByParentRunID(ctx, childRunID, 10)
	if err != nil {
		t.Fatalf("QueryByParentRunID(child): %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("QueryByParentRunID(child): got %d rows want 1 (the grandchild)", len(rows))
	}
}

// queryableLineage extends fakeLineage with a JSON-aware
// QueryByParentRunID + findByRunID lookup used by the walk test.
// Mirrors the postgres `record->>'parent_run_id' = $1` predicate.
type queryableLineage struct {
	fakeLineage
}

func (f *queryableLineage) QueryByParentRunID(_ context.Context, parentRunID shared.UUID, limit int) ([]persistence.LineageRow, error) {
	var out []persistence.LineageRow
	for _, r := range f.rows {
		if r.RecordKind != persistence.LineageRecordKindLeafRun {
			continue
		}
		var rec runtime.LeafRunRecord
		if err := json.Unmarshal(r.Record, &rec); err != nil {
			continue
		}
		if rec.ParentRunID == parentRunID.String() {
			out = append(out, r)
			if limit > 0 && len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

// findByRunID looks up a leaf_run row by its record.run_id (not the
// row id; the row id is the lineage row PK, the run_id is the
// rimsky_node_runs id).
func (f *queryableLineage) findByRunID(runID shared.UUID) (persistence.LineageRow, bool) {
	for _, r := range f.rows {
		if r.RecordKind != persistence.LineageRecordKindLeafRun {
			continue
		}
		var rec runtime.LeafRunRecord
		if err := json.Unmarshal(r.Record, &rec); err != nil {
			continue
		}
		if rec.RunID == runID {
			return r, true
		}
	}
	return persistence.LineageRow{}, false
}

// TestRecursiveAncestorWalk_ChainsSubstitutionRefs pins the cycle-6
// fix: the ancestor walker resolves upstream lineage rows by reading
// `substitution_refs` entries with `source_kind: "run"` and parsing
// the `source_version_or_id` as a UUID. Without this wiring,
// `GET /lineage/runs/{seed}/ancestors` returns empty for every seed
// because the writer never populated SubstitutionRefs.
//
// Seeds a chain (root → child → grandchild) where each child cites
// its upstream as `source_kind: "run"`; verifies the walker recovers
// the full chain.
func TestRecursiveAncestorWalk_ChainsSubstitutionRefs(t *testing.T) {
	t.Parallel()
	lt := &queryableLineage{}
	ctx := context.Background()
	frameID := shared.UUID(uuid.New())
	instanceID := shared.UUID(uuid.New())

	rootRunID := shared.UUID(uuid.New())
	childRunID := shared.UUID(uuid.New())
	grandchildRunID := shared.UUID(uuid.New())

	// Seed: root has no substitution_refs.
	if err := runtime.WriteLeafRunLineage(ctx, nil, lt, instanceID, frameID, time.Now().UTC(),
		runtime.LeafRunRecord{
			RunID:              rootRunID,
			NodeID:             shared.UUID(uuid.New()),
			FrameID:            frameID,
			State:              "fresh",
			SettlingSignalType: "terminal/success",
		}); err != nil {
		t.Fatalf("seed root: %v", err)
	}
	// child cites root via substitution_refs.
	if err := runtime.WriteLeafRunLineage(ctx, nil, lt, instanceID, frameID, time.Now().UTC(),
		runtime.LeafRunRecord{
			RunID:              childRunID,
			NodeID:             shared.UUID(uuid.New()),
			FrameID:            frameID,
			State:              "fresh",
			SettlingSignalType: "terminal/success",
			SubstitutionRefs: []runtime.SubstitutionRef{
				{SourceKind: "run", SourceNodeAlias: "root", SourceVersionOrID: rootRunID.String()},
			},
		}); err != nil {
		t.Fatalf("seed child: %v", err)
	}
	// grandchild cites child via substitution_refs.
	if err := runtime.WriteLeafRunLineage(ctx, nil, lt, instanceID, frameID, time.Now().UTC(),
		runtime.LeafRunRecord{
			RunID:              grandchildRunID,
			NodeID:             shared.UUID(uuid.New()),
			FrameID:            frameID,
			State:              "fresh",
			SettlingSignalType: "terminal/success",
			SubstitutionRefs: []runtime.SubstitutionRef{
				{SourceKind: "run", SourceNodeAlias: "child", SourceVersionOrID: childRunID.String()},
			},
		}); err != nil {
		t.Fatalf("seed grandchild: %v", err)
	}

	// Walk upward from grandchild by reading substitution_refs.
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
