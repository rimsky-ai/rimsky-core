// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package audit

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	shared "github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/signal"
)

type fakeEvents struct {
	rows []persistence.EventAppendInput
}

func (f *fakeEvents) Append(_ context.Context, in persistence.EventAppendInput, _ persistence.Tx) error {
	f.rows = append(f.rows, in)
	return nil
}

func (f *fakeEvents) List(_ context.Context, _ persistence.EventListFilter, _ persistence.ListPagination, _ persistence.Tx) (persistence.EventListResult, error) {
	return persistence.EventListResult{}, nil
}

func (f *fakeEvents) LastTerminalByNodes(_ context.Context, _ []shared.UUID, _ persistence.Tx) (map[shared.UUID]persistence.EventRow, error) {
	return nil, nil
}

func (f *fakeEvents) DeleteOlderThan(_ context.Context, _ time.Time) (int, error) {
	return 0, nil
}

func TestEmitSignal_WritesCanonicalRow(t *testing.T) {
	events := &fakeEvents{}
	instanceID := uuid.New()
	nodeID := uuid.New()
	now := time.Date(2026, 5, 23, 10, 30, 0, 0, time.UTC)

	sig := signal.Signal{
		Type: "terminal/success",
		Payload: map[string]any{
			"changed":          true,
			"attributes_delta": map[string]any{"foo": "bar"},
			"change_summary":   "ok",
		},
	}
	if err := EmitSignal(context.Background(), events, instanceID, nodeID, sig, now, nil); err != nil {
		t.Fatalf("EmitSignal: %v", err)
	}
	if got := len(events.rows); got != 1 {
		t.Fatalf("EmitSignal: want 1 row, got %d", got)
	}
	row := events.rows[0]
	if row.Kind.String() != "terminal/success" {
		t.Fatalf("EmitSignal: kind = %q; want terminal/success", row.Kind.String())
	}
	if row.InstanceID == nil || *row.InstanceID != instanceID {
		t.Fatalf("EmitSignal: instance id mismatch")
	}
	if row.NodeID == nil || *row.NodeID != nodeID {
		t.Fatalf("EmitSignal: node id mismatch")
	}
	if row.OccurredAt == nil || !row.OccurredAt.Equal(now) {
		t.Fatalf("EmitSignal: occurred_at mismatch")
	}
	if !reflect.DeepEqual(row.Payload, sig.Payload) {
		t.Fatalf("EmitSignal: payload mismatch: got=%+v want=%+v", row.Payload, sig.Payload)
	}
}

func TestEmitSignal_NilPayloadBecomesEmptyMap(t *testing.T) {
	events := &fakeEvents{}
	if err := EmitSignal(context.Background(), events, uuid.New(), uuid.New(),
		signal.Signal{Type: "terminal/success"}, time.Time{}, nil); err != nil {
		t.Fatalf("EmitSignal: %v", err)
	}
	if events.rows[0].Payload == nil {
		t.Fatalf("EmitSignal: payload should be empty map, got nil")
	}
	if events.rows[0].OccurredAt != nil {
		t.Fatalf("EmitSignal: zero occurredAt should leave OccurredAt nil for server NOW()")
	}
}
