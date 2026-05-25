// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// N8 scenario — concurrent_staging.
//
// Multiple goroutines stage candidates against distinct scopes
// concurrently and commit them. The producer must serialize its
// internal state mutations such that every entry is observed once.
package atomicstaging

import (
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	"github.com/fallguyconsulting/rimsky/examples/atomic-staging-fs-producer/store"
)

func TestConcurrentStaging(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "atomic-staging")
	s, err := store.New(root)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	const N = 8
	var wg sync.WaitGroup
	errs := make([]error, N)
	for i := 0; i < N; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			claimID := "concurrent-" + strconv.Itoa(i)
			scope := "scope-" + strconv.Itoa(i)
			if _, err := s.Open(claimID, scope); err != nil {
				errs[i] = err
				return
			}
			errs[i] = s.Commit(claimID)
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine[%d]: %v", i, err)
		}
	}
	entries, err := s.Entries()
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("after concurrent staging+commit, expected 0 in-flight, got %d", len(entries))
	}
}
