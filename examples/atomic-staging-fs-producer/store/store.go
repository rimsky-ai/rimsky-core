// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// Package store implements the four-verb atomic-staging logic against a
// POSIX filesystem substrate. The co-located README.md explains the
// per-substrate semantics; this implementation uses two-rename atomic
// swap on Commit.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Store owns the staging area on a POSIX filesystem. Concurrency-safe
// via a single coarse mutex — the workload is filesystem-rename-bound,
// not CPU-bound, and the mutex avoids torn writes to the side-table.
type Store struct {
	root  string
	state stateFile

	mu sync.Mutex
}

// stateFile records per-claim staging metadata so the sweep loop can
// reap leaked staging directories. Stored as JSON next to the staging
// area for simplicity; production deployments should prefer SQLite or
// similar.
type stateFile struct {
	Path string
}

// Entry is one row in the side-table: a (claim_id → staging path)
// mapping plus the canonical target so Commit knows where to rename
// into.
type Entry struct {
	ClaimID       string    `json:"claim_id"`
	Scope         string    `json:"scope"`
	StagingPath   string    `json:"staging_path"`
	CanonicalPath string    `json:"canonical_path"`
	CreatedAt     time.Time `json:"created_at"`
}

// New constructs a Store rooted at the given filesystem directory.
// Creates `<root>/staging/` and `<root>/canonical/` if absent. Also
// validates that `staging/` and `canonical/` live on the SAME
// filesystem — the two-rename atomic swap on Commit relies on
// `rename(2)` being atomic, which the kernel only guarantees within
// one mount. An operator who points the two roots at different mount
// points would silently lose atomicity; we fail loudly at startup
// instead.
func New(root string) (*Store, error) {
	if root == "" {
		return nil, errors.New("atomic-staging: root must be non-empty")
	}
	for _, sub := range []string{"staging", "canonical"} {
		if err := os.MkdirAll(filepath.Join(root, sub), 0o755); err != nil {
			return nil, fmt.Errorf("atomic-staging: mkdir %s: %w", sub, err)
		}
	}
	if err := assertSameFilesystem(
		filepath.Join(root, "staging"),
		filepath.Join(root, "canonical"),
	); err != nil {
		return nil, err
	}
	return &Store{
		root:  root,
		state: stateFile{Path: filepath.Join(root, "producer_state.jsonl")},
	}, nil
}

// Open creates the staging area for (claim_id, scope) and records the
// entry in the side-table. Returns the staging path so the executor
// can write its work product there. The scope is byte-equal-compared
// by rimsky; selectors that should conflict produce byte-equal scope.
func (s *Store) Open(claimID, scope string) (Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	stagingPath := filepath.Join(s.root, "staging", scope, claimID)
	if err := os.MkdirAll(stagingPath, 0o755); err != nil {
		return Entry{}, fmt.Errorf("atomic-staging.Open: mkdir staging: %w", err)
	}
	entry := Entry{
		ClaimID:       claimID,
		Scope:         scope,
		StagingPath:   stagingPath,
		CanonicalPath: filepath.Join(s.root, "canonical", scope),
		CreatedAt:     time.Now().UTC(),
	}
	if err := s.appendEntry(entry); err != nil {
		_ = os.RemoveAll(stagingPath)
		return Entry{}, err
	}
	return entry, nil
}

// Commit fires the two-rename atomic swap: the canonical path is moved
// aside (if present), the staging path is renamed into place, and the
// aside copy is deleted. The window between the two renames is brief;
// readers that hit it see either the wholly-old or the wholly-new state.
//
// Atomicity / no-data-loss property (the load-bearing guarantee of the
// atomic-staging pattern): no partial state is ever visible at the
// canonical path. The swap is a real os.Rename (move, not copy), so the
// canonical view flips between two whole states. If the install rename
// fails after the canonical view was moved aside, the aside copy is
// restored before returning the error, so a failed Commit leaves the
// previously-committed canonical view intact rather than destroyed.
func (s *Store) Commit(claimID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok, err := s.lookup(claimID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("atomic-staging.Commit: unknown claim_id %q", claimID)
	}
	if err := s.swapIntoCanonical(entry); err != nil {
		return err
	}
	if err := s.removeEntry(claimID); err != nil {
		return fmt.Errorf("atomic-staging.Commit: remove side-table entry: %w", err)
	}
	return nil
}

