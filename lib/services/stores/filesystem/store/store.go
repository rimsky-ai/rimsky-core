// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/action"
	claimproducer "github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
)

type PickPolicy struct {
	Root              string
	FolderPattern     *regexp.Regexp
	OnCommit          action.Action
	OnGiveUp          action.Action
	VisibilityTimeout time.Duration
	SyncStrategy      string

	syncMu sync.Mutex
}

type Config struct {
	Root         string
	PickPolicies map[string]*PickPolicy
}

type Store struct {
	root         string
	pickPolicies map[string]*PickPolicy
	mu           sync.Mutex

	claims map[string]string

	ledger *ClaimLedger
}

func (s *Store) Ledger() *ClaimLedger { return s.ledger }

func New(cfg Config) (*Store, error) {
	if cfg.Root == "" {
		return nil, errors.New("filesystem store: root must not be empty")
	}
	for selector, pp := range cfg.PickPolicies {
		res := validatePickPolicy(cfg.Root, selector, pp)
		if !res.OK() {
			msgs := make([]string, 0, len(res.Errors))
			for _, e := range res.Errors {
				msgs = append(msgs, e.Error())
			}
			return nil, fmt.Errorf("filesystem store: pick_policies[%q]: %s",
				selector, strings.Join(msgs, "; "))
		}
		for _, w := range res.Warnings {
			slog.Warn(w)
		}
		dir := filepath.Join(cfg.Root, ".fs-store", trimAtPrefix(selector))
		for _, sub := range []string{"available", "in_progress"} {
			if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
				return nil, fmt.Errorf("filesystem store: mkdir %s: %w", filepath.Join(dir, sub), err)
			}
		}
	}
	return &Store{
		root:         cfg.Root,
		pickPolicies: cfg.PickPolicies,
		claims:       make(map[string]string),
		ledger:       NewClaimLedger(1024),
	}, nil
}

func trimAtPrefix(selector string) string {
	if strings.HasPrefix(selector, "@") {
		return selector[1:]
	}
	return selector
}

func (s *Store) Capabilities() claimproducer.Capabilities {
	return claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
	}
}

func (s *Store) Open(_ context.Context, claimID, selector string) (claimproducer.OpenOutcome, error) {
	if err := s.checkRootAvailable("Open"); err != nil {
		return claimproducer.OpenOutcome{}, err
	}
	if pp, ok := s.pickPolicies[selector]; ok {
		return s.openPickPolicy(claimID, selector, pp)
	}
	return s.openScoped(claimID, selector)
}

func (s *Store) openScoped(claimID, selector string) (claimproducer.OpenOutcome, error) {
	raw := strings.TrimSpace(selector)
	if raw == "" {
		return claimproducer.OpenOutcome{}, errors.New("filesystem store: Open: empty selector")
	}
	if hasGlobMeta(raw) {
		return claimproducer.OpenOutcome{}, fmt.Errorf(
			"filesystem store: Open: selector %q contains glob metacharacters; v3 standard filesystem supports concrete paths only",
			raw)
	}
	cleaned := filepath.Clean(strings.TrimPrefix(raw, "./"))
	if filepath.IsAbs(cleaned) {
		return claimproducer.OpenOutcome{}, fmt.Errorf(
			"filesystem store: Open: selector %q is absolute; selectors must be relative paths under the configured root", raw)
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return claimproducer.OpenOutcome{}, fmt.Errorf(
			"filesystem store: Open: selector %q escapes the configured root", raw)
	}
	if cleaned == "." || cleaned == "" {
		return claimproducer.OpenOutcome{}, fmt.Errorf(
			"filesystem store: Open: selector %q resolves to the root itself; selectors must name a concrete entry", raw)
	}

	scopeBytes, err := json.Marshal(cleaned)
	if err != nil {
		return claimproducer.OpenOutcome{}, fmt.Errorf("filesystem store: marshal scope: %w", err)
	}
	addrPath := filepath.Join(s.root, cleaned)
	rel, relErr := filepath.Rel(s.root, addrPath)
	if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return claimproducer.OpenOutcome{}, fmt.Errorf(
			"filesystem store: Open: selector %q resolved to %q which escapes the configured root %q", raw, addrPath, s.root)
	}
	addrBytes, err := json.Marshal(addrPath)
	if err != nil {
		return claimproducer.OpenOutcome{}, fmt.Errorf("filesystem store: marshal address: %w", err)
	}

	s.mu.Lock()
	s.claims[claimID] = addrPath
	s.mu.Unlock()

	s.ledger.RecordOpen(claimID, raw, addrBytes, scopeBytes)

	return claimproducer.OpenOutcome{
		Available: true,
		Result: claimproducer.ClaimResult{
			Address:                json.RawMessage(addrBytes),
			ClaimScope:             json.RawMessage(scopeBytes),
			RealizedWriteSemantics: claimproducer.WriteSemanticsSync,
		},
	}, nil
}

