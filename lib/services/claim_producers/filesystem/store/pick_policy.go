// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/action"
	claimproducer "github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
)

const batchLeaseIDPrefix = "batchlease~"

func fsyncDir(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		slog.Warn("FILESYSTEMSTORE.DIRFSYNC.OPENFAILED", "dir", dir, "error", err.Error())
		return
	}
	defer func() { _ = d.Close() }()
	if err := d.Sync(); err != nil {
		slog.Warn("FILESYSTEMSTORE.DIRFSYNC.FAILED", "dir", dir, "error", err.Error())
	}
}

func ParseFromRight(name string) (folder, claimID string, claimedNanos int64, err error) {
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

const fsStoreStateDirName = ".fs-store"

const ringPositionHead int64 = 0

func stampRingPosition(sentinelPath string, position int64) error {
	f, err := os.OpenFile(sentinelPath, os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	_, werr := f.WriteString(strconv.FormatInt(position, 10))
	cerr := f.Close()
	if werr != nil {
		return werr
	}
	return cerr
}

func ringPosition(sentinelPath string) int64 {
	raw, err := os.ReadFile(sentinelPath)
	if err != nil {
		return ringPositionHead
	}
	pos, err := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
	if err != nil {
		return ringPositionHead
	}
	return pos
}

func PolicyStateDir(storeRoot, selector string) string {
	return filepath.Join(storeRoot, fsStoreStateDirName, trimAtPrefix(selector))
}

func (s *Store) runSync(selector string, pp *PickPolicy) error {
	pp.syncMu.Lock()
	defer pp.syncMu.Unlock()
	subRoot := filepath.Join(s.root, pp.Root)
	state := PolicyStateDir(s.root, selector)
	availDir := filepath.Join(state, "available")
	inProgDir := filepath.Join(state, "in_progress")

	extantEntries, err := os.ReadDir(subRoot)
	if err != nil {
		return fmt.Errorf("readdir %s: %w", subRoot, err)
	}
	extant := make(map[string]struct{}, len(extantEntries))
	for _, e := range extantEntries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		isDir := e.IsDir()
		if !isDir && e.Type()&fs.ModeSymlink != 0 {
			if info, statErr := os.Stat(filepath.Join(subRoot, name)); statErr == nil && info.IsDir() {
				isDir = true
			}
		}
		if !isDir {
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
		folder, _, _, perr := ParseFromRight(e.Name())
		if perr != nil {
			continue
		}
		tracked[folder] = struct{}{}
	}

	addedAny := false
	changedAny := false
	for folder := range extant {
		if _, ok := tracked[folder]; ok {
			continue
		}
		f, err := os.OpenFile(filepath.Join(availDir, folder),
			os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			_, werr := f.WriteString(strconv.FormatInt(time.Now().UnixNano(), 10))
			cerr := f.Close()
			if werr != nil {
				return fmt.Errorf("stamp available sentinel %s: %w", folder, werr)
			}
			if cerr != nil {
				return fmt.Errorf("close available sentinel %s: %w", folder, cerr)
			}
			addedAny = true
			changedAny = true
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
		changedAny = true
	}
	if changedAny {
		fsyncDir(availDir)
	}
	if addedAny {
		removeDrainedIfPresent(state)
	}
	return nil
}

func removeDrainedIfPresent(state string) {
	drainedPath := filepath.Join(state, "drained")
	if err := os.Remove(drainedPath); err == nil {
		fsyncDir(state)
	}
}

func (s *Store) openPickPolicy(claimID, selector string, pp *PickPolicy) (claimproducer.OpenOutcome, error) {
	state := PolicyStateDir(s.root, selector)
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
				fsyncDir(state)
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
			createDrainedSentinel(drainedPath)
		}
		return outcome, nil
	}
	if pp.SyncStrategy == "on_drain" {
		createDrainedSentinel(drainedPath)
	}
	return claimproducer.OpenOutcome{Available: false}, nil
}

func createDrainedSentinel(drainedPath string) {
	f, err := os.OpenFile(drainedPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err == nil {
		_ = f.Close()
		fsyncDir(filepath.Dir(drainedPath))
	}
}

type claimedFolder struct {
	folder       string
	absPath      string
	subPath      string
	addr         []byte
	scope        []byte
	payload      []byte
	claimedNanos int64
}

func (s *Store) claimNextAvailable(availDir, inProgDir string, pp *PickPolicy, claimFileID string) (claimedFolder, bool, error) {
	entries, err := os.ReadDir(availDir)
	if err != nil {
		return claimedFolder{}, false, fmt.Errorf("filesystem store: readdir available: %w", err)
	}
	type rankedSentinel struct {
		name     string
		position int64
	}
	ranked := make([]rankedSentinel, 0, len(entries))
	for _, entry := range entries {
		ranked = append(ranked, rankedSentinel{
			name:     entry.Name(),
			position: ringPosition(filepath.Join(availDir, entry.Name())),
		})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].position != ranked[j].position {
			return ranked[i].position < ranked[j].position
		}
		return ranked[i].name < ranked[j].name
	})

	for _, entry := range ranked {
		folder := entry.name
		src := filepath.Join(availDir, folder)
		subPath := filepath.Join(pp.Root, folder)
		absPath := filepath.Join(s.root, subPath)
		if _, statErr := os.Stat(absPath); statErr != nil {
			if rmErr := os.Remove(src); rmErr != nil && !errors.Is(rmErr, fs.ErrNotExist) {
				return claimedFolder{}, false, fmt.Errorf("filesystem store: unlink orphan available sentinel %q: %w", folder, rmErr)
			}
			continue
		}
		nowNanos := time.Now().UnixNano()
		dst := filepath.Join(inProgDir, fmt.Sprintf("%s.%s.%d", folder, claimFileID, nowNanos))
		if err := os.Rename(src, dst); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return claimedFolder{}, false, fmt.Errorf("filesystem store: claim rename: %w", err)
		}
		fsyncDir(availDir)
		fsyncDir(inProgDir)
		addr, err := json.Marshal(absPath)
		if err != nil {
			return claimedFolder{}, false, err
		}
		scope, err := s.scopeBytes(subPath)
		if err != nil {
			return claimedFolder{}, false, err
		}
		payload, err := json.Marshal(map[string]string{"folder": folder})
		if err != nil {
			return claimedFolder{}, false, err
		}
		return claimedFolder{
			folder:       folder,
			absPath:      absPath,
			subPath:      subPath,
			addr:         addr,
			scope:        scope,
			payload:      payload,
			claimedNanos: nowNanos,
		}, true, nil
	}
	return claimedFolder{}, false, nil
}

