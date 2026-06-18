// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/action"
	claimproducer "github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
)

func parseFromRight(name string) (folder, claimID string, claimedNanos int64, err error) {
	lastDot := strings.LastIndexByte(name, '.')
	if lastDot < 0 {
		return "", "", 0, errors.New("missing claimed_nanos suffix")
	}
	nanosStr := name[lastDot+1:]
	prev := strings.LastIndexByte(name[:lastDot], '.')
	if prev < 0 {
		return "", "", 0, errors.New("missing claim_id suffix")
	}
	if prev == 0 {
		return "", "", 0, errors.New("empty folder")
	}
	n, parseErr := strconv.ParseInt(nanosStr, 10, 64)
	if parseErr != nil {
		return "", "", 0, errors.New("claimed_nanos is not an integer")
	}
	if n < 0 {
		return "", "", 0, errors.New("claimed_nanos is negative")
	}
	return name[:prev], name[prev+1 : lastDot], n, nil
}

func policyStateDir(storeRoot, selector string) string {
	return filepath.Join(storeRoot, ".fs-store", trimAtPrefix(selector))
}

func (s *Store) runSync(selector string, pp *PickPolicy) error {
	pp.syncMu.Lock()
	defer pp.syncMu.Unlock()
	subRoot := filepath.Join(s.root, pp.Root)
	state := policyStateDir(s.root, selector)
	availDir := filepath.Join(state, "available")
	inProgDir := filepath.Join(state, "in_progress")

	extantEntries, err := os.ReadDir(subRoot)
	if err != nil {
		return fmt.Errorf("readdir %s: %w", subRoot, err)
	}
	extant := make(map[string]struct{}, len(extantEntries))
	for _, e := range extantEntries {
		name := e.Name()
		if !e.IsDir() {
			continue
		}
		if strings.HasPrefix(name, ".") {
			continue
		}
		if pp.FolderPattern != nil && !pp.FolderPattern.MatchString(name) {
			continue
		}
		extant[name] = struct{}{}
	}

	availEntries, err := os.ReadDir(availDir)
	if err != nil {
		return fmt.Errorf("readdir %s: %w", availDir, err)
	}
	avail := make(map[string]struct{}, len(availEntries))
	for _, e := range availEntries {
		avail[e.Name()] = struct{}{}
	}

	inProgEntries, err := os.ReadDir(inProgDir)
	if err != nil {
		return fmt.Errorf("readdir %s: %w", inProgDir, err)
	}
	tracked := make(map[string]struct{}, len(avail)+len(inProgEntries))
	for k := range avail {
		tracked[k] = struct{}{}
	}
	for _, e := range inProgEntries {
		folder, _, _, perr := parseFromRight(e.Name())
		if perr != nil {
			continue
		}
		tracked[folder] = struct{}{}
	}

	addedAny := false
	for folder := range extant {
		if _, ok := tracked[folder]; ok {
			continue
		}
		f, err := os.OpenFile(filepath.Join(availDir, folder),
			os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			_ = f.Close()
			addedAny = true
			continue
		}
		if !errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("create available sentinel %s: %w", folder, err)
		}
	}

	for folder := range avail {
		if _, ok := extant[folder]; ok {
			continue
		}
		if err := os.Remove(filepath.Join(availDir, folder)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("unlink stale available sentinel %s: %w", folder, err)
		}
	}
	if addedAny {
		removeDrainedIfPresent(state)
	}
	return nil
}

func removeDrainedIfPresent(state string) {
	_ = os.Remove(filepath.Join(state, "drained"))
}