func (s *Store) Commit(_ context.Context, claimID string, _ []byte, _ []byte) error {
	if err := s.checkRootAvailable("Commit"); err != nil {
		return err
	}
	pp, sel, entry, folder := s.findByClaimID(claimID)
	s.mu.Lock()
	delete(s.claims, claimID)
	s.mu.Unlock()
	if pp != nil {
		if err := s.applyPickAction(pp, sel, entry, folder, pp.OnCommit); err != nil {
			s.ledger.RecordEvent(claimID, "claim_commit_failed", "ERROR", map[string]any{"error": err.Error()})
			return err
		}
	}
	s.ledger.RecordTerminal(claimID, "claim_committed", nil)
	return nil
}

func (s *Store) Abandon(_ context.Context, claimID string, _ []byte, _ []byte) error {
	if err := s.checkRootAvailable("Abandon"); err != nil {
		return err
	}
	pp, sel, entry, folder := s.findByClaimID(claimID)
	s.mu.Lock()
	delete(s.claims, claimID)
	s.mu.Unlock()
	if pp != nil {
		if err := s.applyPickAction(pp, sel, entry, folder, pp.OnGiveUp); err != nil {
			s.ledger.RecordEvent(claimID, "claim_abandon_failed", "ERROR", map[string]any{"error": err.Error()})
			return err
		}
	}
	s.ledger.RecordTerminal(claimID, "claim_abandoned", nil)
	return nil
}

func (s *Store) Release(_ context.Context, claimID string, _ []byte, _ []byte) error {
	if err := s.checkRootAvailable("Release"); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.claims, claimID)
	s.mu.Unlock()
	s.ledger.RecordTerminal(claimID, "claim_released", nil)
	return nil
}

func (s *Store) checkRootAvailable(verb string) error {
	if _, err := os.Stat(s.root); err != nil {
		return &ClassedError{Class: RootUnavailableClass, Err: fmt.Errorf(
			"filesystem store: %s: configured root %q is not accessible: %v", verb, s.root, err)}
	}
	if err := syscall.Access(s.root, 0x2); err != nil {
		return &ClassedError{Class: RootUnavailableClass, Err: fmt.Errorf(
			"filesystem store: %s: configured root %q is not writable: %v", verb, s.root, err)}
	}
	return nil
}

func hasGlobMeta(s string) bool {
	return strings.ContainsAny(s, "*?[")
}

