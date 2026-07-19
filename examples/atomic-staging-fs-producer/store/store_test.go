// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRelease_UncommittedClaimDiscardsStagingLikeAbandon(t *testing.T) {
	root := t.TempDir()
	st, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	entry, err := st.Open("claim-release", "tenant-a")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := os.WriteFile(filepath.Join(entry.StagingPath, "scratch.txt"), []byte("uncommitted"), 0o644); err != nil {
		t.Fatalf("write staging file: %v", err)
	}

	if err := st.Release("claim-release"); err != nil {
		t.Fatalf("Release: %v", err)
	}

	if _, err := os.Stat(entry.StagingPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staging dir %q must be discarded by Release, stat err = %v", entry.StagingPath, err)
	}
	if _, ok, err := st.lookup("claim-release"); err != nil || ok {
		t.Fatalf("side-table entry for claim-release must be removed by Release, ok=%v err=%v", ok, err)
	}
	if pathExists(entry.CanonicalPath) {
		t.Fatalf("Release of an uncommitted claim must never populate the canonical view %q", entry.CanonicalPath)
	}
}

func TestRelease_UnknownClaimIsNoop(t *testing.T) {
	root := t.TempDir()
	st, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	entry, err := st.Open("claim-committed", "tenant-a")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := st.Commit("claim-committed"); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	if err := st.Release("claim-committed"); err != nil {
		t.Fatalf("Release after Commit must be a no-op, got err = %v", err)
	}
	if !pathExists(entry.CanonicalPath) {
		t.Fatalf("Release must never touch the already-committed canonical view %q", entry.CanonicalPath)
	}
}

func TestNew_RejectsMismatchedFilesystems(t *testing.T) {
	root := t.TempDir()

	orig := assertSameFilesystemFn
	defer func() { assertSameFilesystemFn = orig }()

	sentinel := errors.New("staging and canonical are on different filesystems")
	assertSameFilesystemFn = func(a, b string) error { return sentinel }

	_, err := New(root)
	if !errors.Is(err, sentinel) {
		t.Fatalf("New() with mismatched filesystems must surface the same-filesystem check's error, got %v want %v", err, sentinel)
	}
}

func TestNew_AcceptsMatchedFilesystems(t *testing.T) {
	root := t.TempDir()

	orig := assertSameFilesystemFn
	defer func() { assertSameFilesystemFn = orig }()
	called := false
	assertSameFilesystemFn = func(a, b string) error {
		called = true
		return nil
	}

	if _, err := New(root); err != nil {
		t.Fatalf("New: %v", err)
	}
	if !called {
		t.Fatal("New() must invoke the same-filesystem startup check")
	}
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
