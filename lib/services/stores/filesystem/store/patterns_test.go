// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// End-to-end integration tests for the common pick-policy patterns
// described in spec §8.1–§8.5: ring-mode, queue-mode, stage-promote,
// one-shot ingest, and explicit-refresh static queue.

package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/action"
)

// TestPattern_RingMode_LiveDiscovery — `recycle + on_open`. Cycles
// through 2 folders for 3 rounds; midway adds a 3rd folder externally
// and expects the on_open sync to discover it.
func TestPattern_RingMode_LiveDiscovery(t *testing.T) {
	root := t.TempDir()
	sub := "docs"
	must(t, os.MkdirAll(filepath.Join(root, sub, "alpha"), 0o755))
	must(t, os.MkdirAll(filepath.Join(root, sub, "beta"), 0o755))
	pp := &PickPolicy{
		Root:              sub,
		OnCommit:          action.Action{Kind: action.Recycle},
		OnGiveUp:          action.Action{Kind: action.Recycle},
		VisibilityTimeout: time.Minute,
		SyncStrategy:      "on_open",
	}
	st, err := New(Config{Root: root, PickPolicies: map[string]*PickPolicy{"@r": pp}})
	must(t, err)

	for i := 0; i < 4; i++ {
		o, err := st.Open(context.Background(), fmt.Sprintf("c-%d", i), "@r")
		must(t, err)
		if !o.Available {
			t.Fatalf("iter %d: expected Available", i)
		}
		// @constraint: mtime resolution requires a real delay between commits so ring-mode ordering is observable.
		time.Sleep(2 * time.Millisecond)
		must(t, st.Commit(context.Background(), fmt.Sprintf("c-%d", i), o.Result.ClaimScope, o.Result.Address))
	}
	must(t, os.MkdirAll(filepath.Join(root, sub, "gamma"), 0o755))
	// @deliberate: on_open sync must discover the externally-added folder within a bounded number of cycles.
	seen := map[string]bool{}
	for i := 0; i < 6; i++ {
		o, err := st.Open(context.Background(), fmt.Sprintf("c2-%d", i), "@r")
		must(t, err)
		if !o.Available {
			t.Fatalf("iter %d (post-add): expected Available", i)
		}
		var p struct{ Folder string }
		must(t, json.Unmarshal(o.Result.Payload, &p))
		seen[p.Folder] = true
		must(t, st.Commit(context.Background(), fmt.Sprintf("c2-%d", i), o.Result.ClaimScope, o.Result.Address))
	}
	if !seen["gamma"] {
		t.Errorf("expected gamma to be discovered post-add; saw %v", seen)
	}
}

// TestPattern_QueueMode_AutoRefresh — `pop + on_drain`. Drains N
// folders; verifies a single Unavailable per pass, and the next Open
// re-runs sync.
func TestPattern_QueueMode_AutoRefresh(t *testing.T) {
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
		o, err := st.Open(context.Background(), fmt.Sprintf("p1-%d", i), "@r")
		must(t, err)
		if !o.Available {
			t.Fatalf("p1-%d: expected Available", i)
		}
		must(t, st.Commit(context.Background(), fmt.Sprintf("p1-%d", i), o.Result.ClaimScope, o.Result.Address))
	}
	o, err := st.Open(context.Background(), "p1-x", "@r")
	must(t, err)
	if o.Available {
		t.Fatal("p1: expected Unavailable to consume drained")
	}

	// @deliberate: under pop the folders remain on disk, so a second pass re-syncs the same corpus and yields 3 picks again.
	picks := 0
	for {
		o, err := st.Open(context.Background(), fmt.Sprintf("p2-%d", picks), "@r")
		must(t, err)
		if !o.Available {
			break
		}
		picks++
		must(t, st.Commit(context.Background(), fmt.Sprintf("p2-%d", picks-1), o.Result.ClaimScope, o.Result.Address))
		if picks > 5 {
			t.Fatal("p2: more picks than expected")
		}
	}
	if picks != 3 {
		t.Errorf("p2: expected 3 picks; got %d", picks)
	}
}

