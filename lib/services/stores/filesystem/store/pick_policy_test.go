// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/action"
)

func TestParseFromRight(t *testing.T) {
	cases := []struct {
		name       string
		input      string
		wantFolder string
		wantClaim  string
		wantNanos  int64
		wantErr    bool
	}{
		{"simple", "area-a.uuid-1.1730000000000000000",
			"area-a", "uuid-1", 1730000000000000000, false},
		{"dotted_folder", "my.docs.uuid-2.1730000000000000001",
			"my.docs", "uuid-2", 1730000000000000001, false},
		{"deep_dotted_folder", "a.b.c.uuid-3.1730000000000000002",
			"a.b.c", "uuid-3", 1730000000000000002, false},
		{"missing_nanos", "area-a-uuid",
			"", "", 0, true},
		{"only_one_dot", "area-a.uuid",
			"", "", 0, true},
		{"non_numeric_nanos", "area-a.uuid.abc",
			"", "", 0, true},
		{"empty_folder", ".uuid.123",
			"", "", 0, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			folder, claim, nanos, err := parseFromRight(c.input)
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q, got nil", c.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if folder != c.wantFolder || claim != c.wantClaim || nanos != c.wantNanos {
				t.Errorf("got (%q,%q,%d), want (%q,%q,%d)",
					folder, claim, nanos, c.wantFolder, c.wantClaim, c.wantNanos)
			}
		})
	}
}

