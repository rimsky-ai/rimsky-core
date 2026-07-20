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

func TestCommit_IsIdempotentInClaimID(t *testing.T) {
	root := t.TempDir()
	st, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	entry, err := st.Open("claim-commit-twice", "tenant-a")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := os.WriteFile(filepath.Join(entry.StagingPath, "data.txt"), []byte("v1"), 0o644); err != nil {
		t.Fatalf("write staging file: %v", err)
	}
	if err := st.Commit("claim-commit-twice"); err != nil {
		t.Fatalf("first Commit: %v", err)
	}
	if err := st.Commit("claim-commit-twice"); err != nil {
		t.Fatalf("retried Commit on an already-committed claim_id must succeed as a no-op (idempotent terminal verb), got err = %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(entry.CanonicalPath, "data.txt")); err != nil || string(got) != "v1" {
		t.Fatalf("canonical content must survive the retried Commit unchanged: got=%q err=%v", got, err)
	}
}

func TestCommit_UnknownClaimIDIsNoop(t *testing.T) {
	root := t.TempDir()
	st, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := st.Commit("never-opened"); err != nil {
		t.Fatalf("Commit on an unknown claim_id must be a no-op (symmetric with Abandon), got err = %v", err)
	}
}

func TestOpen_IsIdempotentInClaimID(t *testing.T) {
	root := t.TempDir()
	st, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	first, err := st.Open("claim-open-twice", "tenant-a")
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	second, err := st.Open("claim-open-twice", "tenant-a")
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	if second != first {
		t.Fatalf("repeated Open for the same claim_id must return the same entry: first=%+v second=%+v", first, second)
	}
	all, err := st.Entries()
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	count := 0
	for _, e := range all {
		if e.ClaimID == "claim-open-twice" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("repeated Open must not append a duplicate side-table row, found %d entries for claim-open-twice", count)
	}
}

func TestCommit_OverwritesExistingCanonicalAndRollsBackOnInstallFailure(t *testing.T) {
	root := t.TempDir()
	st, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	first, err := st.Open("claim-1", "tenant-a")
	if err != nil {
		t.Fatalf("Open claim-1: %v", err)
	}
	if err := os.WriteFile(filepath.Join(first.StagingPath, "data.txt"), []byte("v1"), 0o644); err != nil {
		t.Fatalf("write staging file: %v", err)
	}
	if err := st.Commit("claim-1"); err != nil {
		t.Fatalf("Commit claim-1: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(first.CanonicalPath, "data.txt")); err != nil || string(got) != "v1" {
		t.Fatalf("canonical content after first commit: got=%q err=%v", got, err)
	}

	second, err := st.Open("claim-2", "tenant-a")
	if err != nil {
		t.Fatalf("Open claim-2: %v", err)
	}
	if err := os.WriteFile(filepath.Join(second.StagingPath, "data.txt"), []byte("v2"), 0o644); err != nil {
		t.Fatalf("write staging file: %v", err)
	}
	if err := st.Commit("claim-2"); err != nil {
		t.Fatalf("Commit claim-2 (overwrite of an existing canonical): %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(second.CanonicalPath, "data.txt")); err != nil || string(got) != "v2" {
		t.Fatalf("canonical content after second commit should reflect the atomic swap: got=%q err=%v", got, err)
	}
	asideGlob, err := filepath.Glob(second.CanonicalPath + ".aside-*")
	if err != nil {
		t.Fatalf("glob aside: %v", err)
	}
	if len(asideGlob) != 0 {
		t.Fatalf("aside copy must be cleaned up after a successful swap, found: %v", asideGlob)
	}

	third, err := st.Open("claim-3", "tenant-a")
	if err != nil {
		t.Fatalf("Open claim-3: %v", err)
	}
	if err := os.WriteFile(filepath.Join(third.StagingPath, "data.txt"), []byte("v3"), 0o644); err != nil {
		t.Fatalf("write staging file: %v", err)
	}
	if err := os.RemoveAll(third.StagingPath); err != nil {
		t.Fatalf("simulate a vanished staging dir: %v", err)
	}
	if err := st.Commit("claim-3"); err == nil {
		t.Fatal("Commit with a missing staging dir should fail so the rollback path runs")
	}
	if got, err := os.ReadFile(filepath.Join(third.CanonicalPath, "data.txt")); err != nil || string(got) != "v2" {
		t.Fatalf("canonical view must be rolled back to the pre-commit content after a failed install: got=%q err=%v", got, err)
	}
	asideGlob, err = filepath.Glob(third.CanonicalPath + ".aside-*")
	if err != nil {
		t.Fatalf("glob aside: %v", err)
	}
	if len(asideGlob) != 0 {
		t.Fatalf("aside copy must not linger after a rollback, found: %v", asideGlob)
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
