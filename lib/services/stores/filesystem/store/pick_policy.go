// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Package store: pick-policy logic. Per docs/specs/2026-05-03-
// fs-store-pick-policies-design.md. Auto-discovery + rename-based
// atomic claim. Sentinels live at <store-root>/.fs-store/<policy>/
// {available,in_progress}/.
//
// In-progress sentinel filename: <folder>.<claim_id>.<claimed_nanos>.
// Parsed from the right because folder names may contain dots
// (e.g., "my.docs"); claim_id (UUID) and claimed_nanos (digits-only)
// contain no dots.

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

// parseFromRight splits an in-progress sentinel filename into
// (folder, claim_id, claimed_nanos). The two rightmost dot-separators
// are claim_id and claimed_nanos; everything before is the folder.
//
// claimed_nanos must parse as a non-negative int64 (the typical
// time.Now().UnixNano() value). If it doesn't, the entry is treated
// as malformed and parseFromRight returns an error so callers can
// skip it without misinterpreting it.
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

// policyStateDir returns the absolute path to .fs-store/<policy>/.
// selector is the configured selector key (e.g. "@docs-ring"); the
// directory name strips the leading "@".
func policyStateDir(storeRoot, selector string) string {
	return filepath.Join(storeRoot, ".fs-store", trimAtPrefix(selector))
}

// runSync reconciles available/ against readdir(<store-root>/<policy.Root>/).
// Idempotent and concurrency-safe via pp.syncMu; the actual claim
// rename in openPickPolicy remains lockless (POSIX rename atomicity).
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
		// @deliberate: EEXIST means a concurrent runSync added the
		// sentinel first; both callers want the same end-state so
		// swallow the error rather than fault.
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

// removeDrainedIfPresent unlinks <state>/drained if present. Best-effort;
// ENOENT is benign (no drained sentinel was written). Used to clear the
// single-pass-then-refresh sentinel when new work is observed (sync adds
// a new folder, or sweep reclaims an in-progress sentinel).
func removeDrainedIfPresent(state string) {
	_ = os.Remove(filepath.Join(state, "drained"))
}

// openPickPolicy runs sync (per policy.SyncStrategy) and attempts the
// rename-as-claim. Returns OpenOutcome{Available: false} on empty queue.
//
// Strategy dispatch (spec §5):
//   - on_open: always sync first, then attempt the claim.
//   - on_drain: if available/ is empty, check for the drained sentinel
//     — present means single-pass-then-refresh: consume sentinel and
//     return Unavailable. Absent: run sync. On the last claim, write
//     the sentinel atomically (O_EXCL).
//   - explicit, never: never sync from Open.
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
			// @deliberate: O_EXCL write — EEXIST is benign because a
			// concurrent Open that also observed the empty state may
			// have already written the drained sentinel; either writer
			// satisfies the next Open's read.
			f, ferr := os.OpenFile(drainedPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
			if ferr == nil {
				_ = f.Close()
			}
		}
		return outcome, nil
	}
	// @deliberate: available/ was empty after sync. The on_drain
	// corpus-empty case writes the drained sentinel here so the next
	// Open short-circuits to Unavailable without re-syncing.
	if pp.SyncStrategy == "on_drain" {
		f, ferr := os.OpenFile(drainedPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if ferr == nil {
			_ = f.Close()
		}
	}
	return claimproducer.OpenOutcome{Available: false}, nil
}

// tryRenameClaim is the rename-as-claim loop factored out of
// openPickPolicy. Returns (outcome, wasLastItem, err). wasLastItem
// reports whether available/ became empty as a result of the
// successful claim. It's always false on Unavailable outcomes.
//
// The actual claim (rename) remains lockless and relies on POSIX
// rename atomicity; the lastItem check below re-reads available/
// once per success, which is racy in the sense that a concurrent
// runSync could have inserted a sentinel before the readdir — but
// the worst case is "we miss writing the drained sentinel for one
// claim cycle," which the next Open's empty-available path covers.
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
			// @deliberate: a missing source means another worker won
			// the rename race; advance to the next available entry
			// rather than fault.
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return claimproducer.OpenOutcome{}, false, fmt.Errorf("filesystem store: claim rename: %w", err)
		}
		// @deliberate: re-read available/ to detect last-claim and is
		// best-effort — a readdir error just skips the drained-sentinel
		// write for this cycle, which the next Open's empty-available
		// path will cover.
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

// findByClaimID linearly scans every configured policy's in_progress/
// for a sentinel matching *.<claimID>.*. Returns the first match (claim
// IDs are rimsky-supplied UUIDs unique across all acquisitions; mirrors
// pg's findPolicyForClaim behavior). Returns (nil, "", "", "") if no match.
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

// applyPickAction runs the configured action for a Commit/Abandon path.
// Treats ENOENT on the primary mutation as success (idempotent terminal).
//
// Takes the resolved entry/folder from findByClaimID so this function
// can stay action-only (no extra readdir).
func (s *Store) applyPickAction(pp *PickPolicy, selector, entry, folder string, act action.Action) error {
	inProgDir := filepath.Join(policyStateDir(s.root, selector), "in_progress")
	availDir := filepath.Join(policyStateDir(s.root, selector), "available")
	src := filepath.Join(inProgDir, entry)
	switch act.Kind {
	case action.Pop:
		// @constraint: Pop removes the queue sentinel only; the folder
		// itself stays on disk (Pop is "claim consumed", not "data
		// deleted").
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
		// @constraint: Recycle implements release-to-back FIFO ordering
		// by bumping the sentinel's mtime before moving it back to
		// available/; tryRenameClaim sorts by mtime, so this places the
		// recycled entry at the tail of the queue.
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