func validatePickPolicy(storeRoot, selector string, pp *PickPolicy) action.ValidationResult {
	var res action.ValidationResult
	addErr := func(err error) { res.Errors = append(res.Errors, err) }
	addWarn := func(w string) { res.Warnings = append(res.Warnings, w) }

	if pp == nil {
		addErr(errors.New("policy is nil"))
		return res
	}
	if filepath.IsAbs(pp.Root) {
		addErr(fmt.Errorf("root: %q is absolute; must be relative to store root", pp.Root))
	} else {
		cleaned := filepath.Clean(pp.Root)
		if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
			addErr(fmt.Errorf("root: %q escapes the store root", pp.Root))
		} else {
			absPath := filepath.Join(storeRoot, cleaned)
			rel, relErr := filepath.Rel(storeRoot, absPath)
			if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				addErr(fmt.Errorf("root: %q resolves to %q which escapes the store root", pp.Root, absPath))
			} else if info, err := os.Stat(absPath); err != nil {
				addErr(fmt.Errorf("root: stat %s: %w", absPath, err))
			} else if !info.IsDir() {
				addErr(fmt.Errorf("root: %s is not a directory", absPath))
			} else if _, err := os.ReadDir(absPath); err != nil {
				addErr(fmt.Errorf("root: %s not readable: %w", absPath, err))
			} else {
				probe, perr := os.CreateTemp(absPath, ".rimsky-fs-store-probe-*")
				if perr != nil {
					addErr(fmt.Errorf("root: %s not writable: %w", absPath, perr))
				} else {
					probeName := probe.Name()
					_ = probe.Close()
					_ = os.Remove(probeName)
				}
			}
		}
	}

	if pp.OnCommit.Kind == "" {
		addErr(errors.New("on_commit: required (got null or missing)"))
	} else if err := pp.OnCommit.Validate(); err != nil {
		addErr(fmt.Errorf("on_commit: %w", err))
	}
	if pp.OnGiveUp.Kind == "" {
		addErr(errors.New("on_give_up: required (got null or missing)"))
	} else if err := pp.OnGiveUp.Validate(); err != nil {
		addErr(fmt.Errorf("on_give_up: %w", err))
	}

	if pp.OnCommit.Kind == action.PopAndMove && pp.OnCommit.MoveTarget != "" {
		if err := validateMoveTargetSameFS(storeRoot, pp.Root, pp.OnCommit.MoveTarget); err != nil {
			addErr(fmt.Errorf("on_commit: pop_and_move: %w", err))
		}
	}
	if pp.OnGiveUp.Kind == action.PopAndMove && pp.OnGiveUp.MoveTarget != "" {
		if err := validateMoveTargetSameFS(storeRoot, pp.Root, pp.OnGiveUp.MoveTarget); err != nil {
			addErr(fmt.Errorf("on_give_up: pop_and_move: %w", err))
		}
	}

	if pp.VisibilityTimeout <= 0 {
		addErr(errors.New("visibility_timeout_seconds: must be > 0"))
	}

	switch pp.SyncStrategy {
	case "":
		pp.SyncStrategy = "on_open"
	case "on_open", "on_drain", "explicit", "never":
	default:
		addErr(fmt.Errorf("sync_strategy: must be on_open|on_drain|explicit|never, got %q", pp.SyncStrategy))
	}

	if pp.OnCommit.Kind == action.Pop && pp.SyncStrategy == "on_open" {
		addErr(errors.New("on_commit: pop is incompatible with sync_strategy: on_open (queue would never drain because runSync re-adds popped folders); use sync_strategy: on_drain"))
	}

	if pp.OnCommit.Kind == action.Recycle && pp.SyncStrategy == "on_drain" {
		addWarn(fmt.Sprintf("filesystem store: pick_policies[%q]: recycle + sync_strategy: on_drain is inert (queue never empties; on_drain never fires)", selector))
	}

	_ = selector
	return res
}

func validateMoveTargetContained(storeRoot, target string) error {
	if target == "" {
		return errors.New("target must be non-empty")
	}
	if filepath.IsAbs(target) {
		return fmt.Errorf("target %q is absolute; must be relative to store root", target)
	}
	cleaned := filepath.Clean(target)
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return fmt.Errorf("target %q escapes the store root", target)
	}
	absPath := filepath.Join(storeRoot, cleaned)
	rel, relErr := filepath.Rel(storeRoot, absPath)
	if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("target %q resolves to %q which escapes the store root", target, absPath)
	}
	return nil
}

func validateMoveTargetSameFS(storeRoot, policyRoot, target string) error {
	if err := validateMoveTargetContained(storeRoot, target); err != nil {
		return err
	}
	policyAbs, err := filepath.Abs(filepath.Join(storeRoot, policyRoot))
	if err != nil {
		return fmt.Errorf("resolve policy root: %w", err)
	}
	targetAbs, err := filepath.Abs(filepath.Join(storeRoot, target))
	if err != nil {
		return fmt.Errorf("resolve target %q: %w", target, err)
	}
	policyResolved, err := filepath.EvalSymlinks(policyAbs)
	if err != nil {
		return fmt.Errorf("resolve symlinks for policy root %q: %w", policyAbs, err)
	}
	targetResolved, err := filepath.EvalSymlinks(targetAbs)
	if err != nil {
		return fmt.Errorf("resolve symlinks for target %q: %w", targetAbs, err)
	}
	if policyResolved == targetResolved {
		return fmt.Errorf("target %q resolves to the same directory as the policy root %q; pop_and_move with target == policy root is a no-op (use pop instead)", target, policyRoot)
	}
	policyStat, err := os.Stat(policyResolved)
	if err != nil {
		return fmt.Errorf("stat policy root: %w", err)
	}
	targetStat, err := os.Stat(targetResolved)
	if err != nil {
		return fmt.Errorf("stat target: %w", err)
	}
	if !targetStat.IsDir() {
		return fmt.Errorf("target %q is not a directory", target)
	}
	policySys, ok1 := policyStat.Sys().(*syscall.Stat_t)
	targetSys, ok2 := targetStat.Sys().(*syscall.Stat_t)
	if !ok1 || !ok2 {
		return errors.New("filesystem device-id query unavailable on this platform")
	}
	if policySys.Dev != targetSys.Dev {
		return fmt.Errorf("target %q is on a different filesystem than the policy root %q; os.Rename across filesystems is not atomic, refusing to load", target, policyRoot)
	}
	return nil
}
