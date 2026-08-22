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
		TerminalKind:       runtime.LeafRunTerminalKindComplete,
	}
	if err := runtime.WriteLeafRunLineage(ctx, lt, instanceID, frameID, time.Now().UTC(), rec, nil); err != nil {
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

// @decision: lineage-records-computation-only
func TestLeafRunRecordCreation_RefusesATerminalKindOutsideTheClosedFamily(t *testing.T) {
	t.Parallel()
	lt := &fakeLineage{}
	for _, kind := range []string{"", "pure_cascade", "handler_pass"} {
		rec := runtime.LeafRunRecord{
			NodeRunID:          shared.UUID(uuid.New()),
			NodeID:             shared.UUID(uuid.New()),
			FrameID:            shared.UUID(uuid.New()),
			State:              "fresh",
			SettlingSignalType: "terminal/success",
			TerminalKind:       kind,
		}
		err := runtime.WriteLeafRunLineage(context.Background(), lt, shared.UUID(uuid.New()), rec.FrameID, time.Now().UTC(), rec, nil)
		if err == nil {
			t.Fatalf("terminal_kind %q must be refused: the family is complete | errored | park | subgraph_call", kind)
		}
	}
	for _, kind := range []string{
		runtime.LeafRunTerminalKindComplete,
		runtime.LeafRunTerminalKindErrored,
		runtime.LeafRunTerminalKindPark,
		runtime.LeafRunTerminalKindSubgraphCall,
	} {
		rec := runtime.LeafRunRecord{
			NodeRunID:          shared.UUID(uuid.New()),
			NodeID:             shared.UUID(uuid.New()),
			FrameID:            shared.UUID(uuid.New()),
			State:              "fresh",
			SettlingSignalType: "terminal/success",
			TerminalKind:       kind,
		}
		if err := runtime.WriteLeafRunLineage(context.Background(), lt, shared.UUID(uuid.New()), rec.FrameID, time.Now().UTC(), rec, nil); err != nil {
			t.Fatalf("terminal_kind %q is a member of the family and must be accepted: %v", kind, err)
		}
	}
	if len(lt.rows) != 4 {
		t.Fatalf("expected the four accepted kinds to write four rows and the refused kinds none, got %d", len(lt.rows))
	}
}

func TestLeafRunRecordCreation_RequiresState(t *testing.T) {
	t.Parallel()
	lt := &fakeLineage{}
	rec := runtime.LeafRunRecord{
		NodeRunID: shared.UUID(uuid.New()),
		FrameID:   shared.UUID(uuid.New()),
	}
	err := runtime.WriteLeafRunLineage(context.Background(), lt, shared.UUID(uuid.New()), rec.FrameID, time.Now().UTC(), rec, nil)
	if err == nil {
		t.Fatal("expected error when state is empty")
	}
}
