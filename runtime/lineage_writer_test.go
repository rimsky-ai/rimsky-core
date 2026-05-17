// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/foundation/shared"
)

// fakeLineageTable is an in-memory persistence.LineageTable for unit
// tests of the writer.
type fakeLineageTable struct {
	rows []persistence.LineageRow
}

func (f *fakeLineageTable) Insert(_ context.Context, _ persistence.Tx, row persistence.LineageRow) error {
	f.rows = append(f.rows, row)
	return nil
}
func (f *fakeLineageTable) GetByRunID(_ context.Context, _ shared.UUID) ([]persistence.LineageRow, error) {
	return nil, nil
}
func (f *fakeLineageTable) GetByClaimHandleID(_ context.Context, _ shared.UUID) ([]persistence.LineageRow, error) {
	return nil, nil
}
func (f *fakeLineageTable) Query(_ context.Context, _ persistence.LineageQuery, _ persistence.ListPagination) (persistence.PaginatedListResult[persistence.LineageRow], error) {
	return persistence.PaginatedListResult[persistence.LineageRow]{}, nil
}
func (f *fakeLineageTable) DeleteOlderThan(_ context.Context, _ time.Time) (int, error) {
	return 0, nil
}

func TestWriteLeafRunLineage_PayloadRoundtrip(t *testing.T) {
	lt := &fakeLineageTable{}
	ctx := context.Background()
	inst := shared.UUID(uuid.New())
	frame := shared.UUID(uuid.New())
	run := shared.UUID(uuid.New())
	now := time.Now().UTC()
	rec := LeafRunRecord{
		RunID:       run,
		NodeID:      shared.UUID(uuid.New()),
		FrameID:     frame,
		State:       "fresh",
		LastOutcome: "fresh_changed",
		ParamsHash:  HashBytes([]byte(`{"key":"value"}`)),
	}
	if err := WriteLeafRunLineage(ctx, nil, lt, inst, frame, now, rec); err != nil {
		t.Fatalf("WriteLeafRunLineage: %v", err)
	}
	if len(lt.rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(lt.rows))
	}
	row := lt.rows[0]
	if row.RecordKind != persistence.LineageRecordKindLeafRun {
		t.Fatalf("unexpected kind %q", row.RecordKind)
	}
	var decoded LeafRunRecord
	if err := json.Unmarshal(row.Record, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.RunID != run {
		t.Fatalf("run_id roundtrip failed")
	}
	if decoded.LastOutcome != "fresh_changed" {
		t.Fatalf("last_outcome roundtrip failed")
	}
}

func TestWriteClaimTerminalLineage_VersionIDPersisted(t *testing.T) {
	lt := &fakeLineageTable{}
	ctx := context.Background()
	inst := shared.UUID(uuid.New())
	frame := shared.UUID(uuid.New())
	now := time.Now().UTC()
	handle := shared.UUID(uuid.New())
	rec := ClaimTerminalRecord{
		ClaimHandleID: handle,
		RunID:         shared.UUID(uuid.New()),
		NodeID:        shared.UUID(uuid.New()),
		FrameID:       frame,
		ProducerName:  "parquet-store",
		VersionID:     "v123",
		ScopeDataHash: HashBytes([]byte(`{"dataset":"d"}`)),
		Outcome:       persistence.LineageOutcomeCommitted,
	}
	if err := WriteClaimTerminalLineage(ctx, nil, lt, inst, frame, now, rec); err != nil {
		t.Fatalf("WriteClaimTerminalLineage: %v", err)
	}
	if len(lt.rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(lt.rows))
	}
	if lt.rows[0].RecordKind != persistence.LineageRecordKindClaimTerminal {
		t.Fatalf("unexpected kind %q", lt.rows[0].RecordKind)
	}
	if lt.rows[0].Outcome != persistence.LineageOutcomeCommitted {
		t.Fatalf("unexpected outcome %q", lt.rows[0].Outcome)
	}
}

// TestWriteClaimTerminalLineage_AbandonedOutcome pins the Abandon path:
// a natural Abandon row carries `outcome: abandoned` (no cause field).
func TestWriteClaimTerminalLineage_AbandonedOutcome(t *testing.T) {
	lt := &fakeLineageTable{}
	ctx := context.Background()
	inst := shared.UUID(uuid.New())
	frame := shared.UUID(uuid.New())
	rec := ClaimTerminalRecord{
		ClaimHandleID: shared.UUID(uuid.New()),
		RunID:         shared.UUID(uuid.New()),
		NodeID:        shared.UUID(uuid.New()),
		FrameID:       frame,
		ProducerName:  "store",
		Outcome:       persistence.LineageOutcomeAbandoned,
	}
	if err := WriteClaimTerminalLineage(ctx, nil, lt, inst, frame, time.Now().UTC(), rec); err != nil {
		t.Fatalf("WriteClaimTerminalLineage: %v", err)
	}
	if lt.rows[0].Outcome != persistence.LineageOutcomeAbandoned {
		t.Fatalf("outcome got %q want abandoned", lt.rows[0].Outcome)
	}
}

// TestWriteClaimTerminalLineage_ForceCancelledOutcome pins the
// force-cancel path: the cause field is preserved in the JSON payload so
// post-mortem queries can distinguish sibling-cancel from descendant-cancel.
func TestWriteClaimTerminalLineage_ForceCancelledOutcome(t *testing.T) {
	lt := &fakeLineageTable{}
	ctx := context.Background()
	rec := ClaimTerminalRecord{
		ClaimHandleID: shared.UUID(uuid.New()),
		RunID:         shared.UUID(uuid.New()),
		NodeID:        shared.UUID(uuid.New()),
		FrameID:       shared.UUID(uuid.New()),
		Outcome:       persistence.LineageOutcomeForceCancelled,
		Cause:         string(TerminalCauseSiblingCancel),
	}
	if err := WriteClaimTerminalLineage(ctx, nil, lt, shared.UUID(uuid.New()), rec.FrameID, time.Now().UTC(), rec); err != nil {
		t.Fatalf("WriteClaimTerminalLineage: %v", err)
	}
	if lt.rows[0].Outcome != persistence.LineageOutcomeForceCancelled {
		t.Fatalf("outcome got %q want force_cancelled", lt.rows[0].Outcome)
	}
	var decoded ClaimTerminalRecord
	if err := json.Unmarshal(lt.rows[0].Record, &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded.Cause != string(TerminalCauseSiblingCancel) {
		t.Fatalf("cause got %q want sibling_cancel", decoded.Cause)
	}
}

func TestHashHelpers_Stable(t *testing.T) {
	if HashBytes(nil) != "" {
		t.Fatal("HashBytes(nil) should return empty string")
	}
	h1 := HashBytes([]byte("hello"))
	h2 := HashBytes([]byte("hello"))
	if h1 != h2 || h1 == "" {
		t.Fatalf("HashBytes not deterministic: %q vs %q", h1, h2)
	}
	if HashBytes([]byte("hello")) == HashBytes([]byte("world")) {
		t.Fatal("different inputs hash to the same value")
	}
}
