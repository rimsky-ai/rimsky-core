// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package store

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/action"
)

// TestAction_Pop_FolderStays — pop drains the queue entry but keeps
// the underlying folder in place. Combined with sync_strategy: on_drain,
// the next Open after drain returns Unavailable; the folder is NOT
// re-discovered until the operator triggers a refresh.
func TestAction_Pop_FolderStays(t *testing.T) {
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
		t.Fatal("expected Available, got Unavailable")
	}
	must(t, st.Commit(context.Background(), "c", o.Result.ClaimScope, o.Result.Address))

	// Folder still on disk.
	if _, err := os.Stat(filepath.Join(root, sub, "alpha")); err != nil {
		t.Errorf("pop should leave folder in place; stat err = %v", err)
	}
	// in_progress sentinel removed.
	inProgDir := filepath.Join(root, ".fs-store", "r", "in_progress")
	if entries, _ := os.ReadDir(inProgDir); len(entries) != 0 {
		t.Errorf("expected in_progress/ empty after pop commit; got %v", entries)
	}
	// available sentinel for that folder NOT recreated (queue truly drained).
	availDir := filepath.Join(root, ".fs-store", "r", "available")
	if entries, _ := os.ReadDir(availDir); len(entries) != 0 {
		t.Errorf("expected available/ empty after pop commit; got %v", entries)
	}
}

// TestAction_PopAndMove_FolderRenamed — pop_and_move drains the queue
// entry AND renames the folder to the configured target.
func TestAction_PopAndMove_FolderRenamed(t *testing.T) {
	root := t.TempDir()
	sub := "docs"
	must(t, os.MkdirAll(filepath.Join(root, sub, "alpha"), 0o755))
	must(t, os.MkdirAll(filepath.Join(root, "archive"), 0o755))
	pp := &PickPolicy{
		Root:              sub,
		OnCommit:          action.Action{Kind: action.PopAndMove, MoveTarget: "archive"},
		OnGiveUp:          action.Action{Kind: action.Recycle},
		VisibilityTimeout: time.Minute,
		SyncStrategy:      "on_open",
	}
	st, err := New(Config{Root: root, PickPolicies: map[string]*PickPolicy{"@r": pp}})
	must(t, err)

	o, err := st.Open(context.Background(), "c", "@r")
	must(t, err)
	if !o.Available {
		t.Fatal("expected Available, got Unavailable")
	}
	must(t, st.Commit(context.Background(), "c", o.Result.ClaimScope, o.Result.Address))

	// Folder moved to archive/.
	if _, err := os.Stat(filepath.Join(root, "archive", "alpha")); err != nil {
		t.Errorf("expected folder at archive/alpha; stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, sub, "alpha")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("source folder should be gone; stat err = %v", err)
	}
	// in_progress sentinel removed.
	inProgDir := filepath.Join(root, ".fs-store", "r", "in_progress")
	if entries, _ := os.ReadDir(inProgDir); len(entries) != 0 {
		t.Errorf("expected in_progress/ empty after pop_and_move commit; got %v", entries)
	}
}

// TestAction_PopAndMove_GiveUpUsesGiveUpTarget — pop_and_move can be
// configured separately for on_commit and on_give_up. Abandon must use
// the on_give_up target (here "failed/"), not the on_commit target.
func TestAction_PopAndMove_GiveUpUsesGiveUpTarget(t *testing.T) {
	root := t.TempDir()
	sub := "docs"
	must(t, os.MkdirAll(filepath.Join(root, sub, "alpha"), 0o755))
	must(t, os.MkdirAll(filepath.Join(root, "ok"), 0o755))
	must(t, os.MkdirAll(filepath.Join(root, "failed"), 0o755))
	pp := &PickPolicy{
		Root:              sub,
		OnCommit:          action.Action{Kind: action.PopAndMove, MoveTarget: "ok"},
		OnGiveUp:          action.Action{Kind: action.PopAndMove, MoveTarget: "failed"},
		VisibilityTimeout: time.Minute,
		SyncStrategy:      "on_open",
	}
	st, err := New(Config{Root: root, PickPolicies: map[string]*PickPolicy{"@r": pp}})
	must(t, err)

	o, err := st.Open(context.Background(), "c", "@r")
	must(t, err)
	if !o.Available {
		t.Fatal("expected Available, got Unavailable")
	}
	must(t, st.Abandon(context.Background(), "c", o.Result.ClaimScope, o.Result.Address))

	if _, err := os.Stat(filepath.Join(root, "failed", "alpha")); err != nil {
		t.Errorf("expected folder at failed/alpha; stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "ok", "alpha")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("folder should NOT be in ok/; stat err = %v", err)
	}
}

// TestAction_PopAndDelete_FolderGone — pop_and_delete removes the
// folder from disk via os.RemoveAll AND drains the queue entry.
func TestAction_PopAndDelete_FolderGone(t *testing.T) {
	root := t.TempDir()
	sub := "docs"
	must(t, os.MkdirAll(filepath.Join(root, sub, "doomed"), 0o755))
	pp := &PickPolicy{
		Root:              sub,
		OnCommit:          action.Action{Kind: action.PopAndDelete},
		OnGiveUp:          action.Action{Kind: action.Recycle},
		VisibilityTimeout: time.Minute,
		SyncStrategy:      "on_open",
	}
	st, err := New(Config{Root: root, PickPolicies: map[string]*PickPolicy{"@r": pp}})
	must(t, err)

	o, err := st.Open(context.Background(), "c", "@r")
	must(t, err)
	must(t, st.Commit(context.Background(), "c", o.Result.ClaimScope, o.Result.Address))

	if _, err := os.Stat(filepath.Join(root, sub, "doomed")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("folder should be removed after pop_and_delete; stat err = %v", err)
	}
}

// TestAction_Recycle_QueueCycles — recycle returns the queue entry
// to the tail with a fresh mtime, so the same Open→Commit→Open pattern
// re-picks the same folder eventually (after others rotate through).
func TestAction_Recycle_QueueCycles(t *testing.T) {
	root := t.TempDir()
	sub := "docs"
	must(t, os.MkdirAll(filepath.Join(root, sub, "alpha"), 0o755))
	pp := &PickPolicy{
		Root:              sub,
		OnCommit:          action.Action{Kind: action.Recycle},
		OnGiveUp:          action.Action{Kind: action.Recycle},
		VisibilityTimeout: time.Minute,
		SyncStrategy:      "on_open",
	}
	st, err := New(Config{Root: root, PickPolicies: map[string]*PickPolicy{"@r": pp}})
	must(t, err)

	o, err := st.Open(context.Background(), "c", "@r")
	must(t, err)
	must(t, st.Commit(context.Background(), "c", o.Result.ClaimScope, o.Result.Address))

	// Folder still on disk.
	if _, err := os.Stat(filepath.Join(root, sub, "alpha")); err != nil {
		t.Errorf("recycle should leave folder in place; stat err = %v", err)
	}
	// available sentinel re-created.
	availDir := filepath.Join(root, ".fs-store", "r", "available")
	entries, _ := os.ReadDir(availDir)
	if len(entries) != 1 || entries[0].Name() != "alpha" {
		t.Errorf("expected available/alpha after recycle; got %v", entries)
	}
	// Re-pick succeeds.
	o2, err := st.Open(context.Background(), "c2", "@r")
	must(t, err)
	if !o2.Available {
		t.Fatal("expected Available re-pick, got Unavailable")
	}
}
