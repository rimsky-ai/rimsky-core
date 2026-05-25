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

	"github.com/fallguy/rimsky/foundation/cascade"
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
func (f *fakeLineageTable) QueryByParentRunID(_ context.Context, _ shared.UUID, _ int) ([]persistence.LineageRow, error) {
	return nil, nil
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
		RunID:              run,
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
	if decoded.RunID != run {
		t.Fatalf("run_id roundtrip failed")
	}
	if decoded.SettlingSignalType != "terminal/success" {
		t.Fatalf("settling_signal_type roundtrip failed")
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
		RunID:              shared.UUID(uuid.New()),
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

// TestWriteClaimTerminalLineage_EmptyOutcomeRejected pins the
// post-2026-05-17 contract: an empty Outcome on a claim_terminal write
// returns an error (no silent default to `committed`). Prevents an
// Abandon path that forgets to populate Outcome from silently producing
// a Commit-shaped row.
func TestWriteClaimTerminalLineage_EmptyOutcomeRejected(t *testing.T) {
	lt := &fakeLineageTable{}
	rec := ClaimTerminalRecord{
		ClaimHandleID: shared.UUID(uuid.New()),
		RunID:         shared.UUID(uuid.New()),
		NodeID:        shared.UUID(uuid.New()),
		FrameID:       shared.UUID(uuid.New()),
		ProducerName:  "store",
		// Outcome left empty intentionally.
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

// TestWriteLeafRunLineage_ParentRunIDPersistedAndQueryable pins the
// cycle-2 fix: ParentRunID from the LeafRunEmitInput threads into
// LeafRunRecord.ParentRunID, which serializes into the JSONB payload,
// which is queryable via QueryByParentRunID. Without this end-to-end
// round-trip the descendant walker (`GET /lineage/runs/{run_id}/descendants`)
// returns empty for every seed.
func TestWriteLeafRunLineage_ParentRunIDPersistedAndQueryable(t *testing.T) {
	lt := &queryableFakeLineageTable{}
	ctx := context.Background()
	inst := shared.UUID(uuid.New())
	frame := shared.UUID(uuid.New())
	parent := shared.UUID(uuid.New())
	child := shared.UUID(uuid.New())
	rec := LeafRunRecord{
		RunID:              child,
		NodeID:             shared.UUID(uuid.New()),
		FrameID:            frame,
		State:              "fresh",
		SettlingSignalType: "terminal/success",
		ParentRunID:        parent.String(),
	}
	if err := WriteLeafRunLineage(ctx, nil, lt, inst, frame, time.Now().UTC(), rec); err != nil {
		t.Fatalf("WriteLeafRunLineage: %v", err)
	}
	// Round-trip through the JSON column to confirm `parent_run_id`
	// is present and lookup-by-parent returns the child row.
	rows, err := lt.QueryByParentRunID(ctx, parent, 10)
	if err != nil {
		t.Fatalf("QueryByParentRunID: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	var decoded LeafRunRecord
	if err := json.Unmarshal(rows[0].Record, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.ParentRunID != parent.String() {
		t.Fatalf("parent_run_id round-trip failed: got %q want %q",
			decoded.ParentRunID, parent.String())
	}
	if decoded.RunID != child {
		t.Fatalf("run_id round-trip failed")
	}
}

// queryableFakeLineageTable is the subset of fakeLineageTable that
// supports QueryByParentRunID via JSON inspection of the in-memory rows.
// Mirrors the postgres `record->>'parent_run_id' = $1` predicate.
type queryableFakeLineageTable struct {
	fakeLineageTable
}

func (f *queryableFakeLineageTable) QueryByParentRunID(_ context.Context, parentRunID shared.UUID, limit int) ([]persistence.LineageRow, error) {
	var out []persistence.LineageRow
	for _, r := range f.rows {
		if r.RecordKind != persistence.LineageRecordKindLeafRun {
			continue
		}
		var rec LeafRunRecord
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

// emitFakePersist is a minimal persistence.Tables surface backing the
// EmitLeafRunLineage end-to-end test. Only `Lineage()` and
// `Transaction()` are honored; every other accessor returns nil. The
// fake is intentionally narrow — `EmitLeafRunLineage` reaches into only
// those two surfaces, and embedding a noop stub would force a sweeping
// definition change every time `persistence.Tables` adds a method.
type emitFakePersist struct {
	lt persistence.LineageTable
}

func (f *emitFakePersist) Templates() persistence.TemplateTable       { return nil }
func (f *emitFakePersist) TemplateTags() persistence.TemplateTagTable { return nil }
func (f *emitFakePersist) Instances() persistence.InstanceTable       { return nil }
func (f *emitFakePersist) LifecycleIdempotency() persistence.LifecycleIdempotencyTable {
	return nil
}
func (f *emitFakePersist) Nodes() persistence.NodeTable                              { return nil }
func (f *emitFakePersist) ClaimHandles() persistence.ClaimHandleTable                { return nil }
func (f *emitFakePersist) NodeAttributes() persistence.NodeAttributeTable            { return nil }
func (f *emitFakePersist) ClaimHolders() persistence.ClaimHolderTable                { return nil }
func (f *emitFakePersist) Events() persistence.EventTable                            { return nil }
func (f *emitFakePersist) Supervisors() persistence.SupervisorTable                  { return nil }
func (f *emitFakePersist) Frames() persistence.FrameTable                            { return nil }
func (f *emitFakePersist) BlobOrphans() persistence.BlobOrphanTable                  { return nil }
func (f *emitFakePersist) NodeEvents() persistence.NodeEventTable                    { return nil }
func (f *emitFakePersist) WaitSet() persistence.WaitSetTable                         { return nil }
func (f *emitFakePersist) Messages() persistence.MessagesTable                       { return nil }
func (f *emitFakePersist) MessageIdempotencies() persistence.MessageIdempotencyTable { return nil }
func (f *emitFakePersist) Lineage() persistence.LineageTable                         { return f.lt }
func (f *emitFakePersist) PublisherSubscriptions() persistence.PublisherSubscriptionsTable {
	return nil
}
func (f *emitFakePersist) RunTree() persistence.RunTreeTable        { return nil }
func (f *emitFakePersist) RunScopes() persistence.RunScopeTable     { return nil }
func (f *emitFakePersist) APIKeys() persistence.APIKeyTable         { return nil }
func (f *emitFakePersist) Breakpoints() persistence.BreakpointTable { return nil }
func (f *emitFakePersist) BreakpointHits() persistence.BreakpointHitTable {
	return nil
}

func (f *emitFakePersist) Transaction(ctx context.Context, fn func(ctx context.Context, tx persistence.Tx) error) error {
	// The writer is tx-agnostic — the in-memory fake doesn't care about
	// the tx handle, so we pass nil through and run the body inline.
	return fn(ctx, nil)
}

// TestEmitLeafRunLineage_OmitsEmptyParentRunID drives EmitLeafRunLineage
// end-to-end via a minimal RunArgs fixture and pins the nil-pointer →
// empty-string conversion for ParentRunID:
//
//  1. With in.ParentRunID == nil, the emitted row's LeafRunRecord has
//     `ParentRunID == ""` (and the JSONB payload omits the key via
//     `omitempty`, preserving the descendant-walker predicate).
//  2. With in.ParentRunID = &someID, the emitted row's LeafRunRecord
//     has `ParentRunID == someID.String()`.
//
// The pre-2026-05-17 shape of this test called WriteLeafRunLineage
// directly, leaving `EmitLeafRunLineage`'s nil-pointer guard
// (`in.ParentRunID != nil`) uncovered — a regression in that branch
// would only surface in scenario tests.
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
			RunID:              shared.UUID(uuid.New()),
			NodeID:             shared.UUID(uuid.New()),
			State:              string(cascade.NodeStateFresh),
			SettlingSignalType: "terminal/success",
			ParentRunID:        nil, // root run
		})
		if len(lt.rows) != 1 {
			t.Fatalf("expected 1 row, got %d", len(lt.rows))
		}
		// Decode into the typed record: ParentRunID must be empty.
		var rec LeafRunRecord
		if err := json.Unmarshal(lt.rows[0].Record, &rec); err != nil {
			t.Fatalf("unmarshal typed: %v", err)
		}
		if rec.ParentRunID != "" {
			t.Fatalf("root run: ParentRunID got %q want \"\"", rec.ParentRunID)
		}
		// Decode into the raw map: the JSON key must be absent (the
		// omitempty drop is what protects the postgres predicate
		// `record->>'parent_run_id' = $1` from matching the empty
		// string).
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
			RunID:              shared.UUID(uuid.New()),
			NodeID:             shared.UUID(uuid.New()),
			State:              string(cascade.NodeStateFresh),
			SettlingSignalType: "terminal/success",
			ParentRunID:        &parent,
		})
		if len(lt.rows) != 1 {
			t.Fatalf("expected 1 row, got %d", len(lt.rows))
		}
		var rec LeafRunRecord
		if err := json.Unmarshal(lt.rows[0].Record, &rec); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if rec.ParentRunID != parent.String() {
			t.Fatalf("child run: ParentRunID got %q want %q",
				rec.ParentRunID, parent.String())
		}
	})
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

// TestLeafRunRecord_TagDisciplineAndOrder pins the writer-side
// LeafRunRecord JSON-tag discipline + field order so the wire contract
// against `subscribers/openlineage/subscriber.go::LeafRunRecord` stays
// load-bearing on BOTH sides. The subscriber-side mirror test lives at
// `subscribers/openlineage/subscriber_test.go::TestLeafRunRecord_TagDisciplineAndOrder`;
// any field change requires updating both lists in the same commit.
func TestLeafRunRecord_TagDisciplineAndOrder(t *testing.T) {
	t.Parallel()
	want := []struct {
		field     string
		jsonTag   string
		omitempty bool
	}{
		{"RunID", "run_id", false},
		{"NodeID", "node_id", false},
		{"FrameID", "frame_id", false},
		{"ChildKey", "child_key", true},
		{"NodeAlias", "node_alias", true},
		{"ParentRunID", "parent_run_id", true},
		{"FrameTriggerKind", "frame_trigger_kind", true},
		{"TriggerMessageID", "trigger_message_id", true},
		{"HeldClaims", "held_claims", true},
		{"ExecutorName", "executor_name", true},
		{"ExecutorVersion", "executor_version", true},
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

// TestClaimTerminalRecord_TagDisciplineAndOrder pins the writer-side
// ClaimTerminalRecord JSON-tag discipline + field order against the
// subscriber's mirror struct. Same rationale as
// TestLeafRunRecord_TagDisciplineAndOrder.
func TestClaimTerminalRecord_TagDisciplineAndOrder(t *testing.T) {
	t.Parallel()
	want := []struct {
		field     string
		jsonTag   string
		omitempty bool
	}{
		{"ClaimHandleID", "claim_handle_id", false},
		{"RunID", "run_id", false},
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
	}
	assertStructJSONShape(t, ClaimTerminalRecord{}, want)
}

// assertStructJSONShape verifies that v's exported struct fields match
// the expected list in declaration order, json-tag name, and
// `omitempty` discipline.
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