// TestPattern_StagePromote — `pop_and_move(target=promoted) + on_open`.
// Each commit moves the folder to promoted/.
func TestPattern_StagePromote(t *testing.T) {
	root := t.TempDir()
	sub := "docs"
	must(t, os.MkdirAll(filepath.Join(root, "promoted"), 0o755))
	for _, name := range []string{"a", "b", "c"} {
		must(t, os.MkdirAll(filepath.Join(root, sub, name), 0o755))
	}
	pp := &PickPolicy{
		Root:              sub,
		OnCommit:          action.Action{Kind: action.PopAndMove, MoveTarget: "promoted"},
		OnGiveUp:          action.Action{Kind: action.Recycle},
		VisibilityTimeout: time.Minute,
		SyncStrategy:      "on_open",
	}
	st, err := New(Config{Root: root, PickPolicies: map[string]*PickPolicy{"@r": pp}})
	must(t, err)

	for i := 0; i < 3; i++ {
		o, err := st.Open(context.Background(), fmt.Sprintf("c-%d", i), "@r")
		must(t, err)
		if !o.Available {
			t.Fatalf("iter %d: expected Available", i)
		}
		must(t, st.Commit(context.Background(), fmt.Sprintf("c-%d", i), o.Result.ClaimScope, o.Result.Address))
	}
	for _, name := range []string{"a", "b", "c"} {
		if _, err := os.Stat(filepath.Join(root, "promoted", name)); err != nil {
			t.Errorf("expected promoted/%s after stage-promote; stat err = %v", name, err)
		}
		if _, err := os.Stat(filepath.Join(root, sub, name)); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("source folder %s should be gone; stat err = %v", name, err)
		}
	}
}

// TestPattern_OneShotIngest — `pop_and_delete + on_drain`. Drains N
// folders; verifies they're gone.
func TestPattern_OneShotIngest(t *testing.T) {
	root := t.TempDir()
	sub := "docs"
	for _, name := range []string{"a", "b", "c"} {
		must(t, os.MkdirAll(filepath.Join(root, sub, name), 0o755))
	}
	pp := &PickPolicy{
		Root:              sub,
		OnCommit:          action.Action{Kind: action.PopAndDelete},
		OnGiveUp:          action.Action{Kind: action.Recycle},
		VisibilityTimeout: time.Minute,
		SyncStrategy:      "on_drain",
	}
	st, err := New(Config{Root: root, PickPolicies: map[string]*PickPolicy{"@r": pp}})
	must(t, err)

	for i := 0; i < 3; i++ {
		o, err := st.Open(context.Background(), fmt.Sprintf("c-%d", i), "@r")
		must(t, err)
		if !o.Available {
			t.Fatalf("iter %d: expected Available", i)
		}
		must(t, st.Commit(context.Background(), fmt.Sprintf("c-%d", i), o.Result.ClaimScope, o.Result.Address))
	}
	o, err := st.Open(context.Background(), "x", "@r")
	must(t, err)
	if o.Available {
		t.Fatal("expected Unavailable after drain")
	}
	for _, name := range []string{"a", "b", "c"} {
		if _, err := os.Stat(filepath.Join(root, sub, name)); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("expected folder %s to be deleted; stat err = %v", name, err)
		}
	}
}

// TestPattern_StaticQueue_ExplicitRefresh — `pop + explicit`. Drains
// the queue; subsequent Opens stick at Unavailable until the operator
// triggers a manual sync via Store.runSync (the admin endpoint goes
// through this same code path).
func TestPattern_StaticQueue_ExplicitRefresh(t *testing.T) {
	root := t.TempDir()
	sub := "docs"
	for _, name := range []string{"a", "b"} {
		must(t, os.MkdirAll(filepath.Join(root, sub, name), 0o755))
	}
	pp := &PickPolicy{
		Root:              sub,
		OnCommit:          action.Action{Kind: action.Pop},
		OnGiveUp:          action.Action{Kind: action.Recycle},
		VisibilityTimeout: time.Minute,
		SyncStrategy:      "explicit",
	}
	st, err := New(Config{Root: root, PickPolicies: map[string]*PickPolicy{"@r": pp}})
	must(t, err)

	// @deliberate: explicit sync strategy starts with zero available state until an operator triggers runSync.
	o, err := st.Open(context.Background(), "c-0", "@r")
	must(t, err)
	if o.Available {
		t.Fatal("expected Unavailable until explicit sync")
	}

	must(t, st.runSync("@r", pp))

	for i := 0; i < 2; i++ {
		o, err := st.Open(context.Background(), fmt.Sprintf("c-%d", i+1), "@r")
		must(t, err)
		if !o.Available {
			t.Fatalf("iter %d: expected Available", i)
		}
		must(t, st.Commit(context.Background(), fmt.Sprintf("c-%d", i+1), o.Result.ClaimScope, o.Result.Address))
	}
	for i := 0; i < 3; i++ {
		o, err := st.Open(context.Background(), fmt.Sprintf("c-stick-%d", i), "@r")
		must(t, err)
		if o.Available {
			t.Fatalf("iter %d post-drain: expected sticky Unavailable", i)
		}
	}
	must(t, st.runSync("@r", pp))
	o, err = st.Open(context.Background(), "c-after-sync", "@r")
	must(t, err)
	if !o.Available {
		t.Fatal("expected Available after operator sync")
	}
}
