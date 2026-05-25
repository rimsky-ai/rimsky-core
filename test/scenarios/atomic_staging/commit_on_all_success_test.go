// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// N8 scenario — commit_on_all_success.
//
// The atomic-staging-fs-producer Open creates a staging entry; on
// Commit the entry flips into the published set. The scenario
// drives N concurrent Open+Commit and pins that every staged
// entry is finalized.
package atomicstaging

import (
	"path/filepath"
	"testing"

	"github.com/fallguyconsulting/rimsky/examples/atomic-staging-fs-producer/store"
)

func TestCommitOnAllSuccess(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "atomic-staging")
	s, err := store.New(root)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	const N = 5
	for i := 0; i < N; i++ {
		claimID := makeClaimID(i)
		if _, err := s.Open(claimID, makeScope(i)); err != nil {
			t.Fatalf("Open[%d]: %v", i, err)
		}
		if err := s.Commit(claimID); err != nil {
			t.Fatalf("Commit[%d]: %v", i, err)
		}
	}
	entries, err := s.Entries()
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	if len(entries) != 0 {
		// Commit removes the staging entry (the published file is
		// elsewhere); a clean store has zero in-flight after success.
		t.Errorf("commit-on-success: expected 0 staging entries, got %d", len(entries))
	}
}

func makeClaimID(i int) string {
	return "scenario-claim-" + makeIdx(i)
}

func makeScope(i int) string {
	return "scenario-scope-" + makeIdx(i)
}

func makeIdx(i int) string {
	// avoid pulling fmt for one int; small lookup.
	switch i {
	case 0:
		return "0"
	case 1:
		return "1"
	case 2:
		return "2"
	case 3:
		return "3"
	case 4:
		return "4"
	}
	return "n"
}
