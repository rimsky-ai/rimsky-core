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

	corestore "github.com/fallguy/rimsky/core/store"
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

	// Add brand-new folders.
	for folder := range extant {
		if _, ok := tracked[folder]; ok {
			continue
		}
		f, err := os.OpenFile(filepath.Join(availDir, folder),
			os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			_ = f.Close()
			continue
		}
		if !errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("create available sentinel %s: %w", folder, err)
		}
		// EEXIST: concurrent sync added it. Ignore.
	}

	// Remove stale: folder gone from disk but still has an available sentinel.
	for folder := range avail {
		if _, ok := extant[folder]; ok {
			continue
		}
		if err := os.Remove(filepath.Join(availDir, folder)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("unlink stale available sentinel %s: %w", folder, err)
		}
	}
	return nil
}

// openPickPolicy runs sync (per policy.SyncStrategy) and attempts the
// rename-as-claim. Returns OpenOutcome{Available: false} on empty queue.
func (s *Store) openPickPolicy(claimID, selector string, pp *PickPolicy) (corestore.OpenOutcome, error) {
	if pp.SyncStrategy == "" || pp.SyncStrategy == "on_open" {
		if err := s.runSync(selector, pp); err != nil {
			return corestore.OpenOutcome{}, fmt.Errorf("filesystem store: sync: %w", err)
		}
	}
	state := policyStateDir(s.root, selector)
	availDir := filepath.Join(state, "available")
	inProgDir := filepath.Join(state, "in_progress")

	entries, err := os.ReadDir(availDir)
	if err != nil {
		return corestore.OpenOutcome{}, fmt.Errorf("filesystem store: readdir available: %w", err)
	}
	// Sort by mtime ascending; lexical tiebreaker. If `entries[i].Info()`
	// returns an error (e.g., the entry was unlinked between ReadDir and
	// Info under a high-churn pick policy), the closure falls through to
	// the lexical comparison for that pair. Robustness over perfect
	// FIFO ordering — a stale entry would have been a no-op pick anyway.
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
		// Recompute nowNanos per attempt: if N earlier entries lost
		// the rename race (ENOENT) the suffix should record the
		// actual claim moment, not when the loop began.
		nowNanos := time.Now().UnixNano()
		dst := filepath.Join(inProgDir, fmt.Sprintf("%s.%s.%d", folder, claimID, nowNanos))
		if err := os.Rename(src, dst); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue // raced; try next
			}
			return corestore.OpenOutcome{}, fmt.Errorf("filesystem store: claim rename: %w", err)
		}
		subPath := filepath.Join(pp.Root, folder)
		absPath := filepath.Join(s.root, subPath)
		addr, err := json.Marshal(absPath)
		if err != nil {
			return corestore.OpenOutcome{}, err
		}
		region, err := json.Marshal(subPath)
		if err != nil {
			return corestore.OpenOutcome{}, err
		}
		payload, err := json.Marshal(map[string]string{"folder": folder})
		if err != nil {
			return corestore.OpenOutcome{}, err
		}
		s.mu.Lock()
		s.claims[claimID] = absPath
		s.mu.Unlock()
		return corestore.OpenOutcome{
			Available: true,
			Result: corestore.ClaimResult{
				Address: json.RawMessage(addr),
				Payload: json.RawMessage(payload),
				Region:  json.RawMessage(region),
			},
		}, nil
	}
	return corestore.OpenOutcome{Available: false}, nil
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
func (s *Store) applyPickAction(pp *PickPolicy, selector, entry, folder, action string) error {
	inProgDir := filepath.Join(policyStateDir(s.root, selector), "in_progress")
	availDir := filepath.Join(policyStateDir(s.root, selector), "available")
	src := filepath.Join(inProgDir, entry)
	switch action {
	case "release_to_back":
		now := time.Now()
		if err := os.Chtimes(src, now, now); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("filesystem store: chtimes: %w", err)
		}
		if err := os.Rename(src, filepath.Join(availDir, folder)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("filesystem store: release_to_back rename: %w", err)
		}
		return nil
	case "release_to_head":
		epoch := time.Unix(0, 0)
		if err := os.Chtimes(src, epoch, epoch); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("filesystem store: chtimes (head): %w", err)
		}
		if err := os.Rename(src, filepath.Join(availDir, folder)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("filesystem store: release_to_head rename: %w", err)
		}
		return nil
	case "delete":
		folderAbs := filepath.Join(s.root, pp.Root, folder)
		if err := os.RemoveAll(folderAbs); err != nil {
			return fmt.Errorf("filesystem store: removeall %s: %w", folderAbs, err)
		}
		if err := os.Remove(src); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("filesystem store: unlink in_progress: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("filesystem store: unknown pick action %q", action)
	}
}
