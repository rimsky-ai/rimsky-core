// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package store

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/action"
)

// drainedPath is the absolute path to the per-policy drained sentinel.
func drainedPathFor(root, selector string) string {
	return filepath.Join(policyStateDir(root, selector), "drained")
}

// TestOnDrain_SinglePass — `pop + on_drain` drains exactly N items
// per pass before returning Unavailable. After the first Unavailable
// consumes the drained sentinel, the next Open re-runs sync and
// picks up the corpus again (folders still on disk under pop).
//
// Per spec §5.7: each drain pass produces N Acquired + 1 Unavailable.
// The pop action keeps folders in place, so the corpus repopulates
// itself across passes. Operators mutate the corpus externally
// between passes to actually progress (e.g. delete the folder after
// processing it through the executor).
func TestOnDrain_SinglePass(t *testing.T) {
	root := t.TempDir()
	sub := "docs"
	for _, name := range []string{"a", "b", "c"} {
		must(t, os.MkdirAll(filepath.Join(root, sub, name), 0o755))
	}
	pp := &PickPolicy{
		Root:              sub,
		OnCommit:          action.Action{Kind: action.Pop},
		OnGiveUp:          action.Action{Kind: action.Recycle},
		VisibilityTimeout: time.Minute,
		SyncStrategy:      "on_drain",
	}
	st, err := New(Config{Root: root, PickPolicies: map[string]*PickPolicy{"@r": pp}})
	must(t, err)

	// Pass 1: 3 picks, then Unavailable consumes drained.
	for i := 0; i < 3; i++ {
		o, err := st.Open(context.Background(), fmt.Sprintf("p1-c%d", i), "@r")
		must(t, err)
		if !o.Available {
			t.Fatalf("pass 1 iteration %d: expected Available, got Unavailable", i)
		}
		must(t, st.Commit(context.Background(), fmt.Sprintf("p1-c%d", i), o.Result.ClaimScope, o.Result.Address))
	}
	if _, err := os.Stat(drainedPathFor(root, "@r")); err != nil {
		t.Errorf("pass 1: expected drained sentinel after final pop; stat err = %v", err)
	}
	o, err := st.Open(context.Background(), "p1-x", "@r")
	must(t, err)
	if o.Available {
		t.Fatal("pass 1: expected Unavailable to consume drained")
	}
	if _, err := os.Stat(drainedPathFor(root, "@r")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("pass 1: drained should be consumed; stat err = %v", err)
	}

	// Pass 2: corpus unchanged on disk → sync re-discovers a, b, c → 3 more picks.
	//
	// Note: under `pop`, folders stay on disk; runSync re-discovers
	// them on the next pass. Operators using `pop` + `on_drain` MUST
	// mutate the corpus externally between passes (e.g. delete the
	// folder after processing it through the executor) to actually
	// drain. For an in-store one-shot drain, use `pop_and_delete`
	// (or `pop_and_move`) instead. Pass 3 below demonstrates this:
	// removing a folder externally is what makes the next drain pass
	// produce fewer picks.
	for i := 0; i < 3; i++ {
		o, err := st.Open(context.Background(), fmt.Sprintf("p2-c%d", i), "@r")
		must(t, err)
		if !o.Available {
			t.Fatalf("pass 2 iteration %d: expected Available, got Unavailable", i)
		}
		must(t, st.Commit(context.Background(), fmt.Sprintf("p2-c%d", i), o.Result.ClaimScope, o.Result.Address))
	}
	o, err = st.Open(context.Background(), "p2-x", "@r")
	must(t, err)
	if o.Available {
		t.Fatal("pass 2: expected Unavailable to consume drained")
	}

	// Pass 3: corpus shrunk externally (remove `b`) → 2 picks then Unavailable.
	must(t, os.RemoveAll(filepath.Join(root, sub, "b")))
	picks := 0
	for {
		o, err := st.Open(context.Background(), fmt.Sprintf("p3-c%d", picks), "@r")
		must(t, err)
		if !o.Available {
			break
		}
		picks++
		must(t, st.Commit(context.Background(), fmt.Sprintf("p3-c%d", picks-1), o.Result.ClaimScope, o.Result.Address))
		if picks > 5 {
			t.Fatal("pass 3: more picks than expected")
		}
	}
	if picks != 2 {
		t.Errorf("pass 3: expected 2 picks (a, c remain after removing b); got %d", picks)
	}
}