func (s *Store) tryRenameClaim(claimID, selector string, pp *PickPolicy, availDir, inProgDir string) (claimproducer.OpenOutcome, bool, error) {
	claimed, ok, err := s.claimNextAvailable(availDir, inProgDir, pp, claimID)
	if err != nil {
		return claimproducer.OpenOutcome{}, false, err
	}
	if !ok {
		return claimproducer.OpenOutcome{Available: false}, false, nil
	}

	remaining, _ := os.ReadDir(availDir)
	lastItem := len(remaining) == 0

	s.mu.Lock()
	s.claims[claimID] = claimed.absPath
	s.mu.Unlock()
	s.ledger.RecordOpen(claimID, selector, claimed.addr, claimed.scope)
	return claimproducer.OpenOutcome{
		Available: true,
		Result: claimproducer.ClaimResult{
			Address:                json.RawMessage(claimed.addr),
			Payload:                json.RawMessage(claimed.payload),
			ClaimScope:             json.RawMessage(claimed.scope),
			RealizedWriteSemantics: claimproducer.WriteSemanticsSync,
		},
	}, lastItem, nil
}

type PickedItem struct {
	ClaimID         string
	Folder          string
	AbsPath         string
	SubPath         string
	AddressBytes    []byte
	ClaimScopeBytes []byte
	PayloadBytes    []byte
	LeaseToken      string
}