// swapIntoCanonical performs the atomic move of the staging directory
// into the canonical view. The sequence is:
//
//  1. ensure the canonical parent exists (the scope dir lives under it),
//  2. move any existing canonical view aside to a sibling `.aside` path,
//  3. os.Rename(staging → canonical) — the atomic install,
//  4. delete the aside copy.
//
// Both the aside-move (2) and the install (3) are os.Rename calls; the
// kernel guarantees rename(2) atomicity within one filesystem (asserted
// at New() via assertSameFilesystem). Step 3 is the only window during
// which a reader could observe the canonical path absent; it sees the
// wholly-old view before and the wholly-new view after, never a partial
// one. If step 3 fails, the aside copy is restored (no-data-loss on the
// previously-committed view) before the error propagates.
func (s *Store) swapIntoCanonical(entry Entry) error {
	canonical := entry.CanonicalPath
	if err := os.MkdirAll(filepath.Dir(canonical), 0o755); err != nil {
		return fmt.Errorf("atomic-staging.Commit: mkdir canonical parent: %w", err)
	}

	aside := canonical + ".aside-" + entry.ClaimID
	hadExisting, err := movedAside(canonical, aside)
	if err != nil {
		return fmt.Errorf("atomic-staging.Commit: move canonical aside: %w", err)
	}

	if err := os.Rename(entry.StagingPath, canonical); err != nil {
		// Install failed: restore the previously-committed view so a failed
		// Commit never destroys it.
		if hadExisting {
			_ = os.Rename(aside, canonical)
		}
		return fmt.Errorf("atomic-staging.Commit: install staging into canonical: %w", err)
	}

	if hadExisting {
		if err := os.RemoveAll(aside); err != nil {
			return fmt.Errorf("atomic-staging.Commit: delete aside copy: %w", err)
		}
	}
	return nil
}

// movedAside renames `from` to `aside` when `from` exists, reporting
// whether anything was moved. A missing `from` (the common first-commit
// case, where no canonical view exists yet) is not an error.
func movedAside(from, aside string) (bool, error) {
	if _, err := os.Stat(from); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	if err := os.Rename(from, aside); err != nil {
		return false, err
	}
	return true, nil
}

// Abandon drops the staging area without firing the swap.
func (s *Store) Abandon(claimID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.abandonLocked(claimID)
}

// Release is no-op for `r`; equivalent to Abandon for `rw` that never
// committed. The caller distinguishes by intent.
func (s *Store) Release(claimID, intent string) error {
	if intent == "r" {
		return nil
	}
	return s.Abandon(claimID)
}

// Entries returns the side-table snapshot (used by the sweep loop).
func (s *Store) Entries() ([]Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readAll()
}

// AbandonByClaimID is the sweep-loop entry point: drops staging for a
// leaked claim_id without race-checking the caller's view of the
// live-handle set (the sweep already filtered).
func (s *Store) AbandonByClaimID(claimID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.abandonLocked(claimID)
}

func (s *Store) abandonLocked(claimID string) error {
	entry, ok, err := s.lookup(claimID)
	if err != nil {
		return err
	}
	if !ok {
		return nil // idempotent: already gone
	}
	if err := os.RemoveAll(entry.StagingPath); err != nil {
		return fmt.Errorf("atomic-staging.Abandon: %w", err)
	}
	return s.removeEntry(claimID)
}

// --- side-table primitives (JSONL) ----------------------------------

func (s *Store) appendEntry(e Entry) error {
	all, err := s.readAll()
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	all = append(all, e)
	return s.writeAll(all)
}

func (s *Store) removeEntry(claimID string) error {
	all, err := s.readAll()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	out := make([]Entry, 0, len(all))
	for _, e := range all {
		if e.ClaimID != claimID {
			out = append(out, e)
		}
	}
	return s.writeAll(out)
}

func (s *Store) lookup(claimID string) (Entry, bool, error) {
	all, err := s.readAll()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Entry{}, false, nil
		}
		return Entry{}, false, err
	}
	for _, e := range all {
		if e.ClaimID == claimID {
			return e, true, nil
		}
	}
	return Entry{}, false, nil
}

func (s *Store) readAll() ([]Entry, error) {
	f, err := os.Open(s.state.Path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Entry
	dec := json.NewDecoder(f)
	for dec.More() {
		var e Entry
		if err := dec.Decode(&e); err != nil {
			return nil, fmt.Errorf("atomic-staging.readAll: %w", err)
		}
		out = append(out, e)
	}
	return out, nil
}

func (s *Store) writeAll(all []Entry) error {
	tmp := s.state.Path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	for _, e := range all {
		if err := enc.Encode(e); err != nil {
			_ = f.Close()
			_ = os.Remove(tmp)
			return err
		}
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, s.state.Path)
}