// TestOnDrain_EmptyCorpus — 0 folders + pop + on_drain. The drained
// sentinel oscillates: each Open either writes drained (and returns
// Unavailable) or consumes drained (and returns Unavailable).
func TestOnDrain_EmptyCorpus(t *testing.T) {
	root := t.TempDir()
	sub := "docs"
	must(t, os.MkdirAll(filepath.Join(root, sub), 0o755))
	pp := &PickPolicy{
		Root:              sub,
		OnCommit:          action.Action{Kind: action.Pop},
		OnGiveUp:          action.Action{Kind: action.Recycle},
		VisibilityTimeout: time.Minute,
		SyncStrategy:      "on_drain",
	}
	st, err := New(Config{Root: root, PickPolicies: map[string]*PickPolicy{"@r": pp}})
	must(t, err)

	for i := 0; i < 5; i++ {
		o, err := st.Open(context.Background(), fmt.Sprintf("c-%d", i), "@r")
		must(t, err)
		if o.Available {
			t.Fatalf("iter %d: expected Unavailable on empty corpus", i)
		}
	}
}

// TestOnDrain_SweepClearsDrained — after the queue drains and the
// drained sentinel is written, a sweep that reclaims an in-progress
// sentinel must clear drained so the next Open re-picks the
// reclaimed work.
//
// Setup: single-folder corpus. Open #1 claims alpha, writes drained
// (lastItem=true). Don't commit. Sweep would normally just reclaim
// the stale in-progress sentinel — but with drained present, the
// sweep also clears drained so the next Open can pick the reclaimed
// item up directly (without first burning a Unavailable to consume
// the stale drained sentinel).
func TestOnDrain_SweepClearsDrained(t *testing.T) {
	root := t.TempDir()
	sub := "docs"
	must(t, os.MkdirAll(filepath.Join(root, sub, "alpha"), 0o755))
	pp := &PickPolicy{
		Root:              sub,
		OnCommit:          action.Action{Kind: action.Pop},
		OnGiveUp:          action.Action{Kind: action.Recycle},
		VisibilityTimeout: 50 * time.Millisecond,
		SyncStrategy:      "on_drain",
	}
	st, err := New(Config{Root: root, PickPolicies: map[string]*PickPolicy{"@r": pp}})
	must(t, err)

	// Open #1: claims alpha (last item) → drained written.
	o, err := st.Open(context.Background(), "c", "@r")
	must(t, err)
	if !o.Available {
		t.Fatal("expected Available")
	}
	if _, err := os.Stat(drainedPathFor(root, "@r")); err != nil {
		t.Errorf("expected drained sentinel after claiming sole item; stat err = %v", err)
	}

	// Wait past visibility timeout, then sweep — the in-progress
	// sentinel is reclaimed AND drained is cleared.
	time.Sleep(80 * time.Millisecond)
	must(t, st.sweepOnce())
	if _, err := os.Stat(drainedPathFor(root, "@r")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("drained should be cleared by sweep reclaim; stat err = %v", err)
	}

	// Next Open must see the reclaimed sentinel directly: available is
	// non-empty, so the on_drain check skips the empty-branch entirely.
	o2, err := st.Open(context.Background(), "c-y", "@r")
	must(t, err)
	if !o2.Available {
		t.Fatal("expected Available after sweep reclaim cleared drained")
	}
}

