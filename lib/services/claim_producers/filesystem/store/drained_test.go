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

func drainedPathFor(root, selector string) string {
	return filepath.Join(PolicyStateDir(root, selector), "drained")
}

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

	for i := 0; i < 3; i++ {
		o, err := st.Open(context.Background(), fmt.Sprintf("p1-c%d", i), "@r")
		must(t, err)
		if !o.Available {
			t.Fatalf("pass 1 iteration %d: expected Available, got Unavailable", i)
		}
		must(t, st.Commit(context.Background(), fmt.Sprintf("p1-c%d", i), o.Result.ClaimScope, o.Result.Address, ""))
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

	for i := 0; i < 3; i++ {
		o, err := st.Open(context.Background(), fmt.Sprintf("p2-c%d", i), "@r")
		must(t, err)
		if !o.Available {
			t.Fatalf("pass 2 iteration %d: expected Available, got Unavailable", i)
		}
		must(t, st.Commit(context.Background(), fmt.Sprintf("p2-c%d", i), o.Result.ClaimScope, o.Result.Address, ""))
	}
	o, err = st.Open(context.Background(), "p2-x", "@r")
	must(t, err)
	if o.Available {
		t.Fatal("pass 2: expected Unavailable to consume drained")
	}

	must(t, os.RemoveAll(filepath.Join(root, sub, "b")))
	picks := 0
	for {
		o, err := st.Open(context.Background(), fmt.Sprintf("p3-c%d", picks), "@r")
		must(t, err)
		if !o.Available {
			break
		}
		picks++
		must(t, st.Commit(context.Background(), fmt.Sprintf("p3-c%d", picks-1), o.Result.ClaimScope, o.Result.Address, ""))
		if picks > 5 {
			t.Fatal("pass 3: more picks than expected")
		}
	}
	if picks != 2 {
		t.Errorf("pass 3: expected 2 picks (a, c remain after removing b); got %d", picks)
	}
}

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

func TestOnDrain_SweepClearsDrained(t *testing.T) {
	root := t.TempDir()
	sub := "docs"
	must(t, os.MkdirAll(filepath.Join(root, sub, "alpha"), 0o755))
	pp := &PickPolicy{
		Root:              sub,
		OnCommit:          action.Action{Kind: action.Pop},
		OnGiveUp:          action.Action{Kind: action.Recycle},
		VisibilityTimeout: time.Minute,
		SyncStrategy:      "on_drain",
	}
	st, err := New(Config{Root: root, PickPolicies: map[string]*PickPolicy{"@r": pp}})
	must(t, err)

	o, err := st.Open(context.Background(), "c", "@r")
	must(t, err)
	if !o.Available {
		t.Fatal("expected Available")
	}
	if _, err := os.Stat(drainedPathFor(root, "@r")); err != nil {
		t.Errorf("expected drained sentinel after claiming sole item; stat err = %v", err)
	}

	ageInProgressEntry(t, root, "@r")
	st.sweepOnce()
	if _, err := os.Stat(drainedPathFor(root, "@r")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("drained should be cleared by sweep reclaim; stat err = %v", err)
	}

	o2, err := st.Open(context.Background(), "c-y", "@r")
	must(t, err)
	if !o2.Available {
		t.Fatal("expected Available after sweep reclaim cleared drained")
	}
}

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
					o.Result.ClaimScope, o.Result.Address, ""); err != nil {
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
	state := PolicyStateDir(root, "@r")
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
			o.Result.ClaimScope, o.Result.Address, ""))
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
	if sawUnavailable && drainedFileExists(drainedPathFor(root, "@r")) {
		t.Errorf("post-storm: drained should be consumed after returning Unavailable; still present")
	}
}
