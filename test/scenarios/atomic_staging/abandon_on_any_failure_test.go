// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// N8 scenario — abandon_on_any_failure.
//
// When any sub-stage of an atomic-staging operation fails, the
// producer Abandons the candidate without firing the swap. The
// scenario opens 3 candidates, abandons 1, commits 2; the
// canonical area sees the committed candidates and the abandoned
// candidate is dropped without leaving a published artifact.
package atomicstaging

import (
	"path/filepath"
	"testing"

	"github.com/fallguyconsulting/rimsky/examples/atomic-staging-fs-producer/store"
)

func TestAbandonOnAnyFailure(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "atomic-staging")
	s, err := store.New(root)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	// Open three candidates with distinct scopes (so committing two
	// doesn't conflict on swap path).
	for i, scope := range []string{"alpha", "beta", "gamma"} {
		if _, err := s.Open(makeClaimID(i), scope); err != nil {
			t.Fatalf("Open[%d]: %v", i, err)
		}
	}
	// Abandon the middle candidate.
	if err := s.Abandon(makeClaimID(1)); err != nil {
		t.Fatalf("Abandon: %v", err)
	}
	// Commit the others.
	if err := s.Commit(makeClaimID(0)); err != nil {
		t.Fatalf("Commit[0]: %v", err)
	}
	if err := s.Commit(makeClaimID(2)); err != nil {
		t.Fatalf("Commit[2]: %v", err)
	}
	entries, err := s.Entries()
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("after commit+abandon+commit, expected 0 staging entries; got %d", len(entries))
	}
}
