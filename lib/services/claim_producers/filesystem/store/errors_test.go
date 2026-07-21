// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/action"
)

func newStoreAt(t *testing.T, root string) *Store {
	t.Helper()
	s, err := New(Config{Root: root})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func newPickPolicyStoreAt(t *testing.T, root string) (*Store, string) {
	t.Helper()
	sub := "docs"
	must(t, os.MkdirAll(filepath.Join(root, sub, "alpha"), 0o755))
	pp := &PickPolicy{
		Root:              sub,
		OnCommit:          action.Action{Kind: action.Recycle},
		OnGiveUp:          action.Action{Kind: action.Recycle},
		VisibilityTimeout: time.Minute,
		SyncStrategy:      "on_open",
	}
	s, err := New(Config{Root: root, PickPolicies: map[string]*PickPolicy{"@r": pp}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s, "@r"
}

func requireRootUnavailable(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected fs/root_unavailable error, got nil")
	}
	var ce *ClassedError
	if !errors.As(err, &ce) {
		t.Fatalf("expected *ClassedError, got %T: %v", err, err)
	}
	if ce.Class != RootUnavailableClass {
		t.Fatalf("expected class %q, got %q", RootUnavailableClass, ce.Class)
	}
}

func TestReleaseRejectsWhenRootRemoved(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "data")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	s := newStoreAt(t, root)

	out, err := s.Open(context.Background(), "claim-1", "items/out.json")
	if err != nil || !out.Available {
		t.Fatalf("Open while healthy: %v available=%v", err, out.Available)
	}

	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}

	requireRootUnavailable(t, s.Release(context.Background(), "claim-1", nil, nil, ""))
}

func TestReleaseSucceedsWhenRootReadOnly_ScopedClaim(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "data")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	s := newStoreAt(t, root)

	out, err := s.Open(context.Background(), "claim-1", "items/out.json")
	if err != nil || !out.Available {
		t.Fatalf("Open while healthy: %v available=%v", err, out.Available)
	}

	if err := os.Chmod(root, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(root, 0o755) })

	if err := s.Release(context.Background(), "claim-1", out.Result.ClaimScope, out.Result.Address, ""); err != nil {
		t.Fatalf("Release on a scoped (non-pick-policy) claim performs no filesystem writes; must succeed on a read-only root: %v", err)
	}
}

func TestReleaseRejectsWhenRootReadOnly_PickPolicyClaim(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "data")
	s, selector := newPickPolicyStoreAt(t, root)

	out, err := s.Open(context.Background(), "claim-1", selector)
	if err != nil || !out.Available {
		t.Fatalf("Open while healthy: %v available=%v", err, out.Available)
	}

	if err := os.Chmod(root, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(root, 0o755) })

	requireRootUnavailable(t, s.Release(context.Background(), "claim-1", out.Result.ClaimScope, out.Result.Address, ""))
}

func TestCommitRejectsWhenRootRemoved(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "data")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	s := newStoreAt(t, root)

	out, err := s.Open(context.Background(), "claim-1", "items/out.json")
	if err != nil || !out.Available {
		t.Fatalf("Open while healthy: %v available=%v", err, out.Available)
	}

	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}

	requireRootUnavailable(t, s.Commit(context.Background(), "claim-1", nil, nil, ""))
}

func TestCommitSucceedsWhenRootReadOnly_ScopedClaim(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "data")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	s := newStoreAt(t, root)

	out, err := s.Open(context.Background(), "claim-1", "items/out.json")
	if err != nil || !out.Available {
		t.Fatalf("Open while healthy: %v available=%v", err, out.Available)
	}

	if err := os.Chmod(root, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(root, 0o755) })

	if err := s.Commit(context.Background(), "claim-1", out.Result.ClaimScope, out.Result.Address, ""); err != nil {
		t.Fatalf("Commit on a scoped (non-pick-policy) claim performs no filesystem writes; must succeed on a read-only root: %v", err)
	}
}