func TestRunSyncReconciles(t *testing.T) {
	root := t.TempDir()
	sub := "documents"
	if err := os.MkdirAll(filepath.Join(root, sub), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"area-a", "area-b", ".hidden"} {
		if err := os.MkdirAll(filepath.Join(root, sub, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	pp := &PickPolicy{
		Root:              sub,
		OnCommit:          action.Action{Kind: action.Recycle},
		OnGiveUp:          action.Action{Kind: action.Recycle},
		VisibilityTimeout: time.Minute,
		SyncStrategy:      "on_open",
	}
	st, err := New(Config{
		Root:         root,
		PickPolicies: map[string]*PickPolicy{"@ring": pp},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := st.runSync("@ring", pp); err != nil {
		t.Fatalf("runSync: %v", err)
	}
	availDir := filepath.Join(root, ".fs-store", "ring", "available")
	entries, _ := os.ReadDir(availDir)
	got := make(map[string]bool)
	for _, e := range entries {
		got[e.Name()] = true
	}
	if !got["area-a"] || !got["area-b"] {
		t.Errorf("expected available/{area-a,area-b}, got %v", got)
	}
	if got[".hidden"] {
		t.Errorf("leading-dot folder should be filtered, got sentinel for it")
	}
	if len(entries) != 2 {
		t.Errorf("expected exactly 2 sentinels (area-a, area-b), got %d: %v", len(entries), got)
	}

	if err := os.RemoveAll(filepath.Join(root, sub, "area-a")); err != nil {
		t.Fatal(err)
	}
	if err := st.runSync("@ring", pp); err != nil {
		t.Fatalf("runSync (after rm): %v", err)
	}
	entries, _ = os.ReadDir(availDir)
	got = make(map[string]bool)
	for _, e := range entries {
		got[e.Name()] = true
	}
	if got["area-a"] {
		t.Errorf("removed folder still has a sentinel: %v", got)
	}
	if !got["area-b"] {
		t.Errorf("untouched sentinel disappeared: %v", got)
	}
}

func TestOpenPickPolicy_Basic(t *testing.T) {
	root := t.TempDir()
	sub := "docs"
	// @deliberate: single folder so sync-creation map-iteration order
	// doesn't affect the assertion (release_to_back rotation is asserted
	// by TestCommit_ReleaseToBack).
	must(t, os.MkdirAll(filepath.Join(root, sub, "alpha"), 0o755))
	pp := &PickPolicy{
		Root:              sub,
		OnCommit:          action.Action{Kind: action.Recycle},
		OnGiveUp:          action.Action{Kind: action.Recycle},
		VisibilityTimeout: time.Minute,
		SyncStrategy:      "on_open",
	}
	st, err := New(Config{
		Root:         root,
		PickPolicies: map[string]*PickPolicy{"@docs-ring": pp},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	outcome, err := st.Open(context.Background(), "claim-1", "@docs-ring")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !outcome.Available {
		t.Fatal("expected Available, got Unavailable")
	}
	var addr, scope string
	must(t, json.Unmarshal(outcome.Result.Address, &addr))
	must(t, json.Unmarshal(outcome.Result.ClaimScope, &scope))
	wantAddr := filepath.Join(root, sub, "alpha")
	wantScope := filepath.Join(sub, "alpha")
	if addr != wantAddr {
		t.Errorf("address = %q, want %q", addr, wantAddr)
	}
	if scope != wantScope {
		t.Errorf("scope = %q, want %q", scope, wantScope)
	}
}

func TestOpenPickPolicy_EmptyQueueReturnsUnavailable(t *testing.T) {
	root := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(root, "docs"), 0o755))
	pp := &PickPolicy{
		Root: "docs", OnCommit: action.Action{Kind: action.Recycle},
		OnGiveUp: action.Action{Kind: action.Recycle}, VisibilityTimeout: time.Minute,
	}
	st, err := New(Config{Root: root, PickPolicies: map[string]*PickPolicy{"@r": pp}})
	must(t, err)
	outcome, err := st.Open(context.Background(), "c", "@r")
	must(t, err)
	if outcome.Available {
		t.Fatal("expected Unavailable on empty queue")
	}
}

func TestOpenSelectorDispatch(t *testing.T) {
	root := t.TempDir()
	sub := "docs"
	must(t, os.MkdirAll(filepath.Join(root, sub, "alpha"), 0o755))
	pp := &PickPolicy{
		Root: sub, OnCommit: action.Action{Kind: action.Recycle},
		OnGiveUp: action.Action{Kind: action.Recycle}, VisibilityTimeout: time.Minute,
	}
	st, err := New(Config{Root: root, PickPolicies: map[string]*PickPolicy{"@docs-ring": pp}})
	must(t, err)
	o1, _ := st.Open(context.Background(), "c1", "@docs-ring")
	if !o1.Available {
		t.Fatal("pick-policy selector should be Available")
	}
	o2, _ := st.Open(context.Background(), "c2", "docs/alpha")
	if !o2.Available {
		t.Fatal("scope selector should be Available")
	}
	if string(o1.Result.ClaimScope) != string(o2.Result.ClaimScope) {
		t.Errorf("pick-policy scope (%s) != scope (%s) for same logical folder",
			o1.Result.ClaimScope, o2.Result.ClaimScope)
	}
}

func TestOpenPickPolicy_ConcurrentPicksAreUnique(t *testing.T) {
	root := t.TempDir()
	sub := "docs"
	const N = 8
	for i := 0; i < N; i++ {
		must(t, os.MkdirAll(filepath.Join(root, sub, fmt.Sprintf("f-%02d", i)), 0o755))
	}
	pp := &PickPolicy{
		Root: sub, OnCommit: action.Action{Kind: action.Recycle},
		OnGiveUp: action.Action{Kind: action.Recycle}, VisibilityTimeout: time.Minute,
	}
	st, err := New(Config{Root: root, PickPolicies: map[string]*PickPolicy{"@r": pp}})
	must(t, err)

	const M = 32
	var wg sync.WaitGroup
	var mu sync.Mutex
	availableCount := 0
	seenFolders := make(map[string]int)
	for i := 0; i < M; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			outcome, err := st.Open(context.Background(), fmt.Sprintf("claim-%d", i), "@r")
			if err != nil {
				t.Errorf("goroutine %d: Open: %v", i, err)
				return
			}
			if !outcome.Available {
				return
			}
			var folderObj struct {
				Folder string `json:"folder"`
			}
			if err := json.Unmarshal(outcome.Result.Payload, &folderObj); err != nil {
				t.Errorf("goroutine %d: payload: %v", i, err)
				return
			}
			mu.Lock()
			availableCount++
			seenFolders[folderObj.Folder]++
			mu.Unlock()
		}(i)
	}
	wg.Wait()
	if availableCount != N {
		t.Errorf("got %d picks, want %d (M=%d goroutines, N=%d folders)", availableCount, N, M, N)
	}
	for f, c := range seenFolders {
		if c != 1 {
			t.Errorf("folder %s picked %d times; want 1", f, c)
		}
	}
}

func TestCommit_ReleaseToBack(t *testing.T) {
	st, root, sub := newRingStore(t, action.Action{Kind: action.Recycle}, action.Action{Kind: action.Recycle})
	must(t, os.MkdirAll(filepath.Join(root, sub, "alpha"), 0o755))
	must(t, os.MkdirAll(filepath.Join(root, sub, "beta"), 0o755))
	// @deliberate: first pick takes whichever sentinel happens to have
	// the older mtime (sync creation order is map-iteration-random);
	// record it so the rotation assertion below compares against the
	// actually-picked folder.
	o, _ := st.Open(context.Background(), "c-1", "@r")
	if !o.Available {
		t.Fatal("first pick should be Available")
	}
	var first struct{ Folder string }
	must(t, json.Unmarshal(o.Result.Payload, &first))
	must(t, st.Commit(context.Background(), "c-1", o.Result.ClaimScope, o.Result.Address))
	// @constraint: after release_to_back, the first folder sits at the
	// tail; the other folder must be picked next.
	o2, _ := st.Open(context.Background(), "c-2", "@r")
	var second struct{ Folder string }
	must(t, json.Unmarshal(o2.Result.Payload, &second))
	if second.Folder == first.Folder {
		t.Errorf("release_to_back failed to cycle: picked %q twice in a row", first.Folder)
	}
}

func TestCommit_PopAndDelete_RemovesFolder(t *testing.T) {
	st, root, sub := newRingStore(t,
		action.Action{Kind: action.PopAndDelete},
		action.Action{Kind: action.Recycle})
	must(t, os.MkdirAll(filepath.Join(root, sub, "doomed"), 0o755))
	o, _ := st.Open(context.Background(), "c", "@r")
	if !o.Available {
		t.Fatal("pick should be Available")
	}
	must(t, st.Commit(context.Background(), "c", o.Result.ClaimScope, o.Result.Address))
	if _, err := os.Stat(filepath.Join(root, sub, "doomed")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("folder should be removed after pop_and_delete commit; stat err = %v", err)
	}
	// @constraint: pin the in_progress sentinel cleanup behaviour — a
	// regression that drops the unlink in applyPickAction's
	// pop_and_delete arm would leave a stranded sentinel here, causing
	// a wasted-cycle visibility-timeout reclamation later.
	inProgDir := filepath.Join(root, ".fs-store", "r", "in_progress")
	inProg, _ := os.ReadDir(inProgDir)
	if len(inProg) != 0 {
		names := make([]string, 0, len(inProg))
		for _, e := range inProg {
			names = append(names, e.Name())
		}
		t.Errorf("expected in_progress/ empty after pop_and_delete commit; got %v", names)
	}
}

func TestCommit_Idempotent(t *testing.T) {
	st, root, sub := newRingStore(t, action.Action{Kind: action.Recycle}, action.Action{Kind: action.Recycle})
	must(t, os.MkdirAll(filepath.Join(root, sub, "alpha"), 0o755))
	o, _ := st.Open(context.Background(), "c", "@r")
	must(t, st.Commit(context.Background(), "c", o.Result.ClaimScope, o.Result.Address))
	must(t, st.Commit(context.Background(), "c", o.Result.ClaimScope, o.Result.Address))
}

func TestSweep_ReclaimsExpired(t *testing.T) {
	root := t.TempDir()
	sub := "docs"
	must(t, os.MkdirAll(filepath.Join(root, sub, "alpha"), 0o755))
	pp := &PickPolicy{
		Root: sub, OnCommit: action.Action{Kind: action.Recycle},
		OnGiveUp:          action.Action{Kind: action.Recycle},
		VisibilityTimeout: 50 * time.Millisecond,
		SyncStrategy:      "on_open",
	}
	st, err := New(Config{Root: root, PickPolicies: map[string]*PickPolicy{"@r": pp}})
	must(t, err)
	o, _ := st.Open(context.Background(), "c", "@r")
	if !o.Available {
		t.Fatal("pick should be Available")
	}
	time.Sleep(100 * time.Millisecond)
	must(t, st.sweepOnce())
	availDir := filepath.Join(root, ".fs-store", "r", "available")
	entries, _ := os.ReadDir(availDir)
	found := false
	for _, e := range entries {
		if e.Name() == "alpha" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected alpha back in available/ after sweep, got %v", entries)
	}
}

// TestOpenPickPolicy_StoreRootSingleEntry exercises a pick policy whose
// Root is the store root itself (Root: "") combined with a FolderPattern
// that matches exactly one top-level folder. This shape lets a policy
// yield a single rw claim on a top-level subtree (here: "guidance/")
// rather than picking from inside a folder. recycle-on-commit gives
// the consumer a long-lived "always-available" rw claim against that
// subtree.
func TestOpenPickPolicy_StoreRootSingleEntry(t *testing.T) {
	root := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(root, "guidance"), 0o755))
	must(t, os.MkdirAll(filepath.Join(root, "specs"), 0o755))
	must(t, os.MkdirAll(filepath.Join(root, "prompts"), 0o755))
	pp := &PickPolicy{
		Root:              "",
		FolderPattern:     regexp.MustCompile(`^guidance$`),
		OnCommit:          action.Action{Kind: action.Recycle},
		OnGiveUp:          action.Action{Kind: action.Recycle},
		VisibilityTimeout: time.Minute,
		SyncStrategy:      "on_open",
	}
	st, err := New(Config{Root: root, PickPolicies: map[string]*PickPolicy{"@root": pp}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	outcome, err := st.Open(context.Background(), "claim-1", "@root")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !outcome.Available {
		t.Fatal("expected Available, got Unavailable")
	}
	var addr, scope string
	must(t, json.Unmarshal(outcome.Result.Address, &addr))
	must(t, json.Unmarshal(outcome.Result.ClaimScope, &scope))
	wantAddr := filepath.Join(root, "guidance")
	wantScope := "guidance"
	if addr != wantAddr {
		t.Errorf("address = %q, want %q", addr, wantAddr)
	}
	if scope != wantScope {
		t.Errorf("scope = %q, want %q", scope, wantScope)
	}
	// @constraint: the pattern excludes "specs" and "prompts"; only
	// "guidance" should be in available/.
	availDir := filepath.Join(root, ".fs-store", "root", "available")
	entries, _ := os.ReadDir(availDir)
	got := make(map[string]bool)
	for _, e := range entries {
		got[e.Name()] = true
	}
	// @constraint: "guidance" was just claimed (rename-as-claim), so it
	// lives in in_progress/ rather than available/; the other two must
	// NOT be present.
	if got["specs"] || got["prompts"] {
		t.Errorf("non-matching top-level folders leaked into available/: %v", got)
	}
}

func TestFolderPattern_FiltersNonMatching(t *testing.T) {
	root := t.TempDir()
	sub := "docs"
	must(t, os.MkdirAll(filepath.Join(root, sub, "area-a"), 0o755))
	must(t, os.MkdirAll(filepath.Join(root, sub, "skip-me"), 0o755))
	must(t, os.MkdirAll(filepath.Join(root, sub, "area-b"), 0o755))
	pp := &PickPolicy{
		Root:              sub,
		FolderPattern:     regexp.MustCompile(`^area-.*$`),
		OnCommit:          action.Action{Kind: action.Recycle},
		OnGiveUp:          action.Action{Kind: action.Recycle},
		VisibilityTimeout: time.Minute,
		SyncStrategy:      "on_open",
	}
	st, err := New(Config{Root: root, PickPolicies: map[string]*PickPolicy{"@r": pp}})
	must(t, err)
	must(t, st.runSync("@r", pp))
	availDir := filepath.Join(root, ".fs-store", "r", "available")
	entries, _ := os.ReadDir(availDir)
	got := make(map[string]bool)
	for _, e := range entries {
		got[e.Name()] = true
	}
	if got["skip-me"] {
		t.Errorf("skip-me should be filtered: %v", got)
	}
	if !got["area-a"] || !got["area-b"] {
		t.Errorf("matching folders should be enqueued: %v", got)
	}
}

func TestMultiPolicy_NoCrossTalk(t *testing.T) {
	root := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(root, "docs", "alpha"), 0o755))
	must(t, os.MkdirAll(filepath.Join(root, "reports", "beta"), 0o755))
	p1 := &PickPolicy{
		Root: "docs", OnCommit: action.Action{Kind: action.Recycle},
		OnGiveUp: action.Action{Kind: action.Recycle}, VisibilityTimeout: time.Minute,
	}
	p2 := &PickPolicy{
		Root: "reports", OnCommit: action.Action{Kind: action.Recycle},
		OnGiveUp: action.Action{Kind: action.Recycle}, VisibilityTimeout: time.Minute,
	}
	st, err := New(Config{Root: root, PickPolicies: map[string]*PickPolicy{
		"@docs": p1, "@reports": p2,
	}})
	must(t, err)
	o1, _ := st.Open(context.Background(), "c1", "@docs")
	o2, _ := st.Open(context.Background(), "c2", "@reports")
	var f1, f2 struct{ Folder string }
	must(t, json.Unmarshal(o1.Result.Payload, &f1))
	must(t, json.Unmarshal(o2.Result.Payload, &f2))
	if f1.Folder != "alpha" || f2.Folder != "beta" {
		t.Errorf("expected (alpha, beta); got (%s, %s)", f1.Folder, f2.Folder)
	}
}

func newRingStore(t *testing.T, onCommit, onGiveUp action.Action) (*Store, string, string) {
	t.Helper()
	root := t.TempDir()
	sub := "docs"
	must(t, os.MkdirAll(filepath.Join(root, sub), 0o755))
	pp := &PickPolicy{
		Root: sub, OnCommit: onCommit, OnGiveUp: onGiveUp,
		VisibilityTimeout: time.Minute, SyncStrategy: "on_open",
	}
	st, err := New(Config{Root: root, PickPolicies: map[string]*PickPolicy{"@r": pp}})
	must(t, err)
	return st, root, sub
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
