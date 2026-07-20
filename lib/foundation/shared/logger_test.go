// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package shared

import (
	"sync"
	"testing"
)

func TestCapturingLogger_WithDerivedChildRecordsVisibleToParent(t *testing.T) {
	parent := NewCapturingLogger()
	child := parent.With("role", "supervisor")

	parent.Info("parent message")
	child.Info("child message")

	got := parent.Records()
	if len(got) != 2 {
		t.Fatalf("parent.Records() returned %d records, want 2 (one logged directly, one via the With-derived child)", len(got))
	}
	if got[0].Msg != "parent message" {
		t.Fatalf("got[0].Msg = %q, want %q", got[0].Msg, "parent message")
	}
	if got[1].Msg != "child message" {
		t.Fatalf("got[1].Msg = %q, want %q", got[1].Msg, "child message")
	}
	if got[1].Fields["role"] != "supervisor" {
		t.Fatalf("got[1].Fields[%q] = %v, want %q", "role", got[1].Fields["role"], "supervisor")
	}
}

func TestCapturingLogger_GrandchildRecordsVisibleToParent(t *testing.T) {
	parent := NewCapturingLogger()
	child := parent.With("role", "scheduler")
	grandchild := child.With("tick", 1)

	grandchild.Warn("grandchild message")

	got := parent.Records()
	if len(got) != 1 {
		t.Fatalf("parent.Records() returned %d records, want 1", len(got))
	}
	if got[0].Fields["role"] != "scheduler" || got[0].Fields["tick"] != 1 {
		t.Fatalf("got[0].Fields = %+v, want role=scheduler tick=1", got[0].Fields)
	}
}

func TestCapturingLogger_WithDoesNotMutateParentBaseFields(t *testing.T) {
	parent := NewCapturingLogger()
	_ = parent.With("role", "supervisor")

	parent.Info("parent message")

	got := parent.Records()
	if len(got) != 1 {
		t.Fatalf("parent.Records() returned %d records, want 1", len(got))
	}
	if _, ok := got[0].Fields["role"]; ok {
		t.Fatalf("parent record carries the child's base field %q — With mutated the parent's base map", "role")
	}
}

func TestCapturingLogger_ConcurrentParentAndChildAppendsDoNotRace(t *testing.T) {
	parent := NewCapturingLogger()
	child := parent.With("role", "supervisor")

	const n = 200
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			parent.Info("parent message")
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			child.Info("child message")
		}
	}()
	wg.Wait()

	got := parent.Records()
	if len(got) != 2*n {
		t.Fatalf("parent.Records() returned %d records, want %d (all parent and child appends must land in the single shared sink)", len(got), 2*n)
	}
}

func TestCapturingLogger_ClearOnParentClearsChildView(t *testing.T) {
	parent := NewCapturingLogger()
	child := parent.With("role", "supervisor")

	child.Info("child message")
	parent.Clear()

	if got := parent.Records(); len(got) != 0 {
		t.Fatalf("parent.Records() after Clear() = %v, want empty", got)
	}
}
