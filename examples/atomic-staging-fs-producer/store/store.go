// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

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

type Store struct {
	root  string
	state stateFile

	mu sync.Mutex
}

type stateFile struct {
	Path string
}

type Entry struct {
	ClaimID       string    `json:"claim_id"`
	Scope         string    `json:"scope"`
	StagingPath   string    `json:"staging_path"`
	CanonicalPath string    `json:"canonical_path"`
	CreatedAt     time.Time `json:"created_at"`
}

var assertSameFilesystemFn = assertSameFilesystem

func New(root string) (*Store, error) {
	if root == "" {
		return nil, errors.New("atomic-staging: root must be non-empty")
	}
	for _, sub := range []string{"staging", "canonical"} {
		if err := os.MkdirAll(filepath.Join(root, sub), 0o755); err != nil {
			return nil, fmt.Errorf("atomic-staging: mkdir %s: %w", sub, err)
		}
	}
	if err := assertSameFilesystemFn(
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

func (s *Store) Open(claimID, scope string) (Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok, err := s.lookup(claimID); err != nil {
		return Entry{}, err
	} else if ok {
		return existing, nil
	}
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

func (s *Store) Commit(claimID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok, err := s.lookup(claimID)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if err := s.swapIntoCanonical(entry); err != nil {
		return err
	}
	if err := s.removeEntry(claimID); err != nil {
		return fmt.Errorf("atomic-staging.Commit: remove side-table entry: %w", err)
	}
	return nil
}

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

func (s *Store) Abandon(claimID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.abandonLocked(claimID)
}

func (s *Store) Release(claimID string) error {
	return s.Abandon(claimID)
}

func (s *Store) Entries() ([]Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readAll()
}

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
		return nil
	}
	if err := os.RemoveAll(entry.StagingPath); err != nil {
		return fmt.Errorf("atomic-staging.Abandon: %w", err)
	}
	return s.removeEntry(claimID)
}

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