func TestCommitRejectsWhenRootReadOnly_PickPolicyClaim(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "data")
	s, selector := newPickPolicyStoreAt(t, root)

	out, err := s.Open(context.Background(), "claim-1", selector)
	if err != nil || !out.Available {
		t.Fatalf("Open while healthy: %v available=%v", err, out.Available)
	}

	if err := os.Chmod(root, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(root, 0o755) })

	requireRootUnavailable(t, s.Commit(context.Background(), "claim-1", out.Result.ClaimScope, out.Result.Address, ""))
}

func TestAbandonRejectsWhenRootRemoved(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "data")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	s := newStoreAt(t, root)

	out, err := s.Open(context.Background(), "claim-1", "items/out.json")
	if err != nil || !out.Available {
		t.Fatalf("Open while healthy: %v available=%v", err, out.Available)
	}

	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}

	requireRootUnavailable(t, s.Abandon(context.Background(), "claim-1", nil, nil, ""))
}

func TestAbandonSucceedsWhenRootReadOnly_ScopedClaim(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "data")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	s := newStoreAt(t, root)

	out, err := s.Open(context.Background(), "claim-1", "items/out.json")
	if err != nil || !out.Available {
		t.Fatalf("Open while healthy: %v available=%v", err, out.Available)
	}

	if err := os.Chmod(root, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(root, 0o755) })

	if err := s.Abandon(context.Background(), "claim-1", out.Result.ClaimScope, out.Result.Address, ""); err != nil {
		t.Fatalf("Abandon on a scoped (non-pick-policy) claim performs no filesystem writes; must succeed on a read-only root: %v", err)
	}
}

func TestAbandonRejectsWhenRootReadOnly_PickPolicyClaim(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "data")
	s, selector := newPickPolicyStoreAt(t, root)

	out, err := s.Open(context.Background(), "claim-1", selector)
	if err != nil || !out.Available {
		t.Fatalf("Open while healthy: %v available=%v", err, out.Available)
	}

	if err := os.Chmod(root, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(root, 0o755) })

	requireRootUnavailable(t, s.Abandon(context.Background(), "claim-1", out.Result.ClaimScope, out.Result.Address, ""))
}

func TestOpenRejectsWhenRootRemoved(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "data")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	s := newStoreAt(t, root)
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}

	_, err := s.Open(context.Background(), "claim-1", "items/out.json")
	requireRootUnavailable(t, err)
}

func TestOpenSucceedsWhenRootReadOnly_ScopedClaim(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "data")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	s := newStoreAt(t, root)
	if err := os.Chmod(root, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(root, 0o755) })

	if _, err := s.Open(context.Background(), "claim-1", "items/out.json"); err != nil {
		t.Fatalf("Open of a scoped selector performs no filesystem writes; must succeed on a read-only root: %v", err)
	}
}

func TestOpenRejectsWhenRootReadOnly_PickPolicyClaim(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "data")
	sub := "docs"
	if err := os.MkdirAll(filepath.Join(root, sub, "alpha"), 0o755); err != nil {
		t.Fatal(err)
	}
	pp := &PickPolicy{
		Root:              sub,
		OnCommit:          action.Action{Kind: action.Recycle},
		OnGiveUp:          action.Action{Kind: action.Recycle},
		VisibilityTimeout: time.Minute,
		SyncStrategy:      "on_open",
	}
	s, err := New(Config{Root: root, PickPolicies: map[string]*PickPolicy{"@r": pp}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := os.Chmod(root, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(root, 0o755) })

	_, err = s.Open(context.Background(), "claim-1", "@r")
	requireRootUnavailable(t, err)
}

func TestReleaseSucceedsWhileRootHealthy(t *testing.T) {
	root := t.TempDir()
	s := newStoreAt(t, root)
	if _, err := s.Open(context.Background(), "claim-1", "items/out.json"); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s.Release(context.Background(), "claim-1", nil, nil, ""); err != nil {
		t.Fatalf("Release while healthy: %v", err)
	}
}