func batchLeaseToken(claimID string, claimedNanos int64) string {
	return fmt.Sprintf("%s%s.%d", batchLeaseIDPrefix, claimID, claimedNanos)
}

func entryLeaseToken(claimID string, claimedNanos int64) (string, bool) {
	if !strings.HasPrefix(claimID, batchLeaseIDPrefix) {
		return "", false
	}
	return fmt.Sprintf("%s.%d", claimID, claimedNanos), true
}

func (s *Store) BatchPop(_ context.Context, selector string, claimIDs []string) ([]PickedItem, error) {
	if len(claimIDs) == 0 {
		return nil, newValidationError("filesystem store: BatchPop: claimIDs must be non-empty (one distinct id per item to pop)")
	}
	seen := make(map[string]struct{}, len(claimIDs))
	for i, id := range claimIDs {
		if err := validateClaimID(id); err != nil {
			return nil, newValidationError("filesystem store: BatchPop: claimIDs[%d]: %w", i, err)
		}
		if _, dup := seen[id]; dup {
			return nil, newValidationError("filesystem store: BatchPop: claimIDs[%d] = %q duplicates a prior entry; every id must be unique", i, id)
		}
		seen[id] = struct{}{}
	}
	if err := s.checkRootAvailable("BatchPop", true); err != nil {
		return nil, err
	}
	pp, ok := s.pickPolicies[selector]
	if !ok {
		return nil, newValidationError("filesystem store: BatchPop: unknown pick policy %q", selector)
	}
	state := PolicyStateDir(s.root, selector)
	availDir := filepath.Join(state, "available")
	inProgDir := filepath.Join(state, "in_progress")
	drainedPath := filepath.Join(state, "drained")

	switch pp.SyncStrategy {
	case "on_open":
		if err := s.runSync(selector, pp); err != nil {
			return nil, fmt.Errorf("filesystem store: BatchPop sync: %w", err)
		}
	case "on_drain":
		empty, err := isDirEmpty(availDir)
		if err != nil {
			return nil, fmt.Errorf("filesystem store: BatchPop readdir available: %w", err)
		}
		if empty {
			if drainedFileExists(drainedPath) {
				if err := os.Remove(drainedPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
					return nil, fmt.Errorf("filesystem store: BatchPop remove drained: %w", err)
				}
				fsyncDir(state)
				return nil, nil
			}
			if err := s.runSync(selector, pp); err != nil {
				return nil, fmt.Errorf("filesystem store: BatchPop sync: %w", err)
			}
		}
	}

	out := make([]PickedItem, 0, len(claimIDs))
	for _, claimID := range claimIDs {
		item, ok, err := s.popOne(claimID, selector, pp, availDir, inProgDir)
		if err != nil {
			return out, err
		}
		if !ok {
			break
		}
		out = append(out, item)
	}
	if pp.SyncStrategy == "on_drain" {
		if empty, err := isDirEmpty(availDir); err == nil && empty {
			createDrainedSentinel(drainedPath)
		}
	}
	return out, nil
}

func (s *Store) popOne(claimID, selector string, pp *PickPolicy, availDir, inProgDir string) (PickedItem, bool, error) {
	claimed, ok, err := s.claimNextAvailable(availDir, inProgDir, pp, batchLeaseIDPrefix+claimID)
	if err != nil {
		return PickedItem{}, false, err
	}
	if !ok {
		return PickedItem{}, false, nil
	}
	s.ledger.RecordOpen(claimID, selector, claimed.addr, claimed.scope)
	return PickedItem{
		ClaimID:         claimID,
		Folder:          claimed.folder,
		AbsPath:         claimed.absPath,
		SubPath:         claimed.subPath,
		AddressBytes:    claimed.addr,
		ClaimScopeBytes: claimed.scope,
		PayloadBytes:    claimed.payload,
		LeaseToken:      batchLeaseToken(claimID, claimed.claimedNanos),
	}, true, nil
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
		inProg := filepath.Join(PolicyStateDir(s.root, sel), "in_progress")
		entries, err := os.ReadDir(inProg)
		if err != nil {
			continue
		}
		for _, e := range entries {
			f, c, _, perr := ParseFromRight(e.Name())
			if perr != nil || c != claimID {
				continue
			}
			return candidate, sel, e.Name(), f
		}
	}
	return nil, "", "", ""
}