// TestOnDrain_RaceUnderConcurrentOpens — M concurrent Opens against a
// corpus of N folders under `pop + on_drain`. With pop the folders
// stay on disk, so the corpus may repopulate across drain passes
// during the storm — Acquired count can exceed N. The bound assertions
// are:
//
//   - Available + Unavailable == M (every goroutine produced one outcome)
//   - At most one drained sentinel exists in the policy state
//     directory at the end (it's a sentinel — never duplicated)
//   - At least one Acquired (the corpus is non-empty)
//   - After the storm, a follow-up serialized Open observes a
//     consistent sentinel state: either drained is present (next
//     Open consumes it and returns Unavailable) or drained is absent
//     (next Open finds available items and returns Available). Spec
//     §5.3: drained is the load-bearing pass-boundary signal — its
//     presence/absence after the storm must remain semantically
//     consistent with the queue state.
//
// The race coverage is in the file-creation O_EXCL invariant on
// drained writes: under -race + many concurrent Opens, no goroutine
// should panic or trip the data-race detector.
func TestOnDrain_RaceUnderConcurrentOpens(t *testing.T) {
	root := t.TempDir()
	sub := "docs"
	const N = 10
	for i := 0; i < N; i++ {
		must(t, os.MkdirAll(filepath.Join(root, sub, fmt.Sprintf("f-%02d", i)), 0o755))
	}
	pp := &PickPolicy{
		Root:              sub,
		OnCommit:          action.Action{Kind: action.Pop},
		OnGiveUp:          action.Action{Kind: action.Recycle},
		VisibilityTimeout: time.Minute,
		SyncStrategy:      "on_drain",
	}
	st, err := New(Config{Root: root, PickPolicies: map[string]*PickPolicy{"@r": pp}})
	must(t, err)

	const M = 20
	var wg sync.WaitGroup
	var available int64
	var unavailable int64
	for i := 0; i < M; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			o, err := st.Open(context.Background(), fmt.Sprintf("c-%d", i), "@r")
			if err != nil {
				t.Errorf("goroutine %d: Open: %v", i, err)
				return
			}
			if o.Available {
				atomic.AddInt64(&available, 1)
				if err := st.Commit(context.Background(), fmt.Sprintf("c-%d", i),
					o.Result.ClaimScope, o.Result.Address); err != nil {
					t.Errorf("goroutine %d: Commit: %v", i, err)
				}
				return
			}
			atomic.AddInt64(&unavailable, 1)
		}(i)
	}
	wg.Wait()

	if total := available + unavailable; total != M {
		t.Errorf("Available + Unavailable = %d, want %d", total, M)
	}
	if available == 0 {
		t.Errorf("expected at least one Acquired; got 0")
	}
	state := policyStateDir(root, "@r")
	entries, _ := os.ReadDir(state)
	drainedCount := 0
	for _, e := range entries {
		if e.Name() == "drained" {
			drainedCount++
		}
	}
	if drainedCount > 1 {
		t.Errorf("expected at most 1 drained sentinel; got %d", drainedCount)
	}

	// Post-storm drained-cycle check. Drive serialized Opens until we
	// observe one Unavailable outcome — this proves the drained
	// pass-boundary signal still works after the storm. Per spec §5.3
	// drained is the load-bearing pass-boundary signal: under
	// `pop + on_drain` every drain pass must terminate in exactly one
	// Unavailable that consumes (or writes-and-then-the-next-Open-
	// consumes) the sentinel. With `pop` folders stay on disk, so
	// sync re-discovers them; the Unavailable must come within a
	// bounded number of iterations (<= 2N + 1: one full repopulate +
	// drain + the trailing Unavailable).
	const maxIters = 2*N + 5
	sawUnavailable := false
	drainedFlipped := false
	prevDrained := drainedFileExists(drainedPathFor(root, "@r"))
	for i := 0; i < maxIters; i++ {
		o, err := st.Open(context.Background(), fmt.Sprintf("post-storm-%d", i), "@r")
		must(t, err)
		if !o.Available {
			sawUnavailable = true
			break
		}
		must(t, st.Commit(context.Background(), fmt.Sprintf("post-storm-%d", i),
			o.Result.ClaimScope, o.Result.Address))
		curDrained := drainedFileExists(drainedPathFor(root, "@r"))
		if curDrained != prevDrained {
			drainedFlipped = true
		}
		prevDrained = curDrained
	}
	if !sawUnavailable {
		t.Errorf("post-storm: expected at least one Unavailable within %d iterations (drained pass-boundary signal not working); drainedFlipped=%v",
			maxIters, drainedFlipped)
	}
	// After we saw Unavailable, the drained sentinel must have been
	// consumed — i.e., it should now be absent (until the next Open
	// triggers another sync that empties available/).
	if sawUnavailable && drainedFileExists(drainedPathFor(root, "@r")) {
		t.Errorf("post-storm: drained should be consumed after returning Unavailable; still present")
	}
}
