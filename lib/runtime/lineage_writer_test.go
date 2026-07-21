// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
)

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
func (f *fakeLineageTable) QueryByParentNodeRunID(_ context.Context, _ shared.UUID, _ int) ([]persistence.LineageRow, error) {
	return nil, nil
}
func (f *fakeLineageTable) DeleteOlderThan(_ context.Context, _ time.Time) (int, error) {
	return 0, nil
}
func (f *fakeLineageTable) CountOlderThan(_ context.Context, _ time.Time) (int, error) {
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
		NodeRunID:          run,
		NodeID:             shared.UUID(uuid.New()),
		FrameID:            frame,
		State:              "fresh",
		SettlingSignalType: "terminal/success",
		ParamsSnapshotHash: HashBytes([]byte(`{"key":"value"}`)),
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
	if decoded.NodeRunID != run {
		t.Fatalf("run_id roundtrip failed")
	}
	if decoded.SettlingSignalType != "terminal/success" {
		t.Fatalf("settling_signal_type roundtrip failed")
	}
}

func TestWriteLeafRunLineage_RejectsEmptySettlingSignalType(t *testing.T) {
	lt := &fakeLineageTable{}
	ctx := context.Background()
	inst := shared.UUID(uuid.New())
	frame := shared.UUID(uuid.New())
	now := time.Now().UTC()
	rec := LeafRunRecord{
		NodeRunID: shared.UUID(uuid.New()),
		NodeID:    shared.UUID(uuid.New()),
		FrameID:   frame,
		State:     "fresh",
	}
	err := WriteLeafRunLineage(ctx, nil, lt, inst, frame, now, rec)
	if err == nil {
		t.Fatal("WriteLeafRunLineage: expected an error for empty SettlingSignalType, got nil")
	}
	if !strings.Contains(err.Error(), "settling_signal_type") {
		t.Fatalf("WriteLeafRunLineage error = %q, want it to name settling_signal_type", err.Error())
	}
	if len(lt.rows) != 0 {
		t.Fatalf("expected no row written on validation failure, got %d", len(lt.rows))
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
		ClaimHandleID:      handle,
		NodeRunID:          shared.UUID(uuid.New()),
		NodeID:             shared.UUID(uuid.New()),
		FrameID:            frame,
		ProducerName:       "parquet-store",
		VersionID:          "v123",
		ClaimScopeDataHash: HashBytes([]byte(`{"dataset":"d"}`)),
		Outcome:            persistence.LineageOutcomeCommitted,
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

func TestWriteClaimTerminalLineage_AbandonedOutcome(t *testing.T) {
	lt := &fakeLineageTable{}
	ctx := context.Background()
	inst := shared.UUID(uuid.New())
	frame := shared.UUID(uuid.New())
	rec := ClaimTerminalRecord{
		ClaimHandleID: shared.UUID(uuid.New()),
		NodeRunID:     shared.UUID(uuid.New()),
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

func TestWriteClaimTerminalLineage_ForceCancelledOutcome(t *testing.T) {
	lt := &fakeLineageTable{}
	ctx := context.Background()
	rec := ClaimTerminalRecord{
		ClaimHandleID: shared.UUID(uuid.New()),
		NodeRunID:     shared.UUID(uuid.New()),
		NodeID:        shared.UUID(uuid.New()),
		FrameID:       shared.UUID(uuid.New()),
		Outcome:       persistence.LineageOutcomeForceCancelled,
		Cause:         "sibling_cancel",
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
	if decoded.Cause != "sibling_cancel" {
		t.Fatalf("cause got %q want sibling_cancel", decoded.Cause)
	}
}

func TestWriteClaimTerminalLineage_EmptyOutcomeRejected(t *testing.T) {
	lt := &fakeLineageTable{}
	rec := ClaimTerminalRecord{
		ClaimHandleID: shared.UUID(uuid.New()),
		NodeRunID:     shared.UUID(uuid.New()),
		NodeID:        shared.UUID(uuid.New()),
		FrameID:       shared.UUID(uuid.New()),
		ProducerName:  "store",
	}
	err := WriteClaimTerminalLineage(context.Background(), nil, lt,
		shared.UUID(uuid.New()), rec.FrameID, time.Now().UTC(), rec)
	if err == nil {
		t.Fatal("expected error when Outcome is empty; got nil")
	}
	if len(lt.rows) != 0 {
		t.Fatalf("expected 0 rows inserted, got %d", len(lt.rows))
	}
}

func TestWriteLeafRunLineage_ParentRunIDPersistedAndQueryable(t *testing.T) {
	lt := &queryableFakeLineageTable{}
	ctx := context.Background()
	inst := shared.UUID(uuid.New())
	frame := shared.UUID(uuid.New())
	parent := shared.UUID(uuid.New())
	child := shared.UUID(uuid.New())
	rec := LeafRunRecord{
		NodeRunID:          child,
		NodeID:             shared.UUID(uuid.New()),
		FrameID:            frame,
		State:              "fresh",
		SettlingSignalType: "terminal/success",
		ParentNodeRunID:    parent.String(),
	}
	if err := WriteLeafRunLineage(ctx, nil, lt, inst, frame, time.Now().UTC(), rec); err != nil {
		t.Fatalf("WriteLeafRunLineage: %v", err)
	}
	rows, err := lt.QueryByParentNodeRunID(ctx, parent, 10)
	if err != nil {
		t.Fatalf("QueryByParentNodeRunID: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	var decoded LeafRunRecord
	if err := json.Unmarshal(rows[0].Record, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.ParentNodeRunID != parent.String() {
		t.Fatalf("parent_run_id round-trip failed: got %q want %q",
			decoded.ParentNodeRunID, parent.String())
	}
	if decoded.NodeRunID != child {
		t.Fatalf("run_id round-trip failed")
	}
}

type queryableFakeLineageTable struct {
	fakeLineageTable
}

func (f *queryableFakeLineageTable) Query(
	_ context.Context, q persistence.LineageQuery, pag persistence.ListPagination,
) (persistence.PaginatedListResult[persistence.LineageRow], error) {
	var out []persistence.LineageRow
	for i := len(f.rows) - 1; i >= 0; i-- {
		r := f.rows[i]
		if q.Kind != "" && r.RecordKind != q.Kind {
			continue
		}
		if q.InstanceID != nil && r.InstanceID != *q.InstanceID {
			continue
		}
		out = append(out, r)
		if pag.Limit > 0 && len(out) >= pag.Limit {
			break
		}
	}
	return persistence.PaginatedListResult[persistence.LineageRow]{Rows: out}, nil
}

func (f *queryableFakeLineageTable) QueryByParentNodeRunID(_ context.Context, parentNodeRunID shared.UUID, limit int) ([]persistence.LineageRow, error) {
	var out []persistence.LineageRow
	for _, r := range f.rows {
		if r.RecordKind != persistence.LineageRecordKindLeafRun {
			continue
		}
		var rec LeafRunRecord
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

func seedLeafRunForMostRecentLookup(
	t *testing.T, lt *queryableFakeLineageTable, instanceID, nodeID, nodeRunID shared.UUID,
) {
	t.Helper()
	rec := LeafRunRecord{
		NodeRunID:          nodeRunID,
		NodeID:             nodeID,
		FrameID:            shared.UUID(uuid.New()),
		State:              "fresh",
		SettlingSignalType: "terminal/success",
	}
	if err := WriteLeafRunLineage(context.Background(), nil, lt, instanceID, rec.FrameID, time.Now().UTC(), rec); err != nil {
		t.Fatalf("seedLeafRunForMostRecentLookup: %v", err)
	}
}

func TestMostRecentRunIDForNode_ReturnsNewestNotOldest(t *testing.T) {
	lt := &queryableFakeLineageTable{}
	ctx := context.Background()
	inst := shared.UUID(uuid.New())
	upstreamNode := shared.UUID(uuid.New())

	oldRun := shared.UUID(uuid.New())
	newRun := shared.UUID(uuid.New())
	seedLeafRunForMostRecentLookup(t, lt, inst, upstreamNode, oldRun)
	seedLeafRunForMostRecentLookup(t, lt, inst, upstreamNode, newRun)

	got := mostRecentRunIDForNode(ctx, lt, inst, upstreamNode)
	if got != newRun {
		t.Fatalf("mostRecentRunIDForNode = %s, want the most recently observed run %s (got the oldest, %s, instead)",
			got, newRun, oldRun)
	}
}

func TestMostRecentRunIDForNode_ScopesByInstance(t *testing.T) {
	lt := &queryableFakeLineageTable{}
	ctx := context.Background()
	instA := shared.UUID(uuid.New())
	instB := shared.UUID(uuid.New())
	upstreamNode := shared.UUID(uuid.New())

	runA := shared.UUID(uuid.New())
	runB := shared.UUID(uuid.New())
	seedLeafRunForMostRecentLookup(t, lt, instA, upstreamNode, runA)
	seedLeafRunForMostRecentLookup(t, lt, instB, upstreamNode, runB)

	got := mostRecentRunIDForNode(ctx, lt, instA, upstreamNode)
	if got != runA {
		t.Fatalf("mostRecentRunIDForNode(instA) = %s, want %s — a sibling instance's leaf run must not leak across instances",
			got, runA)
	}
}

type emitFakePersist struct {
	stubTables
	lt     persistence.LineageTable
	frames persistence.FrameTable
}

func (f *emitFakePersist) Frames() persistence.FrameTable    { return f.frames }
func (f *emitFakePersist) Lineage() persistence.LineageTable { return f.lt }

func TestEmitLeafRunLineage_OmitsEmptyParentRunID(t *testing.T) {
	ctx := context.Background()
	inst := shared.UUID(uuid.New())
	frame := shared.UUID(uuid.New())

	t.Run("root-run", func(t *testing.T) {
		lt := &fakeLineageTable{}
		args := RunArgs{
			Persist: &emitFakePersist{lt: lt},
			Clock:   shared.SystemClock{},
		}
		EmitLeafRunLineage(ctx, args, LeafRunEmitInput{
			InstanceID:         inst,
			FrameID:            frame,
			NodeRunID:          shared.UUID(uuid.New()),
			NodeID:             shared.UUID(uuid.New()),
			State:              string(cascade.NodeStateFresh),
			SettlingSignalType: "terminal/success",
			ParentNodeRunID:    nil,
		})
		if len(lt.rows) != 1 {
			t.Fatalf("expected 1 row, got %d", len(lt.rows))
		}
		var rec LeafRunRecord
		if err := json.Unmarshal(lt.rows[0].Record, &rec); err != nil {
			t.Fatalf("unmarshal typed: %v", err)
		}
		if rec.ParentNodeRunID != "" {
			t.Fatalf("root run: ParentNodeRunID got %q want \"\"", rec.ParentNodeRunID)
		}
		var raw map[string]any
		if err := json.Unmarshal(lt.rows[0].Record, &raw); err != nil {
			t.Fatalf("unmarshal raw: %v", err)
		}
		if _, ok := raw["parent_run_id"]; ok {
			t.Fatalf("root run should omit parent_run_id; payload was %s", string(lt.rows[0].Record))
		}
	})

	t.Run("child-run", func(t *testing.T) {
		lt := &fakeLineageTable{}
		args := RunArgs{
			Persist: &emitFakePersist{lt: lt},
			Clock:   shared.SystemClock{},
		}
		parent := shared.UUID(uuid.New())
		EmitLeafRunLineage(ctx, args, LeafRunEmitInput{
			InstanceID:         inst,
			FrameID:            frame,
			NodeRunID:          shared.UUID(uuid.New()),
			NodeID:             shared.UUID(uuid.New()),
			State:              string(cascade.NodeStateFresh),
			SettlingSignalType: "terminal/success",
			ParentNodeRunID:    &parent,
		})
		if len(lt.rows) != 1 {
			t.Fatalf("expected 1 row, got %d", len(lt.rows))
		}
		var rec LeafRunRecord
		if err := json.Unmarshal(lt.rows[0].Record, &rec); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if rec.ParentNodeRunID != parent.String() {
			t.Fatalf("child run: ParentNodeRunID got %q want %q",
				rec.ParentNodeRunID, parent.String())
		}
	})
}

func TestEmitLeafRunLineage_TemplateNodeAliasDistinctFromNodeAlias(t *testing.T) {
	ctx := context.Background()
	inst := shared.UUID(uuid.New())
	frame := shared.UUID(uuid.New())

	t.Run("explicit_template_alias_preserved", func(t *testing.T) {
		lt := &fakeLineageTable{}
		args := RunArgs{Persist: &emitFakePersist{lt: lt}, Clock: shared.SystemClock{}}
		EmitLeafRunLineage(ctx, args, LeafRunEmitInput{
			InstanceID:         inst,
			FrameID:            frame,
			NodeRunID:          shared.UUID(uuid.New()),
			NodeID:             shared.UUID(uuid.New()),
			State:              string(cascade.NodeStateFresh),
			SettlingSignalType: "terminal/success",
			NodeAlias:          "runtime-alias",
			TemplateNodeAlias:  "template-alias",
		})
		var rec LeafRunRecord
		if err := json.Unmarshal(lt.rows[0].Record, &rec); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if rec.NodeAlias != "runtime-alias" {
			t.Fatalf("NodeAlias got %q want %q", rec.NodeAlias, "runtime-alias")
		}
		if rec.TemplateNodeAlias != "template-alias" {
			t.Fatalf("TemplateNodeAlias got %q want %q — distinct from NodeAlias, not collapsed into it",
				rec.TemplateNodeAlias, "template-alias")
		}
	})

	t.Run("unset_template_alias_falls_back_to_node_alias", func(t *testing.T) {
		lt := &fakeLineageTable{}
		args := RunArgs{Persist: &emitFakePersist{lt: lt}, Clock: shared.SystemClock{}}
		EmitLeafRunLineage(ctx, args, LeafRunEmitInput{
			InstanceID:         inst,
			FrameID:            frame,
			NodeRunID:          shared.UUID(uuid.New()),
			NodeID:             shared.UUID(uuid.New()),
			State:              string(cascade.NodeStateFresh),
			SettlingSignalType: "terminal/success",
			NodeAlias:          "only-alias",
		})
		var rec LeafRunRecord
		if err := json.Unmarshal(lt.rows[0].Record, &rec); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if rec.TemplateNodeAlias != "only-alias" {
			t.Fatalf("TemplateNodeAlias got %q want fallback %q", rec.TemplateNodeAlias, "only-alias")
		}
	})
}

type fakeFrameTableWithMessage struct {
	row *persistence.FrameRowWithMessage
}

func (f *fakeFrameTableWithMessage) ListRunningFramesNoPendingNodes(context.Context, persistence.Tx) ([]persistence.FramePending, error) {
	return nil, nil
}
func (f *fakeFrameTableWithMessage) HasFailedNode(context.Context, shared.UUID, shared.UUID, persistence.Tx) (bool, error) {
	return false, nil
}
func (f *fakeFrameTableWithMessage) MarkFrameEnded(context.Context, shared.UUID, persistence.Tx) (bool, error) {
	return false, nil
}
func (f *fakeFrameTableWithMessage) MarkOpenFramesEndedForInstance(context.Context, shared.UUID, persistence.Tx) (int, error) {
	return 0, nil
}
func (f *fakeFrameTableWithMessage) EndFrameIfSettled(context.Context, shared.UUID, persistence.Tx) (persistence.FrameEndResult, error) {
	return persistence.FrameEndResult{}, nil
}
func (f *fakeFrameTableWithMessage) GetRunningFrameID(context.Context, shared.UUID, persistence.Tx) (*shared.UUID, error) {
	return nil, nil
}
func (f *fakeFrameTableWithMessage) MarkSourceNodeStale(context.Context, shared.UUID, shared.UUID, shared.UUID, persistence.Tx) (bool, error) {
	return false, nil
}
func (f *fakeFrameTableWithMessage) ListOrphanFrameDispatches(context.Context, persistence.Tx) ([]persistence.OrphanFrameDispatch, error) {
	return nil, nil
}
func (f *fakeFrameTableWithMessage) InsertRunningFrame(context.Context, shared.UUID, shared.UUID, shared.UUID, persistence.Tx) (shared.UUID, error) {
	return shared.UUID{}, nil
}
func (f *fakeFrameTableWithMessage) ListForObservabilityWithMessage(context.Context, persistence.FrameListFilter, persistence.ListPagination, persistence.Tx) (persistence.PaginatedListResult[persistence.FrameRowWithMessage], error) {
	return persistence.PaginatedListResult[persistence.FrameRowWithMessage]{}, nil
}
func (f *fakeFrameTableWithMessage) GetForObservabilityWithMessage(_ context.Context, frameID shared.UUID, _ persistence.Tx) (*persistence.FrameRowWithMessage, error) {
	if f.row == nil || f.row.FrameID != frameID {
		return nil, nil
	}
	return f.row, nil
}
func (f *fakeFrameTableWithMessage) ListForObservability(context.Context, persistence.FrameListFilter, persistence.ListPagination, persistence.Tx) (persistence.PaginatedListResult[persistence.FrameRow], error) {
	return persistence.PaginatedListResult[persistence.FrameRow]{}, nil
}
func (f *fakeFrameTableWithMessage) GetForObservability(context.Context, shared.UUID, persistence.Tx) (*persistence.FrameRow, error) {
	return nil, nil
}
func (f *fakeFrameTableWithMessage) CountHeldFrames(context.Context, persistence.Tx) (int, error) {
	return 0, nil
}
func (f *fakeFrameTableWithMessage) PruneTraceForRetention(context.Context, int, time.Time) (int, error) {
	return 0, nil
}

func TestEmitLeafRunLineage_PopulatesFrameTriggerFields(t *testing.T) {
	ctx := context.Background()
	inst := shared.UUID(uuid.New())
	frame := shared.UUID(uuid.New())
	triggeringMessageID := shared.UUID(uuid.New())

	lt := &fakeLineageTable{}
	frames := &fakeFrameTableWithMessage{
		row: &persistence.FrameRowWithMessage{
			FrameRow: persistence.FrameRow{
				FrameID:             frame,
				InstanceID:          inst,
				TriggeringMessageID: triggeringMessageID,
			},
			MessageSenderKind: "operator",
		},
	}
	args := RunArgs{
		Persist: &emitFakePersist{lt: lt, frames: frames},
		Clock:   shared.SystemClock{},
	}
	EmitLeafRunLineage(ctx, args, LeafRunEmitInput{
		InstanceID:         inst,
		FrameID:            frame,
		NodeRunID:          shared.UUID(uuid.New()),
		NodeID:             shared.UUID(uuid.New()),
		State:              string(cascade.NodeStateFresh),
		SettlingSignalType: "terminal/success",
	})
	if len(lt.rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(lt.rows))
	}
	var rec LeafRunRecord
	if err := json.Unmarshal(lt.rows[0].Record, &rec); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if rec.FrameTriggerKind != "operator" {
		t.Fatalf("FrameTriggerKind got %q want %q", rec.FrameTriggerKind, "operator")
	}
	if rec.TriggerMessageID != triggeringMessageID.String() {
		t.Fatalf("TriggerMessageID got %q want %q", rec.TriggerMessageID, triggeringMessageID.String())
	}
}

func TestEmitLeafRunLineage_FrameLookupUnavailableOmitsTriggerFields(t *testing.T) {
	ctx := context.Background()
	inst := shared.UUID(uuid.New())
	frame := shared.UUID(uuid.New())

	lt := &fakeLineageTable{}
	args := RunArgs{
		Persist: &emitFakePersist{lt: lt, frames: nil},
		Clock:   shared.SystemClock{},
	}
	EmitLeafRunLineage(ctx, args, LeafRunEmitInput{
		InstanceID:         inst,
		FrameID:            frame,
		NodeRunID:          shared.UUID(uuid.New()),
		NodeID:             shared.UUID(uuid.New()),
		State:              string(cascade.NodeStateFresh),
		SettlingSignalType: "terminal/success",
	})
	if len(lt.rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(lt.rows))
	}
	var raw map[string]any
	if err := json.Unmarshal(lt.rows[0].Record, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := raw["frame_trigger_kind"]; ok {
		t.Fatalf("expected frame_trigger_kind omitted when frame lookup is unavailable; payload was %s",
			string(lt.rows[0].Record))
	}
	if _, ok := raw["trigger_message_id"]; ok {
		t.Fatalf("expected trigger_message_id omitted when frame lookup is unavailable; payload was %s",
			string(lt.rows[0].Record))
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

func TestHeldClaimsForLineage_HeldAliasesInDeterministicOrder(t *testing.T) {
	acq := &acquisition{
		HeldClaims: map[string]claimproducer.ClaimResult{
			"zeta":  {},
			"alpha": {},
			"mid":   {},
		},
	}

	for i := 0; i < 20; i++ {
		got := HeldClaimsForLineage(acq)
		if len(got) != 3 {
			t.Fatalf("iteration %d: got %d entries, want 3", i, len(got))
		}
		wantOrder := []string{"held:alpha", "held:mid", "held:zeta"}
		for j, want := range wantOrder {
			if got[j].Role != want {
				t.Fatalf("iteration %d: entry[%d].Role=%q want %q (held aliases must be sorted for deterministic lineage records)", i, j, got[j].Role, want)
			}
		}
	}
}

func TestLeafRunRecord_TagDisciplineAndOrder(t *testing.T) {
	t.Parallel()
	want := []struct {
		field     string
		jsonTag   string
		omitempty bool
	}{
		{"NodeRunID", "run_id", false},
		{"NodeID", "node_id", false},
		{"FrameID", "frame_id", false},
		{"ChildKey", "child_key", true},
		{"NodeAlias", "node_alias", true},
		{"ParentNodeRunID", "parent_run_id", true},
		{"FrameTriggerKind", "frame_trigger_kind", true},
		{"TriggerMessageID", "trigger_message_id", true},
		{"HeldClaims", "held_claims", true},
		{"ExecutorName", "executor_name", true},
		{"TemplateHash", "template_hash", true},
		{"TemplateNodeAlias", "template_node_alias", true},
		{"ParamsSnapshotHash", "params_snapshot_hash", true},
		{"AttributesHash", "attributes_hash", true},
		{"ClaimScopeDataHash", "claim_scope_data_hash", true},
		{"State", "state", false},
		{"SettlingSignalType", "settling_signal_type", false},
		{"Changed", "changed", true},
		{"TerminalKind", "terminal_kind", true},
		{"ErrorClass", "error_class", true},
		{"SubstitutionRefs", "substitution_refs", true},
		{"Extra", "extra", true},
	}
	assertStructJSONShape(t, LeafRunRecord{}, want)
}

func TestClaimTerminalRecord_TagDisciplineAndOrder(t *testing.T) {
	t.Parallel()
	want := []struct {
		field     string
		jsonTag   string
		omitempty bool
	}{
		{"ClaimHandleID", "claim_handle_id", false},
		{"NodeRunID", "run_id", false},
		{"NodeID", "node_id", false},
		{"FrameID", "frame_id", false},
		{"ParentClaimHandleID", "parent_claim_handle_id", true},
		{"OpenLineageRunRef", "open_lineage_run_ref", true},
		{"SubClaimHandleIDs", "sub_claim_handle_ids", true},
		{"CommittedAt", "committed_at", true},
		{"ProducerName", "producer_name", true},
		{"ClaimScopeDataHash", "claim_scope_data_hash", true},
		{"VersionID", "version_id", true},
		{"Outcome", "outcome", false},
		{"Cause", "cause", true},
		{"ProducerMetadata", "producer_metadata", true},
		{"TerminatingSupervisorID", "terminating_supervisor_id", true},
	}
	assertStructJSONShape(t, ClaimTerminalRecord{}, want)
}

func assertStructJSONShape(t *testing.T, v any, want []struct {
	field     string
	jsonTag   string
	omitempty bool
}) {
	t.Helper()
	rt := reflect.TypeOf(v)
	if rt.NumField() != len(want) {
		t.Fatalf("%s: NumField = %d, want %d (fields drifted; update both writer and subscriber + this test)",
			rt.Name(), rt.NumField(), len(want))
	}
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		w := want[i]
		if f.Name != w.field {
			t.Errorf("%s field[%d]: name = %q, want %q (order drifted)", rt.Name(), i, f.Name, w.field)
			continue
		}
		tag := f.Tag.Get("json")
		gotName, gotOmit := parseJSONTag(tag)
		if gotName != w.jsonTag {
			t.Errorf("%s.%s json tag name = %q, want %q", rt.Name(), f.Name, gotName, w.jsonTag)
		}
		if gotOmit != w.omitempty {
			t.Errorf("%s.%s omitempty = %v, want %v", rt.Name(), f.Name, gotOmit, w.omitempty)
		}
	}
}

func parseJSONTag(tag string) (name string, omitempty bool) {
	parts := strings.Split(tag, ",")
	if len(parts) == 0 {
		return tag, false
	}
	name = parts[0]
	for _, opt := range parts[1:] {
		if opt == "omitempty" {
			omitempty = true
		}
	}
	return name, omitempty
}
