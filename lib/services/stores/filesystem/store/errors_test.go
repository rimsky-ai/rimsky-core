// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// errors_test.go — the misconfigured-backing-root rejection: producer
// verbs against a root that vanished (or went read-only) after startup
// must fail with the classed `fs/root_unavailable` error, not silently
// succeed — that classed rejection is what crosses the wire to the
// operator's API response and `error_types:` routing.
package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func newStoreAt(t *testing.T, root string) *Store {
	t.Helper()
	s, err := New(Config{Root: root})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
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

	// Open while healthy — direct mode registers the claim.
	out, err := s.Open(context.Background(), "claim-1", "items/out.json")
	if err != nil || !out.Available {
		t.Fatalf("Open while healthy: %v available=%v", err, out.Available)
	}

	// The backing root vanishes (unmounted volume / deleted path).
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}

	requireRootUnavailable(t, s.Release(context.Background(), "claim-1", nil, nil))
}

func TestReleaseRejectsWhenRootReadOnly(t *testing.T) {
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

	requireRootUnavailable(t, s.Release(context.Background(), "claim-1", nil, nil))
}

func TestCommitRejectsWhenRootRemoved(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "data")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	s := newStoreAt(t, root)

	// Open while healthy — direct mode registers the claim.
	out, err := s.Open(context.Background(), "claim-1", "items/out.json")
	if err != nil || !out.Available {
		t.Fatalf("Open while healthy: %v available=%v", err, out.Available)
	}

	// The backing root vanishes (unmounted volume / deleted path).
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}

	requireRootUnavailable(t, s.Commit(context.Background(), "claim-1", nil, nil))
}

func TestCommitRejectsWhenRootReadOnly(t *testing.T) {
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

	requireRootUnavailable(t, s.Commit(context.Background(), "claim-1", nil, nil))
}

func TestAbandonRejectsWhenRootRemoved(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "data")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	s := newStoreAt(t, root)

	// Open while healthy — direct mode registers the claim.
	out, err := s.Open(context.Background(), "claim-1", "items/out.json")
	if err != nil || !out.Available {
		t.Fatalf("Open while healthy: %v available=%v", err, out.Available)
	}

	// The backing root vanishes (unmounted volume / deleted path).
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}

	requireRootUnavailable(t, s.Abandon(context.Background(), "claim-1", nil, nil))
}

func TestAbandonRejectsWhenRootReadOnly(t *testing.T) {
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

	requireRootUnavailable(t, s.Abandon(context.Background(), "claim-1", nil, nil))
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

func TestReleaseSucceedsWhileRootHealthy(t *testing.T) {
	root := t.TempDir()
	s := newStoreAt(t, root)
	if _, err := s.Open(context.Background(), "claim-1", "items/out.json"); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s.Release(context.Background(), "claim-1", nil, nil); err != nil {
		t.Fatalf("Release while healthy: %v", err)
	}
}