func (s *Store) findByScope(scope []byte, leaseToken string) (pp *PickPolicy, selector, entry, folder string) {
	if len(scope) == 0 {
		return nil, "", "", ""
	}
	var subPath string
	if err := json.Unmarshal(scope, &subPath); err != nil {
		return nil, "", "", ""
	}
	if subPath == "" {
		return nil, "", "", ""
	}
	fold := func(p string) string {
		if s.caseFold {
			return strings.ToLower(p)
		}
		return p
	}
	for sel, candidate := range s.pickPolicies {
		policyRoot := fold(candidate.Root)
		var wantFolder string
		switch {
		case subPath == policyRoot:
			continue
		case strings.HasPrefix(subPath, policyRoot+string(filepath.Separator)):
			wantFolder = subPath[len(policyRoot)+1:]
		case policyRoot == "" || policyRoot == ".":
			wantFolder = subPath
		default:
			continue
		}
		if strings.ContainsRune(wantFolder, filepath.Separator) {
			continue
		}
		inProg := filepath.Join(PolicyStateDir(s.root, sel), "in_progress")
		entries, err := os.ReadDir(inProg)
		if err != nil {
			continue
		}
		for _, e := range entries {
			f, c, nanos, perr := ParseFromRight(e.Name())
			if perr != nil || fold(f) != wantFolder || !strings.HasPrefix(c, batchLeaseIDPrefix) {
				continue
			}
			if leaseToken != "" {
				token, ok := entryLeaseToken(c, nanos)
				if !ok || token != leaseToken {
					continue
				}
			}
			return candidate, sel, e.Name(), f
		}
	}
	return nil, "", "", ""
}

func (s *Store) applyPickAction(pp *PickPolicy, selector, entry, folder string, act action.Action) error {
	state := PolicyStateDir(s.root, selector)
	inProgDir := filepath.Join(state, "in_progress")
	availDir := filepath.Join(state, "available")
	src := filepath.Join(inProgDir, entry)
	switch act.Kind {
	case action.Pop:
		if err := os.Remove(src); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("filesystem store: pop unlink in_progress: %w", err)
		}
		fsyncDir(inProgDir)
		return nil
	case action.PopAndMove:
		folderAbs := filepath.Join(s.root, pp.Root, folder)
		targetAbs := filepath.Join(s.root, act.MoveTarget, folder)
		if err := os.Remove(src); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("filesystem store: pop_and_move unlink in_progress: %w", err)
		}
		fsyncDir(inProgDir)
		if err := os.Rename(folderAbs, targetAbs); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("filesystem store: pop_and_move rename %q→%q: %w",
				folderAbs, targetAbs, err)
		}
		fsyncDir(filepath.Dir(folderAbs))
		fsyncDir(filepath.Dir(targetAbs))
		return nil
	case action.PopAndDelete:
		folderAbs := filepath.Join(s.root, pp.Root, folder)
		if err := os.Remove(src); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("filesystem store: pop_and_delete unlink in_progress: %w", err)
		}
		fsyncDir(inProgDir)
		if err := os.RemoveAll(folderAbs); err != nil {
			return fmt.Errorf("filesystem store: pop_and_delete removeall %s: %w", folderAbs, err)
		}
		fsyncDir(filepath.Dir(folderAbs))
		return nil
	case action.Recycle:
		if err := stampRingPosition(src, time.Now().UnixNano()); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("filesystem store: recycle stamp ring position: %w", err)
		}
		if err := os.Rename(src, filepath.Join(availDir, folder)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("filesystem store: recycle rename: %w", err)
		}
		fsyncDir(inProgDir)
		fsyncDir(availDir)
		removeDrainedIfPresent(state)
		return nil
	default:
		return fmt.Errorf("filesystem store: unknown pick action %q", act.Kind)
	}
}
