// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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

func TestLeafRunRecordCreation(t *testing.T) {
	t.Parallel()
	lt := &fakeLineage{}
	ctx := context.Background()
	frameID := shared.UUID(uuid.New())
	instanceID := shared.UUID(uuid.New())
	rec := runtime.LeafRunRecord{
		NodeRunID:          shared.UUID(uuid.New()),
		NodeID:             shared.UUID(uuid.New()),
		FrameID:            frameID,
		ChildKey:           "partition-7",
		State:              "fresh",
		SettlingSignalType: "terminal/success",
	}
	if err := runtime.WriteLeafRunLineage(ctx, nil, lt, instanceID, frameID, time.Now().UTC(), rec); err != nil {
		t.Fatalf("WriteLeafRunLineage: %v", err)
	}
	if len(lt.rows) != 1 {
		t.Fatalf("expected 1 lineage row, got %d", len(lt.rows))
	}
	row := lt.rows[0]
	if row.RecordKind != persistence.LineageRecordKindLeafRun {
		t.Errorf("record_kind: got %s want leaf_run", row.RecordKind)
	}
	if row.InstanceID != instanceID {
		t.Errorf("instance_id mismatch")
	}
	if row.FrameID != frameID {
		t.Errorf("frame_id mismatch")
	}
	var decoded runtime.LeafRunRecord
	if err := json.Unmarshal(row.Record, &decoded); err != nil {
		t.Fatalf("payload not JSON-decodable: %v", err)
	}
	if decoded.NodeRunID != rec.NodeRunID || decoded.ChildKey != "partition-7" || decoded.State != "fresh" {
		t.Errorf("payload roundtrip mismatch: %+v", decoded)
	}
}

func TestLeafRunRecordCreation_RequiresState(t *testing.T) {
	t.Parallel()
	lt := &fakeLineage{}
	rec := runtime.LeafRunRecord{
		NodeRunID: shared.UUID(uuid.New()),
		FrameID:   shared.UUID(uuid.New()),
	}
	err := runtime.WriteLeafRunLineage(context.Background(), nil, lt,
		shared.UUID(uuid.New()), rec.FrameID, time.Now().UTC(), rec)
	if err == nil {
		t.Fatal("expected error when state is empty")
	}
}