func (s *Store) openPickPolicy(claimID, selector string, pp *PickPolicy) (claimproducer.OpenOutcome, error) {
	state := policyStateDir(s.root, selector)
	availDir := filepath.Join(state, "available")
	inProgDir := filepath.Join(state, "in_progress")
	drainedPath := filepath.Join(state, "drained")

	switch pp.SyncStrategy {
	case "on_open":
		if err := s.runSync(selector, pp); err != nil {
			return claimproducer.OpenOutcome{}, fmt.Errorf("filesystem store: sync: %w", err)
		}
	case "on_drain":
		empty, err := isDirEmpty(availDir)
		if err != nil {
			return claimproducer.OpenOutcome{}, fmt.Errorf("filesystem store: readdir available: %w", err)
		}
		if empty {
			if drainedFileExists(drainedPath) {
				if err := os.Remove(drainedPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
					return claimproducer.OpenOutcome{}, fmt.Errorf("filesystem store: remove drained: %w", err)
				}
				return claimproducer.OpenOutcome{Available: false}, nil
			}
			if err := s.runSync(selector, pp); err != nil {
				return claimproducer.OpenOutcome{}, fmt.Errorf("filesystem store: sync: %w", err)
			}
		}
	case "explicit", "never":
	default:
		return claimproducer.OpenOutcome{}, fmt.Errorf("filesystem store: invalid sync_strategy %q", pp.SyncStrategy)
	}

	outcome, lastItem, err := s.tryRenameClaim(claimID, selector, pp, availDir, inProgDir)
	if err != nil {
		return claimproducer.OpenOutcome{}, err
	}
	if outcome.Available {
		if pp.SyncStrategy == "on_drain" && lastItem {
			f, ferr := os.OpenFile(drainedPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
			if ferr == nil {
				_ = f.Close()
			}
		}
		return outcome, nil
	}
	if pp.SyncStrategy == "on_drain" {
		f, ferr := os.OpenFile(drainedPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if ferr == nil {
			_ = f.Close()
		}
	}
	return claimproducer.OpenOutcome{Available: false}, nil
}

func (s *Store) tryRenameClaim(claimID, selector string, pp *PickPolicy, availDir, inProgDir string) (claimproducer.OpenOutcome, bool, error) {
	entries, err := os.ReadDir(availDir)
	if err != nil {
		return claimproducer.OpenOutcome{}, false, fmt.Errorf("filesystem store: readdir available: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool {
		ii, _ := entries[i].Info()
		jj, _ := entries[j].Info()
		if ii != nil && jj != nil && !ii.ModTime().Equal(jj.ModTime()) {
			return ii.ModTime().Before(jj.ModTime())
		}
		return entries[i].Name() < entries[j].Name()
	})

	for _, entry := range entries {
		folder := entry.Name()
		src := filepath.Join(availDir, folder)
		nowNanos := time.Now().UnixNano()
		dst := filepath.Join(inProgDir, fmt.Sprintf("%s.%s.%d", folder, claimID, nowNanos))
		if err := os.Rename(src, dst); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return claimproducer.OpenOutcome{}, false, fmt.Errorf("filesystem store: claim rename: %w", err)
		}
		remaining, _ := os.ReadDir(availDir)
		lastItem := len(remaining) == 0

		subPath := filepath.Join(pp.Root, folder)
		absPath := filepath.Join(s.root, subPath)
		addr, err := json.Marshal(absPath)
		if err != nil {
			return claimproducer.OpenOutcome{}, false, err
		}
		scope, err := json.Marshal(subPath)
		if err != nil {
			return claimproducer.OpenOutcome{}, false, err
		}
		payload, err := json.Marshal(map[string]string{"folder": folder})
		if err != nil {
			return claimproducer.OpenOutcome{}, false, err
		}
		s.mu.Lock()
		s.claims[claimID] = absPath
		s.mu.Unlock()
		s.ledger.RecordOpen(claimID, selector, addr, scope)
		return claimproducer.OpenOutcome{
			Available: true,
			Result: claimproducer.ClaimResult{
				Address:                json.RawMessage(addr),
				Payload:                json.RawMessage(payload),
				ClaimScope:             json.RawMessage(scope),
				RealizedWriteSemantics: claimproducer.WriteSemanticsSync,
			},
		}, lastItem, nil
	}
	return claimproducer.OpenOutcome{Available: false}, false, nil
}

func isDirEmpty(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, err
	}
	return len(entries) == 0, nil
}

func drainedFileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func (s *Store) findByClaimID(claimID string) (pp *PickPolicy, selector, entry, folder string) {
	for sel, candidate := range s.pickPolicies {
		inProg := filepath.Join(policyStateDir(s.root, sel), "in_progress")
		entries, err := os.ReadDir(inProg)
		if err != nil {
			continue
		}
		for _, e := range entries {
			f, c, _, perr := parseFromRight(e.Name())
			if perr != nil || c != claimID {
				continue
			}
			return candidate, sel, e.Name(), f
		}
	}
	return nil, "", "", ""
}

func (s *Store) applyPickAction(pp *PickPolicy, selector, entry, folder string, act action.Action) error {
	inProgDir := filepath.Join(policyStateDir(s.root, selector), "in_progress")
	availDir := filepath.Join(policyStateDir(s.root, selector), "available")
	src := filepath.Join(inProgDir, entry)
	switch act.Kind {
	case action.Pop:
		if err := os.Remove(src); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("filesystem store: pop unlink in_progress: %w", err)
		}
		return nil
	case action.PopAndMove:
		folderAbs := filepath.Join(s.root, pp.Root, folder)
		targetAbs := filepath.Join(s.root, act.MoveTarget, folder)
		if err := os.Rename(folderAbs, targetAbs); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("filesystem store: pop_and_move rename %q→%q: %w",
				folderAbs, targetAbs, err)
		}
		if err := os.Remove(src); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("filesystem store: pop_and_move unlink in_progress: %w", err)
		}
		return nil
	case action.PopAndDelete:
		folderAbs := filepath.Join(s.root, pp.Root, folder)
		if err := os.RemoveAll(folderAbs); err != nil {
			return fmt.Errorf("filesystem store: pop_and_delete removeall %s: %w", folderAbs, err)
		}
		if err := os.Remove(src); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("filesystem store: pop_and_delete unlink in_progress: %w", err)
		}
		return nil
	case action.Recycle:
		now := time.Now()
		if err := os.Chtimes(src, now, now); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("filesystem store: recycle chtimes: %w", err)
		}
		if err := os.Rename(src, filepath.Join(availDir, folder)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("filesystem store: recycle rename: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("filesystem store: unknown pick action %q", act.Kind)
	}
}
