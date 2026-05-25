// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// N8 scenario — sub_stage_verifier_failure.
//
// A verifier executor running inside an atomic-staging subgraph
// fails; the staging entry is Abandoned (no swap fires). The
// scenario drives the failure path against the example store.
package atomicstaging

import (
	"path/filepath"
	"testing"

	"github.com/fallguyconsulting/rimsky/examples/atomic-staging-fs-producer/store"
)

func TestSubStageVerifierFailure_AbandonDropsStaging(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "atomic-staging")
	s, err := store.New(root)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	const claimID = "verifier-fail-claim"
	if _, err := s.Open(claimID, "verifier-fail-scope"); err != nil {
		t.Fatalf("Open: %v", err)
	}
	entries, err := s.Entries()
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("staging entry after Open: got %d want 1", len(entries))
	}
	// Verifier failed → Abandon.
	if err := s.Abandon(claimID); err != nil {
		t.Fatalf("Abandon: %v", err)
	}
	entries, err = s.Entries()
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("after verifier-fail Abandon: expected 0 staging entries, got %d", len(entries))
	}
}
