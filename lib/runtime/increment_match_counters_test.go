// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

type fakeMatchCounterPersist struct {
	txCount        int
	incrementCalls []fakeIncrementCall
	incrementErr error
}

type fakeIncrementCall struct {
	InstanceID shared.UUID
	Indices    []int
}

func (f *fakeMatchCounterPersist) Transaction(
	ctx context.Context, fn func(ctx context.Context, tx persistence.Tx) error,
) error {
	f.txCount++
	return fn(ctx, nil)
}

func (f *fakeMatchCounterPersist) Instances() persistence.InstanceTable {
	return &fakeInstancesTable{owner: f}
}

type fakeInstancesTable struct {
	owner *fakeMatchCounterPersist
}

func (f *fakeInstancesTable) IncrementAttributeOverrideMatchCounts(
	_ context.Context, instanceID shared.UUID, indices []int, _ persistence.Tx,
) error {
	cp := append([]int(nil), indices...)
	f.owner.incrementCalls = append(f.owner.incrementCalls,
		fakeIncrementCall{InstanceID: instanceID, Indices: cp})
	return f.owner.incrementErr
}

func (f *fakeInstancesTable) Create(context.Context, persistence.InstanceCreateInput, persistence.Tx) (persistence.InstanceRow, error) {
	panic("fakeInstancesTable.Create: unexpected call")
}
func (f *fakeInstancesTable) Get(context.Context, shared.UUID, persistence.Tx) (*persistence.InstanceRow, error) {
	panic("fakeInstancesTable.Get: unexpected call")
}
func (f *fakeInstancesTable) GetByInstanceKey(context.Context, string, string, persistence.Tx) (*persistence.InstanceRow, error) {
	panic("fakeInstancesTable.GetByInstanceKey: unexpected call")
}
func (f *fakeInstancesTable) FindAnyByInstanceKey(context.Context, string, persistence.Tx) (*persistence.InstanceRow, error) {
	panic("fakeInstancesTable.FindAnyByInstanceKey: unexpected call")
}
func (f *fakeInstancesTable) List(context.Context, persistence.InstanceListFilter, persistence.ListPagination, persistence.Tx) (persistence.PaginatedListResult[persistence.InstanceRow], error) {
	panic("fakeInstancesTable.List: unexpected call")
}
func (f *fakeInstancesTable) Delete(context.Context, shared.UUID, persistence.Tx) error {
	panic("fakeInstancesTable.Delete: unexpected call")
}
func (f *fakeInstancesTable) MarkTerminated(context.Context, shared.UUID, persistence.Tx) error {
	panic("fakeInstancesTable.MarkTerminated: unexpected call")
}
func (f *fakeInstancesTable) CountActiveByTemplate(context.Context, string, persistence.Tx) (int, error) {
	panic("fakeInstancesTable.CountActiveByTemplate: unexpected call")
}
func (f *fakeInstancesTable) ListTerminatedWithLifecycleRows(context.Context, int, persistence.Tx) ([]persistence.InstanceRow, error) {
	panic("fakeInstancesTable.ListTerminatedWithLifecycleRows: unexpected call")
}
func (f *fakeInstancesTable) CountByActive(context.Context, persistence.Tx) (int, int, error) {
	panic("fakeInstancesTable.CountByActive: unexpected call")
}
func (f *fakeInstancesTable) SetPaused(context.Context, shared.UUID, bool, persistence.Tx) (bool, error) {
	panic("fakeInstancesTable.SetPaused: unexpected call")
}

func TestIncrementMatchCountersAfterMerge(t *testing.T) {
	ctx := context.Background()
	instanceID := uuid.New()

	t.Run("nil matched: no persistence touch", func(t *testing.T) {
		fake := &fakeMatchCounterPersist{}
		incrementMatchCountersAfterMerge(ctx, fake, shared.SilentLogger{}, instanceID, nil)
		if fake.txCount != 0 {
			t.Fatalf("nil matched should not open a tx; txCount=%d", fake.txCount)
		}
		if len(fake.incrementCalls) != 0 {
			t.Fatalf("nil matched should not call Increment; calls=%#v", fake.incrementCalls)
		}
	})

	t.Run("empty matched slice: no persistence touch", func(t *testing.T) {
		fake := &fakeMatchCounterPersist{}
		incrementMatchCountersAfterMerge(ctx, fake, shared.SilentLogger{}, instanceID, []int{})
		if fake.txCount != 0 {
			t.Fatalf("empty matched should not open a tx; txCount=%d", fake.txCount)
		}
		if len(fake.incrementCalls) != 0 {
			t.Fatalf("empty matched should not call Increment; calls=%#v", fake.incrementCalls)
		}
	})

	t.Run("non-empty matched: one Transaction + correct args", func(t *testing.T) {
		fake := &fakeMatchCounterPersist{}
		matched := []int{0, 2, 4}
		incrementMatchCountersAfterMerge(ctx, fake, shared.SilentLogger{}, instanceID, matched)
		if fake.txCount != 1 {
			t.Fatalf("expected exactly one Transaction call; txCount=%d", fake.txCount)
		}
		if len(fake.incrementCalls) != 1 {
			t.Fatalf("expected exactly one Increment call; calls=%#v", fake.incrementCalls)
		}
		call := fake.incrementCalls[0]
		if call.InstanceID != instanceID {
			t.Fatalf("instanceID mismatch: got=%s want=%s", call.InstanceID, instanceID)
		}
		if !reflect.DeepEqual(call.Indices, matched) {
			t.Fatalf("indices mismatch: got=%#v want=%#v", call.Indices, matched)
		}
	})

	t.Run("increment error is swallowed and logged", func(t *testing.T) {
		fake := &fakeMatchCounterPersist{
			incrementErr: errors.New("simulated persistence failure"),
		}
		capLog := shared.NewCapturingLogger()
		matched := []int{1}
		incrementMatchCountersAfterMerge(ctx, fake, capLog, instanceID, matched)
		if fake.txCount != 1 {
			t.Fatalf("expected exactly one Transaction call; txCount=%d", fake.txCount)
		}
		var sawWarn bool
		for _, rec := range capLog.Records() {
			if rec.Level == "warn" && rec.Msg == "instance.attribute_overrides_counter_increment_failed" {
				sawWarn = true
				break
			}
		}
		if !sawWarn {
			t.Fatalf("expected a Warn log on increment error; got: %+v", capLog.Records())
		}
	})

	t.Run("nil logger tolerated on error path", func(t *testing.T) {
		fake := &fakeMatchCounterPersist{
			incrementErr: errors.New("boom"),
		}
		incrementMatchCountersAfterMerge(ctx, fake, nil, instanceID, []int{0})
		if fake.txCount != 1 {
			t.Fatalf("expected exactly one Transaction call; txCount=%d", fake.txCount)
		}
	})
}
