// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// Package store implements the four-verb atomic-staging logic against a
// POSIX filesystem substrate. The pattern doc at
// docs/agents/examples/atomic-staging.md explains the per-substrate
// semantics; this implementation uses two-rename atomic swap on Commit.
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
// aside (if present), the staging path is moved into place, the
// aside copy is deleted. The window between the two renames is
// brief; readers that hit it see either the old or the new state.
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
	asidePath := entry.CanonicalPath + "._old"
	if _, err := os.Stat(entry.CanonicalPath); err == nil {
		if err := os.Rename(entry.CanonicalPath, asidePath); err != nil {
			return fmt.Errorf("atomic-staging.Commit: move-aside: %w", err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(entry.CanonicalPath), 0o755); err != nil {
		return fmt.Errorf("atomic-staging.Commit: mkdir canonical parent: %w", err)
	}
	if err := os.Rename(entry.StagingPath, entry.CanonicalPath); err != nil {
		_ = os.Rename(asidePath, entry.CanonicalPath) // best-effort rollback
		return fmt.Errorf("atomic-staging.Commit: move-into-place: %w", err)
	}
	_ = os.RemoveAll(asidePath)
	if err := s.removeEntry(claimID); err != nil {
		return fmt.Errorf("atomic-staging.Commit: remove side-table entry: %w", err)
	}
	return nil
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
