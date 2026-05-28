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

// fakeMatchCounterPersist is a tiny test double for the
// matchCounterPersist surface used by incrementMatchCountersAfterMerge.
// Captures the (instanceID, indices) arguments and the number of
// Transaction / Increment calls so the test can assert on the
// integration's behaviour without spinning up a real persistence
// backend.
type fakeMatchCounterPersist struct {
	txCount        int
	incrementCalls []fakeIncrementCall
	// incrementErr forces IncrementAttributeOverrideMatchCounts to
	// return this error on every call (left nil for happy-path tests).
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
	// The IncrementAttributeOverrideMatchCounts implementation here is
	// the fakeInstancesTable below — it does not dereference the tx
	// handle, so passing nil is safe for this seam test.
	return fn(ctx, nil)
}

func (f *fakeMatchCounterPersist) Instances() persistence.InstanceTable {
	return &fakeInstancesTable{owner: f}
}

// fakeInstancesTable implements persistence.InstanceTable for the
// single method incrementMatchCountersAfterMerge exercises. Every
// other method panics — the test fails loudly if the helper grows a
// new persistence dependency that's not asserted on here.
type fakeInstancesTable struct {
	owner *fakeMatchCounterPersist
}

func (f *fakeInstancesTable) IncrementAttributeOverrideMatchCounts(
	_ context.Context, instanceID shared.UUID, indices []int, _ persistence.Tx,
) error {
	// Copy indices to detach from any caller-side slice reuse.
	cp := append([]int(nil), indices...)
	f.owner.incrementCalls = append(f.owner.incrementCalls,
		fakeIncrementCall{InstanceID: instanceID, Indices: cp})
	return f.owner.incrementErr
}

// Every other persistence.InstanceTable method panics. The helper
// must never call them; if it does, the panic surfaces the
// regression immediately.
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

// TestIncrementMatchCountersAfterMerge pins the supervisor →
// IncrementAttributeOverrideMatchCounts integration in isolation.
// The scenario tests cover the happy path end-to-end against real
// persistence; this unit test pins the contract for the three cases
// scenarios do NOT exercise:
//
//   - nil / empty matched slice → no Transaction call, no
//     persistence touch (the steady-state happy path for dispatches
//     without matcher hits — would otherwise pay for an empty tx
//     against every dispatch).
//   - non-empty matched → exactly ONE Transaction call wrapping
//     IncrementAttributeOverrideMatchCounts(instanceID, matched).
//   - Increment errors → swallowed via Warn (counter loss is
//     observability degradation, not dispatch failure).
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
		// MUST NOT panic and MUST NOT propagate the error (helper
		// returns void by design — observability degradation, not
		// dispatch failure per spec §"Error handling").
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
		// nil logger must not panic even when the increment errors —
		// the helper degrades to silent swallow.
		incrementMatchCountersAfterMerge(ctx, fake, nil, instanceID, []int{0})
		if fake.txCount != 1 {
			t.Fatalf("expected exactly one Transaction call; txCount=%d", fake.txCount)
		}
	})
}
